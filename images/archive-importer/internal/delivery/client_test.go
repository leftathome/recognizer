package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeGlovebox is a stand-in for the real /v1/archives endpoint that
// covers the subset of tus.io behavior our client exercises.
type fakeGlovebox struct {
	t            *testing.T
	requireToken string
	createCalls  atomic.Int32
	patchCalls   atomic.Int32
	storedAtomic atomic.Pointer[storedUpload]
	// Trigger one PATCH 503 then succeed; used to exercise retry.
	flapOnce atomic.Bool
}

type storedUpload struct {
	id           string
	length       int64
	metadata     string
	body         []byte
	finalized    bool
	sha256Header string
}

func (f *fakeGlovebox) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/archives", f.handleCreate)
	mux.HandleFunc("/v1/archives/", f.handlePatchOrHead)
	return mux
}

func (f *fakeGlovebox) handleCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !f.checkAuth(w, r) {
		return
	}
	f.createCalls.Add(1)
	length, _ := strconv.ParseInt(r.Header.Get("Upload-Length"), 10, 64)
	id := "upload-1"
	f.storedAtomic.Store(&storedUpload{
		id:       id,
		length:   length,
		metadata: r.Header.Get("Upload-Metadata"),
	})
	w.Header().Set("Tus-Resumable", "1.0.0")
	w.Header().Set("Location", "/v1/archives/"+id)
	w.WriteHeader(http.StatusCreated)
}

func (f *fakeGlovebox) handlePatchOrHead(w http.ResponseWriter, r *http.Request) {
	if !f.checkAuth(w, r) {
		return
	}
	st := f.storedAtomic.Load()
	if st == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	switch r.Method {
	case "PATCH":
		f.patchCalls.Add(1)
		if f.flapOnce.CompareAndSwap(true, false) {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		offset, _ := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
		if offset != int64(len(st.body)) {
			w.WriteHeader(http.StatusConflict)
			return
		}
		buf, _ := io.ReadAll(r.Body)
		st.body = append(st.body, buf...)
		w.Header().Set("Tus-Resumable", "1.0.0")
		w.Header().Set("Upload-Offset", strconv.FormatInt(int64(len(st.body)), 10))
		w.WriteHeader(http.StatusNoContent)
		if int64(len(st.body)) == st.length {
			st.finalized = true
		}
	case "HEAD":
		w.Header().Set("Upload-Length", strconv.FormatInt(st.length, 10))
		w.Header().Set("Upload-Offset", strconv.FormatInt(int64(len(st.body)), 10))
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *fakeGlovebox) checkAuth(w http.ResponseWriter, r *http.Request) bool {
	if h := r.Header.Get("Authorization"); h != "Bearer "+f.requireToken {
		w.WriteHeader(http.StatusUnauthorized)
		return false
	}
	if r.Header.Get("Tus-Resumable") != "1.0.0" && r.Method != "OPTIONS" {
		w.WriteHeader(http.StatusPreconditionFailed)
		return false
	}
	return true
}

func TestUpload_HappyPath_MultipleChunks(t *testing.T) {
	fg := &fakeGlovebox{t: t, requireToken: "tok"}
	srv := httptest.NewServer(fg.handler())
	defer srv.Close()

	// 80 KB body, chunk size 32 KB -> 3 PATCHes (32, 32, 16).
	body := make([]byte, 80*1024)
	for i := range body {
		body[i] = byte(i % 251)
	}
	tmp := t.TempDir()
	bodyPath := filepath.Join(tmp, "body.bin")
	must(t, os.WriteFile(bodyPath, body, 0644))
	sum := sha256.Sum256(body)

	c := NewClient(srv.URL, "tok", "recognizer-smoke-test", nil)
	c.ChunkSize = 32 * 1024

	item := Item{
		ArchiveID: "deadbeef-mail", ArchiveFilename: "f.mbox",
		MediaType: "archive/mbox", MatcherID: "google-takeout/mail",
		SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(body)),
	}
	loc, err := c.Upload(context.Background(), item, bodyPath)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if !strings.Contains(loc, "/v1/archives/upload-1") {
		t.Errorf("Location = %s", loc)
	}
	if fg.createCalls.Load() != 1 {
		t.Errorf("createCalls = %d, want 1", fg.createCalls.Load())
	}
	if fg.patchCalls.Load() != 3 {
		t.Errorf("patchCalls = %d, want 3", fg.patchCalls.Load())
	}
	st := fg.storedAtomic.Load()
	if !st.finalized {
		t.Error("server should have marked finalized at last PATCH")
	}
	if !equalBytes(st.body, body) {
		t.Errorf("stored body length %d != sent %d", len(st.body), len(body))
	}
}

func TestUpload_RetriesOn503(t *testing.T) {
	fg := &fakeGlovebox{t: t, requireToken: "tok"}
	fg.flapOnce.Store(true)
	srv := httptest.NewServer(fg.handler())
	defer srv.Close()

	body := []byte("small body")
	tmp := t.TempDir()
	bodyPath := filepath.Join(tmp, "body.bin")
	must(t, os.WriteFile(bodyPath, body, 0644))
	sum := sha256.Sum256(body)

	c := NewClient(srv.URL, "tok", "recognizer-smoke-test", nil)
	c.ChunkSize = int64(len(body))
	c.MaxRetries = 1
	// Tests should not actually sleep. Skip backoff by setting MaxRetries
	// just-high-enough; the backoff sleep is short on attempt=0 anyway.
	_, err := c.Upload(context.Background(), Item{
		ArchiveID: "x", ArchiveFilename: "f", MediaType: "archive/mbox",
		MatcherID: "m", SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(body)),
	}, bodyPath)
	if err != nil {
		t.Fatalf("Upload should succeed after one 503: %v", err)
	}
	if fg.patchCalls.Load() != 2 {
		t.Errorf("patchCalls = %d, want 2 (one 503 + one success)", fg.patchCalls.Load())
	}
}

func TestUpload_Replay303_AlreadyComplete(t *testing.T) {
	// Server returns 303 on POST (replay) + 200 on HEAD with
	// Upload-Offset == Upload-Length. Upload() should treat this as
	// success without sending any PATCH.
	body := []byte("anything")
	patches := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/archives", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/v1/archives/already")
		w.WriteHeader(http.StatusSeeOther)
	})
	mux.HandleFunc("/v1/archives/already", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "HEAD":
			w.Header().Set("Upload-Length", strconv.Itoa(len(body)))
			w.Header().Set("Upload-Offset", strconv.Itoa(len(body)))
			w.WriteHeader(http.StatusOK)
		case "PATCH":
			patches++
			w.WriteHeader(http.StatusNoContent)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tmp := t.TempDir()
	bodyPath := filepath.Join(tmp, "body.bin")
	must(t, os.WriteFile(bodyPath, body, 0644))
	sum := sha256.Sum256(body)

	c := NewClient(srv.URL, "tok", "recognizer-smoke-test", nil)
	loc, err := c.Upload(context.Background(), Item{
		ArchiveID: "x", ArchiveFilename: "f", MediaType: "archive/mbox",
		MatcherID: "m", SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(body)),
	}, bodyPath)
	if err != nil {
		t.Fatalf("303-then-complete-HEAD should be success: %v", err)
	}
	if !strings.Contains(loc, "/v1/archives/already") {
		t.Errorf("Location = %s", loc)
	}
	if patches != 0 {
		t.Errorf("no PATCH should be sent when server already has full upload; got %d", patches)
	}
}

func TestUpload_Replay303_ResumeFromPartial(t *testing.T) {
	// Server returns 303 on POST (replay) + 200 on HEAD with
	// Upload-Offset < Upload-Length. Upload() should resume PATCHing
	// from the server's reported offset.
	body := make([]byte, 100)
	for i := range body {
		body[i] = byte(i)
	}
	const partial = 40
	var received int
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/archives", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/v1/archives/partial")
		w.WriteHeader(http.StatusSeeOther)
	})
	mux.HandleFunc("/v1/archives/partial", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "HEAD":
			w.Header().Set("Upload-Length", strconv.Itoa(len(body)))
			w.Header().Set("Upload-Offset", strconv.Itoa(partial+received))
			w.WriteHeader(http.StatusOK)
		case "PATCH":
			off, _ := strconv.Atoi(r.Header.Get("Upload-Offset"))
			if off != partial+received {
				w.WriteHeader(http.StatusConflict)
				return
			}
			n, _ := io.Copy(io.Discard, r.Body)
			received += int(n)
			w.Header().Set("Upload-Offset", strconv.Itoa(partial+received))
			w.WriteHeader(http.StatusNoContent)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tmp := t.TempDir()
	bodyPath := filepath.Join(tmp, "body.bin")
	must(t, os.WriteFile(bodyPath, body, 0644))
	sum := sha256.Sum256(body)

	c := NewClient(srv.URL, "tok", "recognizer-smoke-test", nil)
	c.ChunkSize = 32
	_, err := c.Upload(context.Background(), Item{
		ArchiveID: "x", ArchiveFilename: "f", MediaType: "archive/mbox",
		MatcherID: "m", SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(body)),
	}, bodyPath)
	if err != nil {
		t.Fatalf("resume should succeed: %v", err)
	}
	if got := partial + received; got != len(body) {
		t.Errorf("server got %d bytes, want %d (partial=%d, resumed=%d)", got, len(body), partial, received)
	}
}

func TestUpload_HeadResyncAfterPatchError(t *testing.T) {
	// Simulates: PATCH-1 transmits half its chunk, server's view of
	// the upload advances by what it received, the connection drops
	// (we synthesize this with a 500 after the partial read), our
	// client retries from its local offset which is now stale, the
	// retry fails 409 offset_mismatch, then our HEAD-resync catches
	// the server's actual offset and continues.
	body := make([]byte, 100)
	for i := range body {
		body[i] = byte(i % 251)
	}
	var serverOffset int64
	var patchAttempts int
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/archives", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/v1/archives/x")
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("/v1/archives/x", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "HEAD":
			w.Header().Set("Upload-Length", strconv.Itoa(len(body)))
			w.Header().Set("Upload-Offset", strconv.FormatInt(serverOffset, 10))
			w.WriteHeader(http.StatusOK)
		case "PATCH":
			patchAttempts++
			off, _ := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
			if patchAttempts == 1 {
				// Read half the chunk, advance server offset by what we
				// read, then drop the connection mid-stream by writing
				// a 500 and not finishing.
				half := make([]byte, 20)
				io.ReadFull(r.Body, half)
				serverOffset += 20
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if off != serverOffset {
				w.Header().Set("Upload-Offset", strconv.FormatInt(serverOffset, 10))
				w.WriteHeader(http.StatusConflict)
				return
			}
			n, _ := io.Copy(io.Discard, r.Body)
			serverOffset += n
			w.Header().Set("Upload-Offset", strconv.FormatInt(serverOffset, 10))
			w.WriteHeader(http.StatusNoContent)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tmp := t.TempDir()
	bodyPath := filepath.Join(tmp, "body.bin")
	must(t, os.WriteFile(bodyPath, body, 0644))
	sum := sha256.Sum256(body)

	c := NewClient(srv.URL, "tok", "recognizer-smoke-test", nil)
	c.ChunkSize = 40
	c.MaxRetries = 0
	_, err := c.Upload(context.Background(), Item{
		ArchiveID: "x", ArchiveFilename: "f", MediaType: "archive/mbox",
		MatcherID: "m", SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(body)),
	}, bodyPath)
	if err != nil {
		t.Fatalf("HEAD-resync should recover from mid-chunk failure: %v", err)
	}
	if serverOffset != int64(len(body)) {
		t.Errorf("server got %d bytes, want %d", serverOffset, len(body))
	}
}

func TestUpload_4xxNonRetryable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/archives", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"unauthorized","message":"bad token"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body := []byte("anything")
	tmp := t.TempDir()
	bodyPath := filepath.Join(tmp, "body.bin")
	must(t, os.WriteFile(bodyPath, body, 0644))
	sum := sha256.Sum256(body)

	c := NewClient(srv.URL, "wrong", "recognizer-smoke-test", nil)
	_, err := c.Upload(context.Background(), Item{
		ArchiveID: "x", ArchiveFilename: "f", MediaType: "archive/mbox",
		MatcherID: "m", SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(body)),
	}, bodyPath)
	if err == nil {
		t.Fatal("expected error on 401")
	}
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 401 {
		t.Errorf("expected HTTPError 401, got %T %v", err, err)
	}
}

// glovebox v0.8.0 returns an opaque `500 internal_finalize` when finalize
// fails for content reasons (in practice a missing/whitespace-only ocr.txt in
// an archive/recognizer-scan tarball). That is a content bug, not
// backpressure, so it must fail on the FIRST response rather than re-uploading
// the final chunk MaxRetries times into the same verdict.
func TestUpload_FinalizeContentErrorIsNotRetried(t *testing.T) {
	var patches int
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/archives", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/v1/archives/up-1")
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("/v1/archives/up-1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" {
			w.WriteHeader(http.StatusOK)
			return
		}
		patches++
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"internal_finalize","message":"finalize failed"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body := []byte("payload")
	tmp := t.TempDir()
	bodyPath := filepath.Join(tmp, "body.bin")
	must(t, os.WriteFile(bodyPath, body, 0644))
	sum := sha256.Sum256(body)

	c := NewClient(srv.URL, "tok", "recognizer-scanner", nil)
	_, err := c.Upload(context.Background(), Item{
		ArchiveID: "x", ArchiveFilename: "f", MediaType: "archive/recognizer-scan",
		MatcherID: "m", SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(body)),
	}, bodyPath)
	if !errors.Is(err, ErrFinalizeContent) {
		t.Fatalf("expected ErrFinalizeContent, got %T %v", err, err)
	}
	if patches != 1 {
		t.Errorf("expected exactly 1 PATCH (no retries), got %d", patches)
	}
}

// A 5xx whose body is not glovebox's error envelope stays retryable -- a
// truncated or proxy-generated 5xx really is transient.
func TestUpload_Opaque5xxStillRetries(t *testing.T) {
	var patches int
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/archives", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/v1/archives/up-2")
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("/v1/archives/up-2", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" {
			w.WriteHeader(http.StatusOK)
			return
		}
		patches++
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "<html>gateway blew up</html>")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body := []byte("payload")
	tmp := t.TempDir()
	bodyPath := filepath.Join(tmp, "body.bin")
	must(t, os.WriteFile(bodyPath, body, 0644))
	sum := sha256.Sum256(body)

	c := NewClient(srv.URL, "tok", "recognizer-smoke-test", nil)
	c.MaxRetries = 1 // keep the test's backoff short
	_, err := c.Upload(context.Background(), Item{
		ArchiveID: "x", ArchiveFilename: "f", MediaType: "archive/mbox",
		MatcherID: "m", SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(body)),
	}, bodyPath)
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, ErrFinalizeContent) {
		t.Error("opaque 5xx must not be classified as a finalize content error")
	}
	if patches != 2 {
		t.Errorf("expected 2 PATCH attempts (initial + 1 retry), got %d", patches)
	}
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

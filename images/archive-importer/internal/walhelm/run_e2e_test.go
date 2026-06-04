package walhelmsrc

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	walhelm "github.com/leftathome/walhelm-go"
)

// fakeTusServer is a minimal tus.io v1.0.0 endpoint sufficient for the real
// delivery.Client to complete a single upload. It mirrors the relevant subset
// of internal/delivery's fakeGlovebox (which we cannot import cross-package).
//
// Surface implemented (the exact wire contract delivery.Client exercises):
//   - precondition: every request must carry Tus-Resumable: 1.0.0 (else 412)
//     and Authorization: Bearer <token> (else 401).
//   - POST /v1/archives: capture Upload-Metadata + Upload-Length, return 201
//     with a relative Location.
//   - PATCH <location>: append the body bytes (at the declared Upload-Offset),
//     advance the offset, return 204 with the new Upload-Offset.
//   - HEAD <location>: return the current Upload-Offset (resync path; not hit
//     on the happy path, but cheap to provide).
type fakeTusServer struct {
	token string

	// forceFailCreate, when true, causes POST /v1/archives to return 500 so
	// the upload fails before any state is saved. Used by
	// TestRunOnce_UploadError_NoStateSave to lock in the at-least-once guarantee.
	forceFailCreate bool

	mu          sync.Mutex
	postCalls   int
	metadata    string // raw Upload-Metadata header captured at POST
	uploadLen   int64
	body        []byte // accumulated PATCH bytes
	location    string
}

func (s *fakeTusServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/archives", s.handleCreate)
	mux.HandleFunc("/v1/archives/", s.handlePatchOrHead)
	return mux
}

func (s *fakeTusServer) checkPreconditions(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Authorization") != "Bearer "+s.token {
		w.WriteHeader(http.StatusUnauthorized)
		return false
	}
	if r.Header.Get("Tus-Resumable") != "1.0.0" {
		w.WriteHeader(http.StatusPreconditionFailed)
		return false
	}
	return true
}

func (s *fakeTusServer) handleCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.checkPreconditions(w, r) {
		return
	}
	s.mu.Lock()
	s.postCalls++
	fail := s.forceFailCreate
	s.mu.Unlock()

	if fail {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	s.metadata = r.Header.Get("Upload-Metadata")
	s.uploadLen, _ = strconv.ParseInt(r.Header.Get("Upload-Length"), 10, 64)
	s.location = "/v1/archives/walhelm-upload-1"
	loc := s.location
	s.mu.Unlock()

	w.Header().Set("Tus-Resumable", "1.0.0")
	w.Header().Set("Location", loc)
	w.WriteHeader(http.StatusCreated)
}

func (s *fakeTusServer) handlePatchOrHead(w http.ResponseWriter, r *http.Request) {
	if !s.checkPreconditions(w, r) {
		return
	}
	switch r.Method {
	case http.MethodPatch:
		offset, _ := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
		s.mu.Lock()
		if offset != int64(len(s.body)) {
			s.mu.Unlock()
			w.WriteHeader(http.StatusConflict)
			return
		}
		buf, _ := io.ReadAll(r.Body)
		s.body = append(s.body, buf...)
		newOffset := int64(len(s.body))
		s.mu.Unlock()

		w.Header().Set("Tus-Resumable", "1.0.0")
		w.Header().Set("Upload-Offset", strconv.FormatInt(newOffset, 10))
		w.WriteHeader(http.StatusNoContent)
	case http.MethodHead:
		s.mu.Lock()
		offset := int64(len(s.body))
		length := s.uploadLen
		s.mu.Unlock()
		w.Header().Set("Tus-Resumable", "1.0.0")
		w.Header().Set("Upload-Length", strconv.FormatInt(length, 10))
		w.Header().Set("Upload-Offset", strconv.FormatInt(offset, 10))
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// decodeUploadMetadata parses a tus.io Upload-Metadata header
// ("key BASE64(value),key BASE64(value),...") into a key->value map. Values
// use StdEncoding per the delivery.Item contract.
func decodeUploadMetadata(t *testing.T, header string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	if strings.TrimSpace(header) == "" {
		return out
	}
	for _, pair := range strings.Split(header, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, " ", 2)
		key := parts[0]
		if len(parts) == 1 {
			out[key] = ""
			continue
		}
		dec, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			t.Fatalf("Upload-Metadata value for %q is not valid base64: %v", key, err)
		}
		out[key] = string(dec)
	}
	return out
}

// TestRunOnce_E2E_DeliversWalhelmExport drives the whole path: a fake KP client
// -> RunOnce -> the REAL delivery.Client -> a minimal but genuine tus.io server.
// It asserts the provenance metadata the real client put on the wire, the tar
// layout of the delivered body, and that the cursor advanced only after success.
func TestRunOnce_E2E_DeliversWalhelmExport(t *testing.T) {
	srv := &fakeTusServer{token: "tkn"}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	fake := &fakeWalhelmClient{
		acctID: "leftathome",
		folders: []walhelm.Folder{
			{ID: "f1", Name: "Inbox", Count: 2, UnreadCount: 1},
		},
		convsByFolder: map[string][]walhelm.ConversationSummary{
			"f1": {
				{ID: "c1", FolderID: "f1", Subject: "Thread one"},
				{ID: "c2", FolderID: "f1", Subject: "Thread two"},
			},
		},
		convByID: map[string]*walhelm.Conversation{
			"c1": {
				ConversationSummary: walhelm.ConversationSummary{ID: "c1", FolderID: "f1", Subject: "Thread one"},
				Messages:            []walhelm.Message{{ID: "m1", Sender: "doctor", Body: "hello"}},
			},
			"c2": {
				ConversationSummary: walhelm.ConversationSummary{ID: "c2", FolderID: "f1", Subject: "Thread two"},
				Messages:            []walhelm.Message{{ID: "m2", Sender: "nurse", Body: "results in"}},
			},
		},
		labPanels: []walhelm.LabPanel{
			{ID: "lp1", Name: "CMP", Status: "Final"},
		},
		records: []walhelm.MedicalRecord{
			{ID: "r1", Title: "Visit summary", Type: "note"},
		},
	}

	cfg := RunConfig{
		SubjectPrincipal: "walhelm:9f2a",
		StateDir:         t.TempDir(),
		IngestURL:        ts.URL,
		IngestToken:      "tkn",
		IngestSourceID:   "recognizer-test",
		HTTPClient:       ts.Client(),
	}

	delivered, err := RunOnce(context.Background(), fake, cfg)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !delivered {
		t.Fatal("RunOnce delivered = false, want true")
	}

	// --- Step 5: provenance metadata as the REAL client sent it. ---
	if srv.postCalls != 1 {
		t.Fatalf("server POST count = %d, want 1", srv.postCalls)
	}
	meta := decodeUploadMetadata(t, srv.metadata)
	wantMeta := map[string]string{
		"media_type":      "archive/walhelm-export",
		"matcher_id":      "walhelm/export",
		"acq_provider":    "kp-wa",
		"acq_account_id":  "leftathome",
		"acq_auth_method": "browser_session",
		"data_subject":    "walhelm:9f2a",
		"audience":        "subject",
		"provider":        "recognizer-test",
	}
	for k, want := range wantMeta {
		if got := meta[k]; got != want {
			t.Errorf("Upload-Metadata[%q] = %q, want %q", k, got, want)
		}
	}

	// --- Step 6: tar body layout. ---
	if int64(len(srv.body)) != srv.uploadLen {
		t.Fatalf("PATCHed body = %d bytes, Upload-Length = %d", len(srv.body), srv.uploadLen)
	}
	counts := tarEntryCounts(t, srv.body)
	if counts["messages/"] < 2 {
		t.Errorf("tar messages/ files = %d, want >= 2", counts["messages/"])
	}
	if counts["labs/"] < 1 {
		t.Errorf("tar labs/ files = %d, want >= 1", counts["labs/"])
	}
	if counts["records/"] < 1 {
		t.Errorf("tar records/ files = %d, want >= 1", counts["records/"])
	}

	// --- Step 7: cursor advanced (SaveState ran only after delivery). ---
	st, err := LoadState(cfg.StateDir, cfg.SubjectPrincipal)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if st.MessagesSince.IsZero() || st.LabsSince.IsZero() || st.RecordsSince.IsZero() {
		t.Errorf("state cursors not advanced: %+v", st)
	}
}

// TestRunOnce_UploadError_NoStateSave locks in the at-least-once delivery
// invariant's critical branch: when the upload POST returns 500 (or any
// delivery error), RunOnce must return (false, non-nil err) AND must NOT write
// a state file. Cursors must not advance so the next run re-fetches and
// re-delivers the same data.
func TestRunOnce_UploadError_NoStateSave(t *testing.T) {
	srv := &fakeTusServer{token: "tkn", forceFailCreate: true}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	// Provide enough data so Fetch returns items > 0, guaranteeing RunOnce
	// reaches the delivery step before the upload fails.
	fake := &fakeWalhelmClient{
		acctID: "leftathome",
		folders: []walhelm.Folder{
			{ID: "f1", Name: "Inbox"},
		},
		convsByFolder: map[string][]walhelm.ConversationSummary{
			"f1": {{ID: "c1", FolderID: "f1", Subject: "Thread one"}},
		},
		convByID: map[string]*walhelm.Conversation{
			"c1": {
				ConversationSummary: walhelm.ConversationSummary{ID: "c1", FolderID: "f1", Subject: "Thread one"},
				Messages:            []walhelm.Message{{ID: "m1", Sender: "doctor", Body: "hello"}},
			},
		},
	}

	stateDir := t.TempDir()
	cfg := RunConfig{
		SubjectPrincipal: "walhelm:9f2a",
		StateDir:         stateDir,
		IngestURL:        ts.URL,
		IngestToken:      "tkn",
		IngestSourceID:   "recognizer-test",
		HTTPClient:       ts.Client(),
	}

	delivered, err := RunOnce(context.Background(), fake, cfg)

	// Must report failure.
	if err == nil {
		t.Fatal("RunOnce error = nil, want non-nil on upload failure")
	}
	if delivered {
		t.Fatalf("RunOnce delivered = true, want false on upload failure")
	}

	// Cursors must NOT have advanced: state file must not exist.
	statePath := stateFilePath(stateDir, cfg.SubjectPrincipal)
	if _, statErr := os.Stat(statePath); !os.IsNotExist(statErr) {
		t.Fatalf("state file %s must not exist after upload failure (at-least-once guarantee); stat err = %v", statePath, statErr)
	}
}

// tarEntryCounts parses tarBytes and counts regular-file entries grouped by
// their top-level directory prefix (e.g. "messages/").
func tarEntryCounts(t *testing.T, tarBytes []byte) map[string]int {
	t.Helper()
	counts := make(map[string]int)
	tr := tar.NewReader(bytes.NewReader(tarBytes))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar read: %v", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := strings.TrimPrefix(hdr.Name, "./")
		for _, prefix := range []string{"messages/", "labs/", "records/"} {
			if strings.HasPrefix(name, prefix) {
				counts[prefix]++
			}
		}
	}
	return counts
}

// TestRunOnce_E2E_EmptyFetch_NoDelivery: a fake KP client with nothing new must
// yield (false, nil), must not touch the tus.io server at all, and must not
// write a state file (cursors stay put for the next run).
func TestRunOnce_E2E_EmptyFetch_NoDelivery(t *testing.T) {
	srv := &fakeTusServer{token: "tkn"}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	fake := &fakeWalhelmClient{acctID: "leftathome"} // no folders/labs/records

	cfg := RunConfig{
		SubjectPrincipal: "walhelm:9f2a",
		StateDir:         t.TempDir(),
		IngestURL:        ts.URL,
		IngestToken:      "tkn",
		IngestSourceID:   "recognizer-test",
		HTTPClient:       ts.Client(),
	}

	delivered, err := RunOnce(context.Background(), fake, cfg)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if delivered {
		t.Fatal("RunOnce delivered = true, want false on empty fetch")
	}
	if srv.postCalls != 0 {
		t.Errorf("server POST count = %d, want 0 (no delivery on empty fetch)", srv.postCalls)
	}
	if _, err := os.Stat(stateFilePath(cfg.StateDir, cfg.SubjectPrincipal)); !os.IsNotExist(err) {
		t.Errorf("state file should not exist after empty fetch; stat err = %v", err)
	}
}

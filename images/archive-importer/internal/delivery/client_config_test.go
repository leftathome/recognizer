package delivery

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// --- B4: token-via-file, re-read per delivery ---

// tokenCheckServer is a minimal tus.io stand-in that records the bearer
// token seen on every request (POST create + PATCH) so tests can assert
// exactly which token was used per-request.
type tokenCheckServer struct {
	mu     sync.Mutex
	tokens []string // Authorization header value (without "Bearer ") seen, in order
	body   []byte
}

func (s *tokenCheckServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/archives", func(w http.ResponseWriter, r *http.Request) {
		s.recordAuth(r)
		w.Header().Set("Location", "/v1/archives/x")
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("/v1/archives/x", func(w http.ResponseWriter, r *http.Request) {
		s.recordAuth(r)
		switch r.Method {
		case "PATCH":
			s.mu.Lock()
			s.body = append(s.body, mustReadAll(r)...)
			n := len(s.body)
			s.mu.Unlock()
			w.Header().Set("Upload-Offset", strconv.Itoa(n))
			w.WriteHeader(http.StatusNoContent)
		case "HEAD":
			s.mu.Lock()
			n := len(s.body)
			s.mu.Unlock()
			w.Header().Set("Upload-Offset", strconv.Itoa(n))
			w.WriteHeader(http.StatusOK)
		}
	})
	return mux
}

func (s *tokenCheckServer) recordAuth(r *http.Request) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	s.tokens = append(s.tokens, tok)
	s.mu.Unlock()
}

func (s *tokenCheckServer) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.tokens))
	copy(out, s.tokens)
	return out
}

func mustReadAll(r *http.Request) []byte {
	buf, _ := io.ReadAll(r.Body)
	return buf
}

func uploadOnce(t *testing.T, c *Client, body []byte) {
	t.Helper()
	tmp := t.TempDir()
	bodyPath := filepath.Join(tmp, "body.bin")
	must(t, os.WriteFile(bodyPath, body, 0644))
	sum := sha256.Sum256(body)
	_, err := c.Upload(context.Background(), Item{
		ArchiveID: "x", ArchiveFilename: "f", MediaType: "archive/mbox",
		MatcherID: "m", SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(body)),
	}, bodyPath)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
}

func TestResolveTokenSource_FileWins_UsedOnEveryRequest(t *testing.T) {
	srv := &tokenCheckServer{}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	tokenFile := filepath.Join(t.TempDir(), "token")
	must(t, os.WriteFile(tokenFile, []byte("tok-A\n"), 0600))

	source, err := ResolveTokenSource(tokenFile, "")
	if err != nil {
		t.Fatalf("ResolveTokenSource: %v", err)
	}

	c := NewClient(ts.URL, "", "recognizer-smoke-test", nil)
	c.TokenSource = source
	uploadOnce(t, c, []byte("first delivery"))

	for _, got := range srv.seen() {
		if got != "tok-A" {
			t.Errorf("request used token %q, want tok-A (trimmed)", got)
		}
	}
}

func TestResolveTokenSource_FileRotated_SecondRequestUsesNewValue(t *testing.T) {
	srv := &tokenCheckServer{}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	tokenFile := filepath.Join(t.TempDir(), "token")
	must(t, os.WriteFile(tokenFile, []byte("tok-old"), 0600))

	source, err := ResolveTokenSource(tokenFile, "")
	if err != nil {
		t.Fatalf("ResolveTokenSource: %v", err)
	}
	c := NewClient(ts.URL, "", "recognizer-smoke-test", nil)
	c.TokenSource = source

	// First delivery: old token.
	uploadOnce(t, c, []byte("delivery one"))
	first := srv.seen()
	if len(first) == 0 || first[0] != "tok-old" {
		t.Fatalf("first delivery tokens = %v, want first entry tok-old", first)
	}

	// Rotate (simulates Vault re-projecting the Secret file).
	must(t, os.WriteFile(tokenFile, []byte("tok-new"), 0600))

	// Second delivery must use the rotated value, without any restart or
	// re-construction of the Client.
	uploadOnce(t, c, []byte("delivery two"))
	all := srv.seen()
	last := all[len(all)-1]
	if last != "tok-new" {
		t.Errorf("second delivery's last request token = %q, want tok-new", last)
	}
	// And none of the second delivery's requests should still be on the
	// old value.
	for _, got := range all[len(first):] {
		if got != "tok-new" {
			t.Errorf("second delivery used stale token %q", got)
		}
	}
}

func TestResolveTokenSource_FileVarUnset_EnvUsed(t *testing.T) {
	source, err := ResolveTokenSource("", "env-token")
	if err != nil {
		t.Fatalf("ResolveTokenSource: %v", err)
	}
	tok, err := source()
	if err != nil {
		t.Fatalf("source(): %v", err)
	}
	if tok != "env-token" {
		t.Errorf("token = %q, want env-token", tok)
	}
}

func TestResolveTokenSource_NeitherSet_Error(t *testing.T) {
	_, err := ResolveTokenSource("", "")
	if err == nil {
		t.Fatal("expected error when neither token nor token-file is set")
	}
}

func TestResolveTokenSource_FileReadError_FailsRequest(t *testing.T) {
	source, err := ResolveTokenSource(filepath.Join(t.TempDir(), "does-not-exist"), "")
	if err != nil {
		t.Fatalf("ResolveTokenSource should not fail at construction: %v", err)
	}
	if _, err := source(); err == nil {
		t.Fatal("expected error reading a missing token file")
	}
}

func TestNewClientFromConfig_NoToken_Error(t *testing.T) {
	_, err := NewClientFromConfig(ClientConfig{BaseURL: "http://example.invalid", SourceID: "s"})
	if err == nil {
		t.Fatal("expected error when neither Token nor TokenFile is set")
	}
}

// --- A2: TLS-capable client ---

func TestNewClientFromConfig_TLS_CustomCA_Succeeds(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/v1/archives/x")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	caFile := writeCertPEM(t, srv.Certificate())

	c, err := NewClientFromConfig(ClientConfig{
		BaseURL: srv.URL, SourceID: "recognizer-smoke-test", Token: "tok", CAFile: caFile,
	})
	if err != nil {
		t.Fatalf("NewClientFromConfig: %v", err)
	}

	body := []byte("tls happy path")
	tmp := t.TempDir()
	bodyPath := filepath.Join(tmp, "body.bin")
	must(t, os.WriteFile(bodyPath, body, 0644))
	sum := sha256.Sum256(body)
	// The stub server only implements POST (201, no PATCH handler), which
	// is enough to prove the TLS handshake with the pinned CA succeeds --
	// Upload's PATCH loop would 404 against the default mux, so assert
	// only on the transport-level outcome via create() directly.
	_, replay, err := c.create(context.Background(), Item{
		ArchiveID: "x", ArchiveFilename: "f", MediaType: "archive/mbox",
		MatcherID: "m", SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(body)),
	})
	if err != nil {
		t.Fatalf("create over TLS with pinned CA should succeed: %v", err)
	}
	if replay {
		t.Error("expected a fresh (non-replay) create")
	}
}

func TestNewClientFromConfig_TLS_WithoutCA_X509Failure(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	// No CAFile: the server's self-signed cert is untrusted.
	c, err := NewClientFromConfig(ClientConfig{
		BaseURL: srv.URL, SourceID: "recognizer-smoke-test", Token: "tok",
	})
	if err != nil {
		t.Fatalf("NewClientFromConfig: %v", err)
	}

	_, _, err = c.create(context.Background(), Item{
		ArchiveID: "x", ArchiveFilename: "f", MediaType: "archive/mbox",
		MatcherID: "m", SHA256: "aa", SizeBytes: 1,
	})
	if err == nil {
		t.Fatal("expected an x509 trust failure without a pinned CA")
	}
	var unknownAuth x509.UnknownAuthorityError
	if !strings.Contains(err.Error(), "certificate signed by unknown authority") && !errors.As(err, &unknownAuth) {
		t.Errorf("expected an x509 unknown-authority error, got: %v", err)
	}
}

func TestNewClientFromConfig_RequireTLS_HTTPURL_Error(t *testing.T) {
	_, err := NewClientFromConfig(ClientConfig{
		BaseURL: "http://glovebox.example.local:9091", SourceID: "s", Token: "tok", RequireTLS: true,
	})
	if err == nil {
		t.Fatal("expected error constructing a client with RequireTLS against an http:// URL")
	}
	if !strings.Contains(err.Error(), "PHI") {
		t.Errorf("expected the error to name the PHI rationale, got: %v", err)
	}
}

func TestNewClientFromConfig_RequireTLS_HTTPSURL_OK(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	caFile := writeCertPEM(t, srv.Certificate())

	_, err := NewClientFromConfig(ClientConfig{
		BaseURL: srv.URL, SourceID: "s", Token: "tok", RequireTLS: true, CAFile: caFile,
	})
	if err != nil {
		t.Fatalf("https:// URL with RequireTLS should construct cleanly: %v", err)
	}
}

func writeCertPEM(t *testing.T, cert *x509.Certificate) string {
	t.Helper()
	if cert == nil {
		t.Fatal("test TLS server exposed no certificate")
	}
	block := &pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}
	path := filepath.Join(t.TempDir(), "ca.pem")
	must(t, os.WriteFile(path, pem.EncodeToMemory(block), 0644))
	return path
}

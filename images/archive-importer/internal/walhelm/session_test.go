package walhelmsrc

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	walhelm "github.com/leftathome/walhelm-go"
)

// buildSessionFile marshals a walhelm.Session into a temp file and returns
// the path. Using the library's own type guarantees the JSON shape matches.
func buildSessionFile(t *testing.T, s walhelm.Session) string {
	t.Helper()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal session fixture: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write session fixture: %v", err)
	}
	return path
}

func TestLoadSession_ValidSession(t *testing.T) {
	sess := walhelm.Session{
		UserID:    "user-abc",
		CreatedAt: time.Now().Add(-1 * time.Hour),
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	path := buildSessionFile(t, sess)

	got, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession returned error for valid session: %v", err)
	}
	if got == nil {
		t.Fatal("LoadSession returned nil session without error")
	}
	if got.UserID != "user-abc" {
		t.Errorf("UserID = %q, want %q", got.UserID, "user-abc")
	}
	if !got.IsValid() {
		t.Error("loaded session reports IsValid() = false, want true")
	}
}

func TestLoadSession_ExpiredSession(t *testing.T) {
	sess := walhelm.Session{
		UserID:    "user-expired",
		CreatedAt: time.Now().Add(-48 * time.Hour),
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	}
	path := buildSessionFile(t, sess)

	_, err := LoadSession(path)
	if err == nil {
		t.Fatal("LoadSession returned no error for expired session, want ErrSessionExpired")
	}
	if !errors.Is(err, ErrSessionExpired) {
		t.Errorf("LoadSession error = %v, want errors.Is(err, ErrSessionExpired) to be true", err)
	}
}

func TestLoadSession_MissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	_, err := LoadSession(path)
	if err == nil {
		t.Fatal("LoadSession returned no error for missing file, want an error")
	}
	// Should NOT be ErrSessionExpired -- it should be an OS-level error.
	if errors.Is(err, ErrSessionExpired) {
		t.Errorf("LoadSession missing-file error should not be ErrSessionExpired, got: %v", err)
	}
}

func TestLoadSession_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0600); err != nil {
		t.Fatalf("write malformed fixture: %v", err)
	}

	_, err := LoadSession(path)
	if err == nil {
		t.Fatal("LoadSession returned no error for malformed JSON, want an error")
	}
	if errors.Is(err, ErrSessionExpired) {
		t.Errorf("LoadSession malformed-JSON error should not be ErrSessionExpired, got: %v", err)
	}
}

func TestNewClientFromSession_ValidSession(t *testing.T) {
	sess := &walhelm.Session{
		UserID:    "user-abc",
		CreatedAt: time.Now().Add(-1 * time.Hour),
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}

	client, err := NewClientFromSession(sess)
	if err != nil {
		t.Fatalf("NewClientFromSession returned error: %v", err)
	}
	if client == nil {
		t.Fatal("NewClientFromSession returned nil client")
	}
	if got := client.AcqAccountID(); got != "user-abc" {
		t.Errorf("AcqAccountID = %q, want %q", got, "user-abc")
	}
}

func TestNewClientFromSession_IntegrationWithLoad(t *testing.T) {
	// Round-trip: build fixture -> LoadSession -> NewClientFromSession -> AcqAccountID.
	sess := walhelm.Session{
		UserID:    "user-roundtrip",
		CreatedAt: time.Now().Add(-30 * time.Minute),
		ExpiresAt: time.Now().Add(12 * time.Hour),
	}
	path := buildSessionFile(t, sess)

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}

	client, err := NewClientFromSession(loaded)
	if err != nil {
		t.Fatalf("NewClientFromSession: %v", err)
	}

	if got := client.AcqAccountID(); got != "user-roundtrip" {
		t.Errorf("AcqAccountID = %q, want %q", got, "user-roundtrip")
	}
}

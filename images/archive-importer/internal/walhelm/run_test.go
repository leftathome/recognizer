package walhelmsrc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return tm
}

// TestRunOnceEmptyFetchNoDelivery: a fake client with nothing new must yield
// (false, nil) and must NOT write a state file (cursors stay put for the next
// run). The empty-fetch path is exercised here; the deliver-success e2e is
// Task 8.
func TestRunOnceEmptyFetchNoDelivery(t *testing.T) {
	stateDir := t.TempDir()
	cli := &fakeWalhelmClient{
		// No folders, no labs, no records => Fetch returns items == 0.
		acctID: "acct-123",
	}
	cfg := RunConfig{
		SubjectPrincipal: "walhelm:9f2a",
		StateDir:         stateDir,
		IngestURL:        "http://example.invalid",
		IngestToken:      "tok",
		IngestSourceID:   "recognizer-test",
	}

	delivered, err := RunOnce(context.Background(), cli, cfg)
	if err != nil {
		t.Fatalf("RunOnce error = %v, want nil", err)
	}
	if delivered {
		t.Fatalf("delivered = true, want false on an empty fetch")
	}

	// No state file must have been written.
	statePath := filepath.Join(stateDir, "state-walhelm_9f2a.json")
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state file %s exists (err=%v); empty fetch must not save state", statePath, err)
	}
}

// TestRunOnceFetchErrorPropagated: a client error during Fetch must surface
// from RunOnce as (false, err), with no state saved.
func TestRunOnceFetchErrorPropagated(t *testing.T) {
	stateDir := t.TempDir()
	sentinel := errors.New("boom")
	cli := &fakeWalhelmClient{
		foldersErr: sentinel,
	}
	cfg := RunConfig{
		SubjectPrincipal: "walhelm:9f2a",
		StateDir:         stateDir,
		IngestURL:        "http://example.invalid",
		IngestToken:      "tok",
		IngestSourceID:   "recognizer-test",
	}

	delivered, err := RunOnce(context.Background(), cli, cfg)
	if err == nil {
		t.Fatalf("RunOnce error = nil, want non-nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("RunOnce error = %v, want it to wrap sentinel %v", err, sentinel)
	}
	if delivered {
		t.Fatalf("delivered = true, want false on a fetch error")
	}
	if entries, _ := os.ReadDir(stateDir); len(entries) != 0 {
		t.Fatalf("state dir not empty after fetch error: %v", entries)
	}
}

// TestArchiveID exercises the deterministic archive_id derivation and the
// glovebox format constraint (^[a-zA-Z0-9._-]{1,128}$).
func TestArchiveID(t *testing.T) {
	st := State{}
	st.MessagesSince = mustTime(t, "2026-06-03T12:34:56Z")
	got := archiveID("walhelm:9f2a", st)
	want := "walhelm-walhelm_9f2a-20260603T123456Z"
	if got != want {
		t.Fatalf("archiveID = %q, want %q", got, want)
	}
	if len(got) > 128 || len(got) == 0 {
		t.Fatalf("archiveID length %d out of range", len(got))
	}
	for _, r := range got {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '.' || r == '_' || r == '-'
		if !ok {
			t.Fatalf("archiveID %q contains disallowed rune %q", got, r)
		}
	}
}

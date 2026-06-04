package walhelmsrc

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// stateFixedTime returns a fixed UTC time for round-trip comparisons.
func stateFixedTime(sec int64) time.Time {
	return time.Unix(sec, 0).UTC()
}

func TestSaveLoadState_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	subject := "walhelm:9f2a"

	want := State{
		MessagesSince: stateFixedTime(1700000000),
		LabsSince:     stateFixedTime(1700001000),
		RecordsSince:  stateFixedTime(1700002000),
	}

	if err := SaveState(dir, subject, want); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	got, err := LoadState(dir, subject)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	if !got.MessagesSince.Equal(want.MessagesSince) {
		t.Errorf("MessagesSince: got %v, want %v", got.MessagesSince, want.MessagesSince)
	}
	if !got.LabsSince.Equal(want.LabsSince) {
		t.Errorf("LabsSince: got %v, want %v", got.LabsSince, want.LabsSince)
	}
	if !got.RecordsSince.Equal(want.RecordsSince) {
		t.Errorf("RecordsSince: got %v, want %v", got.RecordsSince, want.RecordsSince)
	}
}

func TestLoadState_AbsentFile(t *testing.T) {
	dir := t.TempDir()
	subject := "walhelm:no-such-subject"

	got, err := LoadState(dir, subject)
	if err != nil {
		t.Fatalf("LoadState with absent file: expected nil error, got %v", err)
	}
	var zero State
	if !got.MessagesSince.Equal(zero.MessagesSince) {
		t.Errorf("MessagesSince: got %v, want zero", got.MessagesSince)
	}
	if !got.LabsSince.Equal(zero.LabsSince) {
		t.Errorf("LabsSince: got %v, want zero", got.LabsSince)
	}
	if !got.RecordsSince.Equal(zero.RecordsSince) {
		t.Errorf("RecordsSince: got %v, want zero", got.RecordsSince)
	}
}

func TestLoadState_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	subject := "walhelm:9f2a"

	// Write garbage directly to the expected sanitized path.
	safe := stateFilePath(dir, subject)
	if err := os.WriteFile(safe, []byte("not valid json {{{{"), 0o644); err != nil {
		t.Fatalf("setup: write corrupt file: %v", err)
	}

	got, err := LoadState(dir, subject)
	if err != nil {
		t.Fatalf("LoadState with corrupt file: expected nil error, got %v", err)
	}
	var zero State
	if !got.MessagesSince.Equal(zero.MessagesSince) {
		t.Errorf("MessagesSince: got %v, want zero", got.MessagesSince)
	}
	if !got.LabsSince.Equal(zero.LabsSince) {
		t.Errorf("LabsSince: got %v, want zero", got.LabsSince)
	}
	if !got.RecordsSince.Equal(zero.RecordsSince) {
		t.Errorf("RecordsSince: got %v, want zero", got.RecordsSince)
	}
}

func TestSaveState_SanitizedFilename(t *testing.T) {
	dir := t.TempDir()
	subject := "walhelm:9f2a"

	if err := SaveState(dir, subject, State{}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	// Confirm the file exists at the sanitized path (no colon in filename).
	expectedPath := stateFilePath(dir, subject)
	if _, err := os.Stat(expectedPath); err != nil {
		t.Fatalf("expected state file at %s, got error: %v", expectedPath, err)
	}

	// The filename must not contain a colon.
	base := filepath.Base(expectedPath)
	for _, ch := range base {
		if ch == ':' {
			t.Errorf("sanitized filename %q still contains a colon", base)
		}
	}

	// Confirm no file exists with the raw (unsanitized) colon-containing name.
	rawPath := filepath.Join(dir, "state-"+subject+".json")
	if _, err := os.Stat(rawPath); err == nil {
		t.Errorf("file exists at unsanitized path %s; subject was not sanitized", rawPath)
	}
}

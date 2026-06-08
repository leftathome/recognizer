package walhelmsrc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"time"
)

// State holds the per-subject since-cursor timestamps used to perform
// incremental pulls from a Walhelm account. A zero State (all fields at their
// zero value) causes the caller to perform a full pull from the beginning of
// time.
//
// Absent-file and malformed-JSON conditions are treated identically: both
// return State{}, nil so that the caller falls back to a full pull without
// returning an error. The rationale:
//
//   - Absent: the subject is new; full pull is correct.
//   - Malformed: the file is corrupt or was written by an incompatible version;
//     a full pull is safe and self-healing.
//
// A genuine unexpected IO error (e.g. the state directory itself is
// unreadable) IS returned, because it likely indicates a misconfiguration or
// permission problem that the caller should surface.
type State struct {
	MessagesSince time.Time `json:"messages_since"`
	LabsSince     time.Time `json:"labs_since"`
	RecordsSince  time.Time `json:"records_since"`
}

// stateFilePath returns the absolute path for the state file for the given
// subject inside dir. The subject is sanitized so it forms a safe filename
// component (colons and other special characters are replaced with "_").
func stateFilePath(dir, subject string) string {
	safe := safeName(subject, 0)
	return filepath.Join(dir, "state-"+safe+".json")
}

// LoadState reads the cursor state for subject from dir.
//
// Decision table:
//
//	absent file        -> State{}, nil   (new subject; full pull)
//	malformed JSON     -> State{}, nil   (corrupt/incompatible; full pull)
//	other IO error     -> State{}, err   (surfaced to caller)
//	valid file         -> parsed State, nil
func LoadState(dir, subject string) (State, error) {
	path := stateFilePath(dir, subject)

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Normal for a first-ever pull. Not an error.
			return State{}, nil
		}
		return State{}, fmt.Errorf("LoadState: read %s: %w", path, err)
	}

	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		// Corrupt or incompatible state file. Log for observability, but treat
		// as zero so the caller performs a safe full pull.
		log.Printf("walhelmsrc: LoadState: discarding malformed state file %s: %v", path, err)
		return State{}, nil
	}

	return s, nil
}

// SaveState persists the cursor state for subject into dir atomically.
//
// The file is written to a temporary path in the same directory and then
// renamed to the final path so that a concurrent reader never sees a partial
// write. dir is created (with all parents) if it does not exist.
func SaveState(dir, subject string, s State) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("SaveState: mkdir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("SaveState: marshal: %w", err)
	}

	finalPath := stateFilePath(dir, subject)
	tmpPath := finalPath + ".tmp"

	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("SaveState: write tmp %s: %w", tmpPath, err)
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		// Best-effort cleanup; ignore error from Remove.
		_ = os.Remove(tmpPath)
		return fmt.Errorf("SaveState: rename to %s: %w", finalPath, err)
	}

	return nil
}

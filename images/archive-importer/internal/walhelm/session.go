package walhelmsrc

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	walhelm "github.com/leftathome/walhelm-go"
)

// ErrSessionExpired is returned by LoadSession when the session file parses
// successfully but the session is no longer valid (expired or zero expiry).
// Callers (e.g. main in Task 7) can detect this with errors.Is and exit with
// the appropriate exit code.
var ErrSessionExpired = errors.New("walhelm session expired or invalid")

// LoadSession reads a JSON-encoded walhelm.Session from path and returns it.
//
// Errors:
//   - missing file: wrapped os error (use errors.Is(err, fs.ErrNotExist))
//   - malformed JSON: wrapped json error
//   - expired session: ErrSessionExpired (detectable via errors.Is)
func LoadSession(path string) (*walhelm.Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("LoadSession: read %s: %w", path, err)
	}

	var s walhelm.Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("LoadSession: parse %s: %w", path, err)
	}

	if !s.IsValid() {
		return nil, fmt.Errorf("LoadSession: %w", ErrSessionExpired)
	}

	return &s, nil
}

// NewClientFromSession constructs a WalhelmClient from a previously loaded
// session. It installs the session via walhelm.WithSession so the upstream
// client loads its cookies into the jar at construction time.
func NewClientFromSession(s *walhelm.Session) (WalhelmClient, error) {
	c, err := walhelm.NewClient(walhelm.WithSession(s))
	if err != nil {
		return nil, fmt.Errorf("NewClientFromSession: %w", err)
	}
	return newRealClient(c), nil
}

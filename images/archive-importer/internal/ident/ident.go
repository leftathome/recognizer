// Package ident derives the content-addressed archive identifier
// used to name the unpacked-tree directory: <sha256-prefix>-<filename-stem>.
package ident

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Derive returns "<8-char-sha-prefix>-<filename-stem>" for archivePath.
// The stem strips known archive extensions (.zip, .tar.gz, .7z) and
// anything after the first dot for unknown extensions.
func Derive(archivePath string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("open: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash: %w", err)
	}
	prefix := hex.EncodeToString(h.Sum(nil))[:8]
	base := filepath.Base(archivePath)
	stem := strings.TrimSuffix(base, ".zip")
	stem = strings.TrimSuffix(stem, ".tar.gz")
	stem = strings.TrimSuffix(stem, ".7z")
	if i := strings.Index(stem, "."); i > 0 && stem == base {
		stem = stem[:i]
	}
	return prefix + "-" + stem, nil
}

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
	"time"
)

// HashFile reads the file at path once and returns its full SHA-256
// (lowercase hex), size in bytes, and modification time. Single pass
// over the file so callers don't have to hash twice when they need
// both the archive_id and the manifest source fields.
func HashFile(path string) (sha256hex string, size int64, mtime time.Time, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, time.Time{}, fmt.Errorf("open: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", 0, time.Time{}, fmt.Errorf("stat: %w", err)
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, time.Time{}, fmt.Errorf("hash: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), n, st.ModTime(), nil
}

// DeriveID composes "<8-char-sha-prefix>-<filename-stem>" from a
// filename and a full SHA-256 hex string. Pure function -- no I/O,
// so callers that already have the hash (e.g. via HashFile) don't
// pay for an extra read.
//
// The stem strips known archive extensions (.zip, .tar.gz, .7z,
// .mbox) and falls back to "everything before the first dot" for
// unknown extensions.
func DeriveID(filename, sha256hex string) string {
	prefix := sha256hex[:8]
	stem := strings.TrimSuffix(filename, ".zip")
	stem = strings.TrimSuffix(stem, ".tar.gz")
	stem = strings.TrimSuffix(stem, ".7z")
	stem = strings.TrimSuffix(stem, ".mbox")
	if i := strings.Index(stem, "."); i > 0 && stem == filename {
		stem = stem[:i]
	}
	return prefix + "-" + stem
}

// Derive returns "<8-char-sha-prefix>-<filename-stem>" for archivePath.
// Thin wrapper around HashFile + DeriveID; kept for callers (and
// tests) that don't care about the size/mtime.
func Derive(archivePath string) (string, error) {
	sha, _, _, err := HashFile(archivePath)
	if err != nil {
		return "", err
	}
	return DeriveID(filepath.Base(archivePath), sha), nil
}

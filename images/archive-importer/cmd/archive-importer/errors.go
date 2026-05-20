package main

import (
	"archive/zip"
	"errors"
	"os"
	"strings"
)

// Sentinel-shaped error categorization for the os.Exit code in main().
// Per spec 03 § 4.3.
func exitCodeFor(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, zip.ErrInsecurePath) {
		return 1
	}
	if errors.Is(err, os.ErrNotExist) {
		return 1
	}
	// Match each branch by the exact wrap prefix the producer code uses.
	// Bare-substring checks (e.g. "unpack") false-positive when the path
	// "unpacked/<id>" appears in an unrelated error message -- e.g.
	// "matcher: no provider matched in /data/unpacked/abc-takeout" gets
	// misclassified as exit 1 instead of 2.
	msg := err.Error()
	if strings.HasPrefix(msg, "unpack:") || strings.HasPrefix(msg, "open archive:") {
		return 1
	}
	if strings.HasPrefix(msg, "relay POST exhausted retries") || strings.Contains(msg, ": relay POST exhausted retries") {
		return 3
	}
	if strings.HasPrefix(msg, "manifest write") {
		return 4
	}
	if strings.HasPrefix(msg, "partial state") || strings.HasPrefix(msg, "another import is in progress") {
		return 5
	}
	if strings.HasPrefix(msg, "matcher:") || strings.HasPrefix(msg, "matcher ") {
		return 2
	}
	return 1
}

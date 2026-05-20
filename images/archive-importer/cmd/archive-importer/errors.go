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
	if strings.Contains(err.Error(), "unpack") || strings.Contains(err.Error(), "open archive") {
		return 1
	}
	if strings.Contains(err.Error(), "relay POST exhausted retries") {
		return 3
	}
	if strings.Contains(err.Error(), "manifest write") {
		return 4
	}
	if strings.Contains(err.Error(), "partial state") || strings.Contains(err.Error(), "another import is in progress") {
		return 5
	}
	if strings.Contains(err.Error(), "matcher") {
		return 2
	}
	return 1
}

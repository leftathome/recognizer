package main

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
)

func TestExitCodeFor(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil -> 0", nil, 0},
		{"zip.ErrInsecurePath -> 1", zip.ErrInsecurePath, 1},
		{"os.ErrNotExist -> 1", os.ErrNotExist, 1},
		{"wrapped 'unpack' -> 1", fmt.Errorf("unpack: %w", io.EOF), 1},
		{"wrapped 'open archive' -> 1", errors.New("open archive: no such file"), 1},
		{"wrapped 'matcher' -> 2", errors.New("matcher: no provider matched"), 2},
		{"wrapped 'relay POST exhausted retries' -> 3", errors.New("relay POST exhausted retries after 5 attempts"), 3},
		{"unpack false positive: 'matcher' with path /unpacked/ -> 2", errors.New("matcher: no provider matched in /data/unpacked/abc"), 2},
		{"wrapped 'manifest write' -> 4", fmt.Errorf("manifest write: %w", os.ErrPermission), 4},
		{"wrapped 'partial state' -> 5", errors.New("partial state at /tmp/foo"), 5},
		{"wrapped 'another import is in progress' -> 5", fmt.Errorf("another import is in progress for x: %w", io.EOF), 5},
		{"unrecognized error -> 1 (default)", errors.New("something unexpected"), 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := exitCodeFor(c.err); got != c.want {
				t.Errorf("exitCodeFor(%v) = %d, want %d", c.err, got, c.want)
			}
		})
	}
}

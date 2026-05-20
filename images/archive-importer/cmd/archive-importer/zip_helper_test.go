package main

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// buildFixtureZip zips srcDir into a new file under t.TempDir() and returns
// its path. Entries are added in sorted order with a fixed mtime so the
// resulting zip is byte-deterministic.
func buildFixtureZip(t *testing.T, srcDir string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "takeout-EXAMPLE.zip")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	defer w.Close()

	fixedTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var paths []string
	filepath.WalkDir(srcDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			paths = append(paths, p)
		}
		return nil
	})
	sort.Strings(paths)
	for _, p := range paths {
		rel, _ := filepath.Rel(srcDir, p)
		fh := &zip.FileHeader{Name: rel, Method: zip.Deflate, Modified: fixedTime}
		w.SetComment("")
		writer, err := w.CreateHeader(fh)
		if err != nil {
			t.Fatal(err)
		}
		src, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(writer, src)
		src.Close()
	}
	return out
}

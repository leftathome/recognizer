package unpacker

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUnpackZip_BasicTree(t *testing.T) {
	src := makeZip(t, map[string][]byte{
		"Takeout/Mail/foo.mbox":    []byte("From: a@b\n"),
		"Takeout/Calendar/foo.ics": []byte("BEGIN:VCALENDAR\nEND:VCALENDAR\n"),
	})
	dst := t.TempDir()
	if err := UnpackZip(src, dst); err != nil {
		t.Fatal(err)
	}
	mustExist(t, filepath.Join(dst, "Takeout/Mail/foo.mbox"))
	mustExist(t, filepath.Join(dst, "Takeout/Calendar/foo.ics"))
}

func TestUnpackZip_RejectsPathTraversal(t *testing.T) {
	src := makeZipRaw(t, "../escape.txt", []byte("evil"))
	dst := t.TempDir()
	err := UnpackZip(src, dst)
	if err == nil {
		t.Fatal("expected error for path traversal, got nil")
	}
	if !errors.Is(err, zip.ErrInsecurePath) {
		t.Errorf("got %v, want zip.ErrInsecurePath", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dst), "escape.txt")); !os.IsNotExist(err) {
		t.Errorf("file leaked outside dst")
	}
}

func TestUnpackZip_PreservesDirectoryStructure(t *testing.T) {
	src := makeZip(t, map[string][]byte{
		"a/b/c/leaf.txt": []byte("leaf"),
	})
	dst := t.TempDir()
	if err := UnpackZip(src, dst); err != nil {
		t.Fatal(err)
	}
	mustExist(t, filepath.Join(dst, "a/b/c/leaf.txt"))
}

func makeZip(t *testing.T, files map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	for name, body := range files {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write(body)
	}
	w.Close()
	return path
}

func makeZipRaw(t *testing.T, entry string, body []byte) string {
	t.Helper()
	return makeZip(t, map[string][]byte{entry: body})
}

func mustExist(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Stat(p); err != nil {
		t.Errorf("expected %s to exist: %v", p, err)
	}
}

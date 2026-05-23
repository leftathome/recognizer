package delivery

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestTarSubtree_RegularFilesAndDirs(t *testing.T) {
	src := t.TempDir()
	tmp := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(src, "sub", "deep"), 0755))
	must(t, os.WriteFile(filepath.Join(src, "a.json"), []byte(`{"a":1}`), 0644))
	must(t, os.WriteFile(filepath.Join(src, "sub", "b.json"), []byte("body"), 0644))
	must(t, os.WriteFile(filepath.Join(src, "sub", "deep", "c.bin"), []byte{1, 2, 3}, 0644))

	path, sha, size, err := TarSubtree(src, tmp)
	if err != nil {
		t.Fatalf("TarSubtree: %v", err)
	}
	defer os.Remove(path)

	if size <= 0 {
		t.Errorf("size should be > 0, got %d", size)
	}
	if len(sha) != 64 {
		t.Errorf("sha256 must be 64 hex chars, got %d", len(sha))
	}

	// Verify recorded size + sha256 match the file we wrote.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(b)) != size {
		t.Errorf("file size %d != recorded %d", len(b), size)
	}
	h := sha256.Sum256(b)
	if hex.EncodeToString(h[:]) != sha {
		t.Error("sha256 mismatch between recorded and file content")
	}

	names := listTar(t, path)
	sort.Strings(names)
	want := []string{"a.json", "sub/", "sub/b.json", "sub/deep/", "sub/deep/c.bin"}
	if diff := strings.Join(names, "|"); diff != strings.Join(want, "|") {
		t.Errorf("tar entries:\n got %v\nwant %v", names, want)
	}
}

func TestTarSubtree_RejectsSymlinks(t *testing.T) {
	src := t.TempDir()
	must(t, os.WriteFile(filepath.Join(src, "real"), []byte("ok"), 0644))
	must(t, os.Symlink("real", filepath.Join(src, "link")))

	_, _, _, err := TarSubtree(src, t.TempDir())
	if err == nil {
		t.Fatal("expected symlink to be rejected (would trip glovebox tar_unsafe_entry)")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error %q should mention 'symlink'", err)
	}
}

func TestHashFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "f")
	must(t, os.WriteFile(path, []byte("hello world"), 0644))

	sha, size, err := HashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if size != 11 {
		t.Errorf("size = %d, want 11", size)
	}
	// sha256("hello world") = b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9
	if sha != "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9" {
		t.Errorf("sha mismatch: %s", sha)
	}
}

func listTar(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tr := tar.NewReader(f)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	return names
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

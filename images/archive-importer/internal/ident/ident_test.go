package ident

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDerive_KnownContent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "takeout-EXAMPLE.zip")
	if err := os.WriteFile(p, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := Derive(p)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("hello"))
	wantPrefix := hex.EncodeToString(sum[:])[:8]
	want := wantPrefix + "-takeout-EXAMPLE"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDerive_DeterministicAcrossCalls(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "fixture.zip")
	if err := os.WriteFile(p, []byte("deterministic"), 0644); err != nil {
		t.Fatal(err)
	}
	a, _ := Derive(p)
	b, _ := Derive(p)
	if a != b {
		t.Errorf("non-deterministic: %q vs %q", a, b)
	}
}

func TestDerive_StripsExtension(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"x.zip", "x.tar.gz", "x.7z", "x"} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte("data"), 0644)
		got, err := Derive(p)
		if err != nil {
			t.Errorf("Derive(%q) err=%v", name, err)
			continue
		}
		stem := got[strings.Index(got, "-")+1:]
		if stem != "x" {
			t.Errorf("Derive(%q).stem = %q, want %q", name, stem, "x")
		}
	}
}

func TestDerive_MissingFile(t *testing.T) {
	_, err := Derive("/nonexistent/path.zip")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

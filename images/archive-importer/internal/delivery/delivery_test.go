package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leftathome/recognizer/images/archive-importer/internal/manifest"
)

func TestGloveboxMediaTypeFor(t *testing.T) {
	cases := []struct {
		recognizer string
		want       string
	}{
		{"archive/google-takeout/mail", "archive/mbox"}, // mbox special case
		{"archive/google-takeout/calendar", "archive/google-takeout-subtree"},
		{"archive/google-takeout/photos", "archive/google-takeout-subtree"},
		{"archive/meta-facebook/ads-information", "archive/generic-tarball"},
		{"archive/meta-facebook/activity", "archive/generic-tarball"},
		{"archive/unknown/foo", "archive/generic-tarball"},
	}
	for _, tc := range cases {
		got := gloveboxMediaTypeFor(tc.recognizer)
		if got != tc.want {
			t.Errorf("%s -> %s, want %s", tc.recognizer, got, tc.want)
		}
	}
}

func TestGloveboxArchiveID(t *testing.T) {
	got, err := gloveboxArchiveID("5c2599d6-All mail Including Spam and Trash-002", "archive/google-takeout/mail")
	if err != nil {
		t.Fatal(err)
	}
	if got != "5c2599d6-google-takeout-mail" {
		t.Errorf("got %q", got)
	}

	got, err = gloveboxArchiveID("abcd1234-foo", "archive/meta-facebook/ads-information")
	if err != nil {
		t.Fatal(err)
	}
	if got != "abcd1234-meta-facebook-ads-information" {
		t.Errorf("got %q", got)
	}

	// No '-' in archive_id is malformed; matches what recognizer's
	// ident.DeriveID always produces.
	if _, err := gloveboxArchiveID("plain", "archive/x/y"); err == nil {
		t.Error("expected error on archive_id without '-' separator")
	}
}

func TestGtSubtreeRelativePath(t *testing.T) {
	cases := map[string]string{
		"/data/incoming/archives/unpacked/abc-takeout/Takeout/Mail":             "Mail",
		"/data/incoming/archives/unpacked/abc-takeout/Takeout/Google Photos":    "Google Photos",
		"/data/incoming/archives/unpacked/abc-takeout/Takeout/Drive/My Drive":   "Drive/My Drive",
		"/data/something/with/no/takeout/Mail":                                  "Mail",
	}
	for path, want := range cases {
		if got := gtSubtreeRelativePath(path); got != want {
			t.Errorf("%s -> %s, want %s", path, got, want)
		}
	}
}

func TestDeliver_RawFile_MboxEndToEnd(t *testing.T) {
	fg := &fakeGlovebox{t: t, requireToken: "tok"}
	srv := httptest.NewServer(fg.handler())
	defer srv.Close()

	src := t.TempDir()
	mboxPath := filepath.Join(src, "All mail Including Spam and Trash-002.mbox")
	body := []byte("From foo@bar Wed Jan  1 00:00:00 2025\n\nbody\n\n")
	must(t, os.WriteFile(mboxPath, body, 0644))
	sum := sha256.Sum256(body)

	c := NewClient(srv.URL, "tok", "recognizer-smoke-test", nil)
	c.ChunkSize = 4 * 1024
	o := &Orchestrator{Client: c, TempDir: t.TempDir()}

	st := manifest.SubtreeRecognized{
		MediaType:  "archive/google-takeout/mail",
		OutputPath: mboxPath,
		ByteSize:   int64(len(body)),
	}
	d := o.Deliver(context.Background(), "5c2599d6-foo", "All mail Including Spam and Trash-002.mbox", st)
	if d.Status != "completed" {
		t.Fatalf("status = %s reason = %s", d.Status, d.FailureReason)
	}
	if d.MediaType != "archive/mbox" {
		t.Errorf("MediaType = %s", d.MediaType)
	}
	if d.MatcherID != "google-takeout/mail" {
		t.Errorf("MatcherID = %s", d.MatcherID)
	}
	if d.ArchiveID != "5c2599d6-google-takeout-mail" {
		t.Errorf("ArchiveID = %s", d.ArchiveID)
	}
	if d.SHA256 != hex.EncodeToString(sum[:]) {
		t.Error("SHA256 mismatch")
	}
	if d.SizeBytes != int64(len(body)) {
		t.Errorf("SizeBytes = %d", d.SizeBytes)
	}
	if !strings.Contains(d.UploadURL, "/v1/archives/upload-1") {
		t.Errorf("UploadURL = %s", d.UploadURL)
	}
}

func TestDeliver_Directory_TarsAndStreams(t *testing.T) {
	fg := &fakeGlovebox{t: t, requireToken: "tok"}
	srv := httptest.NewServer(fg.handler())
	defer srv.Close()

	src := t.TempDir()
	subtree := filepath.Join(src, "Takeout", "Mail")
	must(t, os.MkdirAll(subtree, 0755))
	must(t, os.WriteFile(filepath.Join(subtree, "All-mail.mbox"), []byte("body"), 0644))

	c := NewClient(srv.URL, "tok", "recognizer-smoke-test", nil)
	c.ChunkSize = 32 * 1024
	o := &Orchestrator{Client: c, TempDir: t.TempDir()}

	st := manifest.SubtreeRecognized{
		MediaType:  "archive/google-takeout/calendar",
		OutputPath: subtree,
	}
	d := o.Deliver(context.Background(), "abcd1234-archive", "x.zip", st)
	if d.Status != "completed" {
		t.Fatalf("status = %s reason = %s", d.Status, d.FailureReason)
	}
	if d.MediaType != "archive/google-takeout-subtree" {
		t.Errorf("MediaType = %s", d.MediaType)
	}
	if d.SizeBytes <= 0 || len(d.SHA256) != 64 {
		t.Errorf("size=%d sha=%s should be populated", d.SizeBytes, d.SHA256)
	}
}

func TestDeliverAll_IdempotencyOnReRun(t *testing.T) {
	fg := &fakeGlovebox{t: t, requireToken: "tok"}
	srv := httptest.NewServer(fg.handler())
	defer srv.Close()

	src := t.TempDir()
	mboxPath := filepath.Join(src, "f.mbox")
	must(t, os.WriteFile(mboxPath, []byte("body"), 0644))

	c := NewClient(srv.URL, "tok", "recognizer-smoke-test", nil)
	c.ChunkSize = 1024
	o := &Orchestrator{Client: c, TempDir: t.TempDir()}

	subs := []manifest.SubtreeRecognized{{MediaType: "archive/google-takeout/mail", OutputPath: mboxPath}}
	alreadyDone := []manifest.Delivery{{
		MatcherID: "google-takeout/mail", MediaType: "archive/mbox",
		SourcePath: mboxPath, ArchiveID: "x-x", Status: "completed",
	}}

	beforePatches := fg.patchCalls.Load()
	before := fg.createCalls.Load()
	out := o.DeliverAll(context.Background(), "5c2599d6-foo", "f.mbox", subs, alreadyDone)
	if len(out) != 1 {
		t.Fatalf("DeliverAll = %d records, want 1", len(out))
	}
	if out[0].Status != "completed" {
		t.Errorf("expected completed (reused), got %s", out[0].Status)
	}
	if got := fg.createCalls.Load() - before; got != 0 {
		t.Errorf("createCalls bumped by %d on idempotent re-run", got)
	}
	if got := fg.patchCalls.Load() - beforePatches; got != 0 {
		t.Errorf("patchCalls bumped by %d on idempotent re-run", got)
	}
}

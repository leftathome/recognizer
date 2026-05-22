package delivery

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestUploadMetadataHeader_RequiredKeys(t *testing.T) {
	it := Item{
		ArchiveID:       "abc-1",
		ArchiveFilename: "f.mbox",
		MediaType:       "archive/mbox",
		MatcherID:       "google-takeout/mail",
		SHA256:          strings.Repeat("a", 64),
		SizeBytes:       1234,
	}
	hdr := it.UploadMetadataHeader("recognizer-smoke-test")

	got := parseMetadataHeader(t, hdr)
	want := map[string]string{
		"archive_id":       "abc-1",
		"archive_filename": "f.mbox",
		"media_type":       "archive/mbox",
		"matcher_id":       "google-takeout/mail",
		"provider":         "recognizer-smoke-test",
		"sha256":           strings.Repeat("a", 64),
		"size_bytes":       "1234",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("key %q = %q, want %q", k, got[k], v)
		}
	}
	if _, ok := got["subtree_relative_path"]; ok {
		t.Error("subtree_relative_path should be absent for non-google-takeout-subtree items")
	}
	if _, ok := got["delivered_by"]; ok {
		t.Error("delivered_by is a reserved key, must not be sent")
	}
}

func TestUploadMetadataHeader_GoogleTakeoutSubtree(t *testing.T) {
	it := Item{
		ArchiveID: "x", ArchiveFilename: "f", MatcherID: "google-takeout/mail",
		MediaType: "archive/google-takeout-subtree", SHA256: strings.Repeat("b", 64),
		SizeBytes: 1, SubtreeRelativePath: "Mail",
	}
	hdr := it.UploadMetadataHeader("recognizer-smoke-test")
	got := parseMetadataHeader(t, hdr)
	if got["subtree_relative_path"] != "Mail" {
		t.Errorf("subtree_relative_path = %q", got["subtree_relative_path"])
	}
}

func TestValidate_Rejects(t *testing.T) {
	good := Item{
		ArchiveID: "x", ArchiveFilename: "f.mbox", MediaType: "archive/mbox",
		MatcherID: "google-takeout/mail", SHA256: strings.Repeat("a", 64), SizeBytes: 1,
	}
	if err := good.Validate("recognizer-smoke-test"); err != nil {
		t.Fatalf("good item should validate: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*Item) *Item
		want string
	}{
		{"missing archive_id", func(i *Item) *Item { i.ArchiveID = ""; return i }, "ArchiveID"},
		{"unknown media_type", func(i *Item) *Item { i.MediaType = "archive/meta-facebook"; return i }, "MediaType"},
		{"missing matcher_id", func(i *Item) *Item { i.MatcherID = ""; return i }, "MatcherID"},
		{"short sha256", func(i *Item) *Item { i.SHA256 = "abc"; return i }, "SHA256"},
		{"negative size", func(i *Item) *Item { i.SizeBytes = -1; return i }, "SizeBytes"},
		{"missing rel path for google-takeout-subtree",
			func(i *Item) *Item {
				i.MediaType = "archive/google-takeout-subtree"
				return i
			}, "SubtreeRelativePath"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			it := good
			err := tc.mut(&it).Validate("recognizer-smoke-test")
			if err == nil {
				t.Fatalf("expected error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestValidate_RequiresSourceID(t *testing.T) {
	it := Item{
		ArchiveID: "x", ArchiveFilename: "f", MediaType: "archive/mbox",
		MatcherID: "x", SHA256: strings.Repeat("a", 64), SizeBytes: 1,
	}
	if err := it.Validate(""); err == nil || !strings.Contains(err.Error(), "source_id") {
		t.Errorf("expected source_id error, got %v", err)
	}
}

// parseMetadataHeader decodes "key BASE64(value),key BASE64(value)..."
// into a map. Used by tests to assert that the header round-trips.
func parseMetadataHeader(t *testing.T, hdr string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, pair := range strings.Split(hdr, ",") {
		parts := strings.SplitN(pair, " ", 2)
		if len(parts) != 2 {
			t.Fatalf("malformed pair %q", pair)
		}
		v, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			t.Fatalf("decode %q: %v", parts[1], err)
		}
		out[parts[0]] = string(v)
	}
	return out
}

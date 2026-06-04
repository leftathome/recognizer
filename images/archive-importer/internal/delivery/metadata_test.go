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

// TestUploadMetadataHeader_WalhelmExport verifies that an archive/walhelm-export
// Item emits all spec-15 provenance keys in addition to the standard keys.
func TestUploadMetadataHeader_WalhelmExport(t *testing.T) {
	it := Item{
		ArchiveID:       "walhelm-001",
		ArchiveFilename: "export.tar.gz",
		MediaType:       "archive/walhelm-export",
		MatcherID:       "walhelm/health",
		SHA256:          strings.Repeat("c", 64),
		SizeBytes:       9999,
		AcqProvider:     "kp-wa",
		AcqAccountID:    "leftathome",
		AcqAuthMethod:   "browser_session",
		DataSubject:     "walhelm:9f2a",
		Audience:        []string{"subject", "guardians"},
	}
	hdr := it.UploadMetadataHeader("recognizer-smoke-test")
	got := parseMetadataHeader(t, hdr)

	wantProvenance := map[string]string{
		"acq_provider":    "kp-wa",
		"acq_account_id":  "leftathome",
		"acq_auth_method": "browser_session",
		"data_subject":    "walhelm:9f2a",
		"audience":        "subject,guardians",
	}
	for k, v := range wantProvenance {
		if got[k] != v {
			t.Errorf("key %q = %q, want %q", k, got[k], v)
		}
	}
	// Standard keys must still be present.
	if got["archive_id"] != "walhelm-001" {
		t.Errorf("archive_id = %q, want %q", got["archive_id"], "walhelm-001")
	}
	if got["media_type"] != "archive/walhelm-export" {
		t.Errorf("media_type = %q, want %q", got["media_type"], "archive/walhelm-export")
	}
}

// TestUploadMetadataHeader_BackwardCompat_Mbox verifies that an archive/mbox
// Item with spec-15 fields left zero produces a header byte-identical to what
// was produced before spec-15 was introduced (no acq_*/data_subject/audience
// keys present).
func TestUploadMetadataHeader_BackwardCompat_Mbox(t *testing.T) {
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

	absent := []string{"acq_provider", "acq_account_id", "acq_auth_method", "data_subject", "audience"}
	for _, k := range absent {
		if _, ok := got[k]; ok {
			t.Errorf("key %q must be absent for archive/mbox with zero spec-15 fields, but it is present", k)
		}
	}
	// Reconstruct expected header byte-for-byte to catch any reordering.
	wantHdr := "archive_id " + b64("abc-1") + "," +
		"archive_filename " + b64("f.mbox") + "," +
		"media_type " + b64("archive/mbox") + "," +
		"matcher_id " + b64("google-takeout/mail") + "," +
		"provider " + b64("recognizer-smoke-test") + "," +
		"sha256 " + b64(strings.Repeat("a", 64)) + "," +
		"size_bytes " + b64("1234")
	if hdr != wantHdr {
		t.Errorf("mbox header changed (backward-compat broken):\ngot:  %s\nwant: %s", hdr, wantHdr)
	}
}

// TestValidate_WalhelmExport_RequiresProvenanceFields verifies that
// archive/walhelm-export Items are rejected when spec-15 provenance
// fields are missing, and accepted when all are present.
func TestValidate_WalhelmExport_RequiresProvenanceFields(t *testing.T) {
	good := Item{
		ArchiveID:       "w-1",
		ArchiveFilename: "export.tar.gz",
		MediaType:       "archive/walhelm-export",
		MatcherID:       "walhelm/health",
		SHA256:          strings.Repeat("d", 64),
		SizeBytes:       1,
		AcqProvider:     "kp-wa",
		AcqAccountID:    "leftathome",
		AcqAuthMethod:   "browser_session",
		DataSubject:     "walhelm:9f2a",
		Audience:        []string{"subject"},
	}
	if err := good.Validate("recognizer-smoke-test"); err != nil {
		t.Fatalf("complete walhelm-export item should validate: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*Item)
		want string
	}{
		{"missing AcqProvider", func(i *Item) { i.AcqProvider = "" }, "AcqProvider"},
		{"missing AcqAccountID", func(i *Item) { i.AcqAccountID = "" }, "AcqAccountID"},
		{"missing AcqAuthMethod", func(i *Item) { i.AcqAuthMethod = "" }, "AcqAuthMethod"},
		{"missing DataSubject", func(i *Item) { i.DataSubject = "" }, "DataSubject"},
		{"empty Audience", func(i *Item) { i.Audience = nil }, "Audience"},
		{"empty Audience slice", func(i *Item) { i.Audience = []string{} }, "Audience"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			it := good
			tc.mut(&it)
			err := it.Validate("recognizer-smoke-test")
			if err == nil {
				t.Fatalf("expected error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// TestValidate_NonWalhelm_ProvenanceFieldsOptional verifies that mbox and
// other existing media types do not require spec-15 provenance fields.
func TestValidate_NonWalhelm_ProvenanceFieldsOptional(t *testing.T) {
	mediaTypes := []string{
		"archive/mbox",
		"archive/imap-export",
		"archive/generic-tarball",
	}
	for _, mt := range mediaTypes {
		t.Run(mt, func(t *testing.T) {
			it := Item{
				ArchiveID:       "x",
				ArchiveFilename: "f",
				MediaType:       mt,
				MatcherID:       "x",
				SHA256:          strings.Repeat("a", 64),
				SizeBytes:       1,
				// spec-15 fields intentionally left zero.
			}
			if err := it.Validate("recognizer-smoke-test"); err != nil {
				t.Errorf("media type %q with zero spec-15 fields should not error: %v", mt, err)
			}
		})
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

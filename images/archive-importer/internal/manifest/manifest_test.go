package manifest

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	m := Manifest{
		SchemaVersion: "1.0",
		ArchiveID:     "00000000-takeout-EXAMPLE",
		Source: Source{
			OriginalFilename: "takeout-EXAMPLE.zip",
			MovedTo:          "unpacked/00000000-takeout-EXAMPLE/takeout-EXAMPLE.zip",
			SHA256:           "0000000000000000000000000000000000000000000000000000000000000000",
			SizeBytes:        1234,
			Mtime:            time.Now().UTC().Format(time.RFC3339),
			ArchiveFormat:    "zip",
		},
		Provider:       strptr("google-takeout"),
		MatcherVersion: "1.0",
		Timestamps:     Timestamps{Start: time.Now().UTC().Format(time.RFC3339), End: time.Now().UTC().Format(time.RFC3339)},
		SubtreesRecognized: []SubtreeRecognized{
			{MediaType: "archive/google-takeout/mail", OutputPath: "unpacked/00000000-takeout-EXAMPLE/Takeout/Mail", ByteSize: 100, EventID: "evt_EXAMPLE_001"},
		},
		SubtreesUnrecognized: []SubtreeUnrecognized{},
		EventsEmitted: []EventEmitted{
			{EventID: "evt_EXAMPLE_001", EventType: "archive-subtree-recognized", MediaType: "archive/google-takeout/mail", Timestamp: time.Now().UTC().Format(time.RFC3339)},
		},
	}

	dir := t.TempDir()
	p := filepath.Join(dir, ManifestFilename)
	if err := Write(p, &m); err != nil {
		t.Fatal(err)
	}
	got, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.ArchiveID != m.ArchiveID {
		t.Errorf("round-trip lost ArchiveID")
	}
}

func TestWrite_ProducesValidJSON(t *testing.T) {
	m := minimal()
	p := filepath.Join(t.TempDir(), ManifestFilename)
	if err := Write(p, m); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	var anything map[string]any
	if err := json.Unmarshal(b, &anything); err != nil {
		t.Errorf("invalid JSON: %v", err)
	}
}

// TestWrite_ProducesSchemaValidJSON validates the writer output against
// schemas/archive-layout-manifest.v1.schema.json by shelling out to
// `python3 -m jsonschema`. Catches drift if either the writer or the
// schema changes without the other.
//
// Requires python3 + the jsonschema pip package on the host. The
// golang:1.26.3-bookworm image used by `test:go` has python3 in stdlib
// but NOT the jsonschema package, so CI installs it via the before_script
// hook in .gitlab-ci.yml (Task E1c). Locally, install via
// `pip install jsonschema` or skip with `go test -short`.
func TestWrite_ProducesSchemaValidJSON(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping schema-validation test in -short mode")
	}
	if err := exec.Command("python3", "-c", "import jsonschema").Run(); err != nil {
		t.Skip("python3 jsonschema package not installed")
	}
	m := minimal()
	p := filepath.Join(t.TempDir(), ManifestFilename)
	if err := Write(p, m); err != nil {
		t.Fatal(err)
	}
	schema := "../../../../schemas/archive-layout-manifest.v1.schema.json"
	cmd := exec.Command("python3", "-c", `
import json, sys, jsonschema
schema = json.load(open(sys.argv[1]))
sample = json.load(open(sys.argv[2]))
jsonschema.validate(sample, schema)
`, schema, p)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("schema validation failed: %v\n%s", err, out)
	}
}

func TestRead_RejectsInvalidJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), ManifestFilename)
	os.WriteFile(p, []byte("not json"), 0644)
	if _, err := Read(p); err == nil {
		t.Error("expected error")
	}
}

func TestExists_True(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ManifestFilename)
	Write(p, minimal())
	if !Exists(dir) {
		t.Error("expected Exists=true")
	}
}

func TestExists_False(t *testing.T) {
	if Exists(t.TempDir()) {
		t.Error("expected Exists=false in empty dir")
	}
}

func strptr(s string) *string { return &s }

func minimal() *Manifest {
	return &Manifest{
		SchemaVersion:        "1.0",
		ArchiveID:            "00000000-x",
		Source:               Source{OriginalFilename: "x.zip", MovedTo: "x", SHA256: "0000000000000000000000000000000000000000000000000000000000000000", SizeBytes: 1, Mtime: "2026-01-01T00:00:00Z", ArchiveFormat: "zip"},
		Provider:             strptr("google-takeout"),
		MatcherVersion:       "1.0",
		Timestamps:           Timestamps{Start: "2026-01-01T00:00:00Z", End: "2026-01-01T00:00:01Z"},
		SubtreesRecognized:   []SubtreeRecognized{},
		SubtreesUnrecognized: []SubtreeUnrecognized{},
		EventsEmitted:        []EventEmitted{},
	}
}

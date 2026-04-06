package manifest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/session"
)

func makeSession(t *testing.T) *session.Session {
	t.Helper()
	dir := t.TempDir()
	// Create dummy TIFF files
	for _, name := range []string{"page_001.tiff", "page_002.tiff"} {
		data := make([]byte, 1024)
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &session.Session{
		ID:          "20260404-183045-a1b2c3",
		StartTime:   time.Date(2026, 4, 4, 18, 30, 45, 0, time.UTC),
		EndTime:     time.Date(2026, 4, 4, 18, 32, 12, 0, time.UTC),
		InputMethod: session.InputADF,
		Duplex:      true,
		Pages: []session.Page{
			{Filename: "page_001.tiff", Side: "front"},
			{Filename: "page_002.tiff", Side: "back"},
		},
		OutputDir: dir,
	}
}

func TestBuild_ValidManifest(t *testing.T) {
	sess := makeSession(t)
	pages := []PageInfo{
		{Filename: "page_001.tiff", Side: "front", WidthPx: 5100, HeightPx: 6600},
		{Filename: "page_002.tiff", Side: "back", WidthPx: 5100, HeightPx: 6600},
	}

	m, err := Build(sess, "epson-ds-1630", 600, "color", pages)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if m.SchemaVersion != "1.0" {
		t.Errorf("schema_version: got %q, want 1.0", m.SchemaVersion)
	}
	if m.SessionID != "20260404-183045-a1b2c3" {
		t.Errorf("session_id: got %q", m.SessionID)
	}
	if m.InputMethod != "adf" {
		t.Errorf("input_method: got %q, want adf", m.InputMethod)
	}
	if !m.Duplex {
		t.Error("expected duplex true")
	}
	if m.ResolutionDPI != 600 {
		t.Errorf("resolution: got %d, want 600", m.ResolutionDPI)
	}
	if m.PageCount != 2 {
		t.Errorf("page_count: got %d, want 2", m.PageCount)
	}
	if len(m.Pages) != 2 {
		t.Fatalf("pages: got %d, want 2", len(m.Pages))
	}
	if m.Pages[0].Filename != "page_001.tiff" {
		t.Errorf("page 0 filename: got %q", m.Pages[0].Filename)
	}
	if m.Pages[0].SizeBytes != 1024 {
		t.Errorf("page 0 size: got %d, want 1024", m.Pages[0].SizeBytes)
	}
	if m.Pages[0].Dimensions.WidthPx != 5100 {
		t.Errorf("page 0 width: got %d", m.Pages[0].Dimensions.WidthPx)
	}
}

func TestBuild_NilSession(t *testing.T) {
	_, err := Build(nil, "dev", 600, "color", nil)
	if err == nil {
		t.Fatal("expected error for nil session")
	}
}

func TestBuild_MissingFile(t *testing.T) {
	sess := &session.Session{
		ID:          "20260404-183045-a1b2c3",
		StartTime:   time.Now(),
		EndTime:     time.Now(),
		InputMethod: session.InputFlatbed,
		OutputDir:   t.TempDir(),
	}
	pages := []PageInfo{{Filename: "nonexistent.tiff", Side: "single", WidthPx: 100, HeightPx: 100}}
	_, err := Build(sess, "dev", 600, "color", pages)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestWriteJSON_CreatesFile(t *testing.T) {
	sess := makeSession(t)
	pages := []PageInfo{
		{Filename: "page_001.tiff", Side: "front", WidthPx: 5100, HeightPx: 6600},
		{Filename: "page_002.tiff", Side: "back", WidthPx: 5100, HeightPx: 6600},
	}
	m, err := Build(sess, "epson-ds-1630", 600, "color", pages)
	if err != nil {
		t.Fatal(err)
	}

	path, err := WriteJSON(m, sess.OutputDir)
	if err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}

	if filepath.Base(path) != "manifest.json" {
		t.Errorf("expected manifest.json, got %s", filepath.Base(path))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var parsed Manifest
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed.SessionID != "20260404-183045-a1b2c3" {
		t.Errorf("parsed session_id: got %q", parsed.SessionID)
	}
	if parsed.PageCount != 2 {
		t.Errorf("parsed page_count: got %d", parsed.PageCount)
	}
}

func TestWriteJSON_AtomicNoTmpLeft(t *testing.T) {
	sess := makeSession(t)
	pages := []PageInfo{
		{Filename: "page_001.tiff", Side: "front", WidthPx: 5100, HeightPx: 6600},
		{Filename: "page_002.tiff", Side: "back", WidthPx: 5100, HeightPx: 6600},
	}
	m, _ := Build(sess, "epson-ds-1630", 600, "color", pages)
	WriteJSON(m, sess.OutputDir)

	tmpPath := filepath.Join(sess.OutputDir, "manifest.json.tmp")
	if _, err := os.Stat(tmpPath); err == nil {
		t.Error("temp file should not remain after write")
	}
}

func TestWriteJSON_OutputMatchesSchema(t *testing.T) {
	sess := makeSession(t)
	pages := []PageInfo{
		{Filename: "page_001.tiff", Side: "front", WidthPx: 5100, HeightPx: 6600},
		{Filename: "page_002.tiff", Side: "back", WidthPx: 5100, HeightPx: 6600},
	}
	m, _ := Build(sess, "epson-ds-1630", 600, "color", pages)
	path, _ := WriteJSON(m, sess.OutputDir)

	data, _ := os.ReadFile(path)
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)

	// Check all required schema fields exist
	required := []string{
		"schema_version", "session_id", "timestamp_start", "timestamp_end",
		"source_device", "input_method", "duplex", "resolution_dpi",
		"color_mode", "page_count", "pages", "output_path", "user_tags", "notes",
	}
	for _, key := range required {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing required field: %s", key)
		}
	}
}

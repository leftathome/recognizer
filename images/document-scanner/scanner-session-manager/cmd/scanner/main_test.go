package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/config"
	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/notify"
	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/scan"
	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/session"
)

func makeClosedSession(t *testing.T) *session.Session {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"page_001.tiff", "page_002.tiff"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fake tiff"), 0o644); err != nil {
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

func testConfig(relayURL string) config.Config {
	cfg := config.Default()
	cfg.RelayURL = relayURL
	return cfg
}

// TestCloseHandler_WritesSchemaShapedManifest asserts the manifest.json
// written on close carries every field
// schemas/scan-session-manifest.v1.schema.json requires, structurally (no
// external jsonschema dependency needed for a flat field-presence/shape
// check like this).
func TestCloseHandler_WritesSchemaShapedManifest(t *testing.T) {
	sess := makeClosedSession(t)
	var sendCalled bool
	send := func(notify.Event, string) error {
		sendCalled = true
		return nil
	}

	onClose := closeHandler(scan.New(scan.ExecCommander{}), "epsonscan2:DS-1630:usb:04b8:0154", testConfig(""), send)
	onClose(sess)

	if sendCalled {
		t.Error("send should not be called when relay URL is empty")
	}

	data, err := os.ReadFile(filepath.Join(sess.OutputDir, "manifest.json"))
	if err != nil {
		t.Fatalf("manifest.json not written: %v", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("manifest.json is not valid JSON: %v", err)
	}

	required := []string{
		"schema_version", "session_id", "timestamp_start", "timestamp_end",
		"source_device", "input_method", "duplex", "resolution_dpi",
		"color_mode", "page_count", "pages", "output_path",
	}
	for _, key := range required {
		if _, ok := raw[key]; !ok {
			t.Errorf("manifest missing required field %q", key)
		}
	}

	if raw["schema_version"] != "1.0" {
		t.Errorf("schema_version: got %v, want 1.0", raw["schema_version"])
	}
	if raw["session_id"] != sess.ID {
		t.Errorf("session_id: got %v, want %v", raw["session_id"], sess.ID)
	}
	if raw["source_device"] != "epsonscan2:DS-1630:usb:04b8:0154" {
		t.Errorf("source_device: got %v", raw["source_device"])
	}
	if raw["color_mode"] != "color" {
		t.Errorf("color_mode: got %v, want lowercase 'color' per schema enum", raw["color_mode"])
	}
	pages, ok := raw["pages"].([]interface{})
	if !ok || len(pages) != 2 {
		t.Fatalf("pages: got %v", raw["pages"])
	}
	page0, ok := pages[0].(map[string]interface{})
	if !ok {
		t.Fatalf("page 0 not an object: %v", pages[0])
	}
	for _, key := range []string{"filename", "side", "size_bytes", "dimensions"} {
		if _, ok := page0[key]; !ok {
			t.Errorf("page 0 missing field %q", key)
		}
	}
	dims, ok := page0["dimensions"].(map[string]interface{})
	if !ok {
		t.Fatalf("page 0 dimensions not an object: %v", page0["dimensions"])
	}
	if dims["width_px"] != float64(5100) || dims["height_px"] != float64(6600) {
		t.Errorf("dimensions at 600 DPI: got %v, want 5100x6600", dims)
	}
}

// TestCloseHandler_SendsNotificationToRelay proves the wired-up path posts
// a schema-shaped scan-session-complete event to the configured relay,
// using a real httptest server and the production notify.Send (send=nil).
func TestCloseHandler_SendsNotificationToRelay(t *testing.T) {
	sess := makeClosedSession(t)

	var receivedBody []byte
	var receivedMethod, receivedContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedContentType = r.Header.Get("Content-Type")
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	onClose := closeHandler(scan.New(scan.ExecCommander{}), "epsonscan2:DS-1630:usb:04b8:0154", testConfig(server.URL), nil)
	onClose(sess)

	if receivedMethod != http.MethodPost {
		t.Errorf("expected POST, got %q", receivedMethod)
	}
	if receivedContentType != "application/json" {
		t.Errorf("expected application/json, got %q", receivedContentType)
	}

	var event map[string]interface{}
	if err := json.Unmarshal(receivedBody, &event); err != nil {
		t.Fatalf("relay body is not valid JSON: %v", err)
	}
	if event["event_type"] != "scan-session-complete" {
		t.Errorf("event_type: got %v", event["event_type"])
	}
	if event["source"] != "document-scanner" {
		t.Errorf("source: got %v", event["source"])
	}
	metadata, ok := event["metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("metadata not an object: %v", event["metadata"])
	}
	if metadata["session_id"] != sess.ID {
		t.Errorf("metadata.session_id: got %v, want %v", metadata["session_id"], sess.ID)
	}
	if metadata["page_count"] != float64(2) {
		t.Errorf("metadata.page_count: got %v, want 2", metadata["page_count"])
	}
}

// TestCloseHandler_SkipsNotificationWhenRelayURLEmpty asserts the manager
// never crashes and never calls send when the relay URL is unset -- it
// should log clearly and move on (manifest is still written).
func TestCloseHandler_SkipsNotificationWhenRelayURLEmpty(t *testing.T) {
	sess := makeClosedSession(t)
	send := func(notify.Event, string) error {
		t.Fatal("send must not be called when relay URL is empty")
		return nil
	}

	onClose := closeHandler(scan.New(scan.ExecCommander{}), "dev", testConfig(""), send)
	onClose(sess) // must not panic

	if _, err := os.Stat(filepath.Join(sess.OutputDir, "manifest.json")); err != nil {
		t.Errorf("manifest.json should still be written: %v", err)
	}
}

// TestCloseHandler_RelayFailureDoesNotPanic proves a relay error is
// swallowed (logged) rather than propagated -- dead-lettering is the
// relay's job, not this process's.
func TestCloseHandler_RelayFailureDoesNotPanic(t *testing.T) {
	sess := makeClosedSession(t)
	send := func(notify.Event, string) error {
		return &sendError{"relay unreachable"}
	}

	onClose := closeHandler(scan.New(scan.ExecCommander{}), "dev", testConfig("http://127.0.0.1:1/nope"), send)
	onClose(sess) // must not panic
}

// TestCloseHandler_ManifestBuildFailureDoesNotPanic proves a manifest build
// failure (e.g. a page file missing on disk) is logged and does not crash
// the manager -- the session's pages/OutputDir here don't exist on disk.
func TestCloseHandler_ManifestBuildFailureDoesNotPanic(t *testing.T) {
	sess := &session.Session{
		ID:          "20260404-183045-a1b2c3",
		StartTime:   time.Now(),
		EndTime:     time.Now(),
		InputMethod: session.InputFlatbed,
		Pages:       []session.Page{{Filename: "missing.tiff", Side: "single"}},
		OutputDir:   t.TempDir(),
	}
	send := func(notify.Event, string) error {
		t.Fatal("send must not be called when manifest build fails")
		return nil
	}

	onClose := closeHandler(scan.New(scan.ExecCommander{}), "dev", testConfig("http://example.invalid/event"), send)
	onClose(sess) // must not panic
}

type sendError struct{ msg string }

func (e *sendError) Error() string { return e.msg }

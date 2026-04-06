package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/leftathome/archiver/images/document-scanner/scanner-session-manager/session"
)

func makeSession() *session.Session {
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
		OutputDir: "/volume1/incoming/scans/sessions/20260404-183045-a1b2c3",
	}
}

func TestBuildEvent_ADF(t *testing.T) {
	sess := makeSession()
	event := BuildEvent(sess, "epson-ds-1630")

	if event.SchemaVersion != "1.0" {
		t.Errorf("schema_version: got %q", event.SchemaVersion)
	}
	if event.Source != "document-scanner" {
		t.Errorf("source: got %q", event.Source)
	}
	if event.EventType != "scan-session-complete" {
		t.Errorf("event_type: got %q", event.EventType)
	}
	if event.MediaType != "scan/adf-session" {
		t.Errorf("media_type: got %q, want scan/adf-session", event.MediaType)
	}
	if event.Metadata.SourceDevice != "epson-ds-1630" {
		t.Errorf("source_device: got %q", event.Metadata.SourceDevice)
	}
	if event.Metadata.PageCount == nil || *event.Metadata.PageCount != 2 {
		t.Errorf("page_count: got %v, want 2", event.Metadata.PageCount)
	}
	if event.Metadata.SessionID == nil || *event.Metadata.SessionID != "20260404-183045-a1b2c3" {
		t.Errorf("session_id: got %v", event.Metadata.SessionID)
	}
	if event.Metadata.Title != nil {
		t.Errorf("title should be nil for scanner events")
	}
	if event.Metadata.DiscLabel != nil {
		t.Errorf("disc_label should be nil for scanner events")
	}
}

func TestBuildEvent_Flatbed(t *testing.T) {
	sess := makeSession()
	sess.InputMethod = session.InputFlatbed
	event := BuildEvent(sess, "epson-ds-1630")

	if event.MediaType != "scan/flatbed-session" {
		t.Errorf("media_type: got %q, want scan/flatbed-session", event.MediaType)
	}
}

func TestBuildEvent_OutputPath(t *testing.T) {
	sess := makeSession()
	event := BuildEvent(sess, "epson-ds-1630")
	if event.OutputPath != sess.OutputDir {
		t.Errorf("output_path: got %q, want %q", event.OutputPath, sess.OutputDir)
	}
}

func TestSend_Success(t *testing.T) {
	var received Event
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected application/json content type")
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &received)
		w.WriteHeader(202)
	}))
	defer server.Close()

	sess := makeSession()
	event := BuildEvent(sess, "epson-ds-1630")
	err := Send(event, server.URL)
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if received.Source != "document-scanner" {
		t.Errorf("relay received wrong source: %q", received.Source)
	}
}

func TestSend_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer server.Close()

	sess := makeSession()
	event := BuildEvent(sess, "epson-ds-1630")
	err := Send(event, server.URL)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestSend_ConnectionRefused(t *testing.T) {
	sess := makeSession()
	event := BuildEvent(sess, "epson-ds-1630")
	err := Send(event, "http://127.0.0.1:1/nope")
	if err == nil {
		t.Fatal("expected error for connection refused")
	}
}

func TestBuildEvent_ValidJSON(t *testing.T) {
	sess := makeSession()
	event := BuildEvent(sess, "epson-ds-1630")
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	required := []string{
		"schema_version", "source", "event_type", "timestamp",
		"output_path", "media_type", "metadata", "node_name",
	}
	for _, key := range required {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing required field: %s", key)
		}
	}
}

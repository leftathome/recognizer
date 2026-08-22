package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/scan"
	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/session"
)

// fakeCommander is a minimal scan.Commander fake for web package tests
// (the scan package's own mockCommander is unexported and lives in a
// different package).
type fakeCommander struct {
	calls    []string
	failScan bool
	devices  string // stdout for `scanimage -f '%d%n'`
}

func (f *fakeCommander) Run(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	for _, a := range args {
		if a == "-f" {
			return []byte(f.devices), nil, nil
		}
	}
	if f.failScan {
		return nil, []byte("scanimage: no paper in ADF\n"), fmt.Errorf("exit status 7")
	}
	// Simulate scanimage writing the output file so manifest.Build (tested
	// elsewhere) would find it.
	for i, a := range args {
		if a == "--output-file" && i+1 < len(args) {
			_ = os.WriteFile(args[i+1], []byte("fake tiff bytes"), 0o644)
		}
	}
	return nil, nil, nil
}

func defaultSettings() ScanSettings {
	return ScanSettings{
		ResolutionDPI: 600,
		ColorMode:     scan.ModeColor,
		Format:        scan.FormatTIFF,
	}
}

func setup(t *testing.T) (*Handler, *session.Manager) {
	t.Helper()
	mgr := session.NewManager(session.Config{
		BaseDir:     t.TempDir(),
		IdleTimeout: 90 * time.Second,
	})
	scanner := scan.New(&fakeCommander{})
	h := NewHandler(mgr, scanner, "epson-ds-1630", defaultSettings())
	return h, mgr
}

func TestHealthz(t *testing.T) {
	h, _ := setup(t)
	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Errorf("expected ok, got %s", body["status"])
	}
}

func TestStatus_Idle(t *testing.T) {
	h, _ := setup(t)
	req := httptest.NewRequest("GET", "/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp statusResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.State != "idle" {
		t.Errorf("expected idle, got %s", resp.State)
	}
	if resp.Device != "epson-ds-1630" {
		t.Errorf("expected epson-ds-1630, got %s", resp.Device)
	}
	if resp.CurrentSession != nil {
		t.Error("expected nil current session")
	}
}

func TestStatus_WithSession(t *testing.T) {
	h, mgr := setup(t)
	mgr.AddPage(session.InputFlatbed, false, "single")
	mgr.AddPage(session.InputFlatbed, false, "single")

	req := httptest.NewRequest("GET", "/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var resp statusResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.State != "open" {
		t.Errorf("expected open, got %s", resp.State)
	}
	if resp.CurrentSession == nil {
		t.Fatal("expected current session")
	}
	if resp.CurrentSession.PageCount != 2 {
		t.Errorf("expected 2 pages, got %d", resp.CurrentSession.PageCount)
	}
}

func TestStatus_RecentSessions(t *testing.T) {
	h, mgr := setup(t)
	mgr.AddPage(session.InputADF, false, "single")
	mgr.ADFComplete()

	req := httptest.NewRequest("GET", "/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var resp statusResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.RecentSessions) != 1 {
		t.Errorf("expected 1 recent session, got %d", len(resp.RecentSessions))
	}
}

func TestScan_Post(t *testing.T) {
	h, mgr := setup(t)
	req := httptest.NewRequest("POST", "/scan", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body["status"] != "scan complete" {
		t.Errorf("status: got %v", body["status"])
	}
	if body["filename"] != "page_001.tiff" {
		t.Errorf("filename: got %v", body["filename"])
	}
	if body["side"] != "single" {
		t.Errorf("side: got %v", body["side"])
	}
	if body["input_method"] != "flatbed" {
		t.Errorf("input_method: got %v", body["input_method"])
	}

	sess := mgr.Current()
	if sess == nil || len(sess.Pages) != 1 {
		t.Fatalf("expected 1 page in current session, got %+v", sess)
	}
	if _, err := os.Stat(filepath.Join(sess.OutputDir, "page_001.tiff")); err != nil {
		t.Errorf("expected scanned file to exist: %v", err)
	}
}

func TestScan_ADFDuplexRequestsCorrectSource(t *testing.T) {
	h, mgr := setup(t)
	body := bytes.NewBufferString(`{"input_method":"adf","duplex":true,"side":"front"}`)
	req := httptest.NewRequest("POST", "/scan", body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	sess := mgr.Current()
	if sess == nil || sess.InputMethod != session.InputADF || !sess.Duplex {
		t.Fatalf("expected open ADF duplex session, got %+v", sess)
	}
	if sess.Pages[0].Side != "front" {
		t.Errorf("side: got %q, want front", sess.Pages[0].Side)
	}
}

func TestScan_InvalidInputMethod(t *testing.T) {
	h, _ := setup(t)
	body := bytes.NewBufferString(`{"input_method":"carrier-pigeon"}`)
	req := httptest.NewRequest("POST", "/scan", body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestScan_ScannerErrorMapsTo502(t *testing.T) {
	mgr := session.NewManager(session.Config{BaseDir: t.TempDir(), IdleTimeout: 90 * time.Second})
	scanner := scan.New(&fakeCommander{failScan: true})
	h := NewHandler(mgr, scanner, "epson-ds-1630", defaultSettings())

	req := httptest.NewRequest("POST", "/scan", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 502 {
		t.Fatalf("expected 502, got %d: %s", w.Code, w.Body.String())
	}
	// The failed page must not linger in the session (it would otherwise
	// break manifest building on close, since no file was written for it).
	if sess := mgr.Current(); sess != nil && len(sess.Pages) != 0 {
		t.Errorf("expected failed page to be removed, got %d pages", len(sess.Pages))
	}
}

func TestScan_DeviceUnavailableMapsTo503(t *testing.T) {
	mgr := session.NewManager(session.Config{BaseDir: t.TempDir(), IdleTimeout: 90 * time.Second})
	// No device override and scanimage -f reports nothing -> auto-detect fails.
	scanner := scan.New(&fakeCommander{devices: ""})
	h := NewHandler(mgr, scanner, "", defaultSettings())

	req := httptest.NewRequest("POST", "/scan", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 503 {
		t.Fatalf("expected 503, got %d: %s", w.Code, w.Body.String())
	}
}

func TestScan_AutoDetectsDeviceWhenNoOverride(t *testing.T) {
	mgr := session.NewManager(session.Config{BaseDir: t.TempDir(), IdleTimeout: 90 * time.Second})
	scanner := scan.New(&fakeCommander{devices: "epsonscan2:DS-1630:usb:04b8:0154\n"})
	h := NewHandler(mgr, scanner, "", defaultSettings())

	req := httptest.NewRequest("POST", "/scan", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// /status should now report the auto-detected device.
	statusReq := httptest.NewRequest("GET", "/status", nil)
	statusW := httptest.NewRecorder()
	h.ServeHTTP(statusW, statusReq)
	var resp statusResponse
	json.Unmarshal(statusW.Body.Bytes(), &resp)
	if resp.Device != "epsonscan2:DS-1630:usb:04b8:0154" {
		t.Errorf("expected auto-detected device in status, got %q", resp.Device)
	}
}

func TestScan_GetNotAllowed(t *testing.T) {
	h, _ := setup(t)
	req := httptest.NewRequest("GET", "/scan", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 405 {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestCloseSession_NoSession(t *testing.T) {
	h, _ := setup(t)
	req := httptest.NewRequest("POST", "/session/close", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "no active session" {
		t.Errorf("expected 'no active session', got %v", body["status"])
	}
}

func TestCloseSession_WithSession(t *testing.T) {
	h, mgr := setup(t)
	mgr.AddPage(session.InputFlatbed, false, "single")

	req := httptest.NewRequest("POST", "/session/close", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "session closed" {
		t.Errorf("expected 'session closed', got %v", body["status"])
	}
	if mgr.State() != session.StateIdle {
		t.Error("session should be idle after close")
	}
}

func TestNewDocument(t *testing.T) {
	h, mgr := setup(t)
	mgr.AddPage(session.InputFlatbed, false, "single")

	req := httptest.NewRequest("POST", "/session/new-document", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "document boundary created" {
		t.Errorf("expected 'document boundary created', got %v", body["status"])
	}
}

func TestStatus_MethodNotAllowed(t *testing.T) {
	h, _ := setup(t)
	req := httptest.NewRequest("POST", "/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 405 {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

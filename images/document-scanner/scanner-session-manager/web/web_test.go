package web

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/leftathome/archiver/images/document-scanner/scanner-session-manager/session"
)

func setup(t *testing.T) (*Handler, *session.Manager) {
	t.Helper()
	mgr := session.NewManager(session.Config{
		BaseDir:     t.TempDir(),
		IdleTimeout: 90 * time.Second,
	})
	h := NewHandler(mgr, nil, "epson-ds-1630")
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
	h, _ := setup(t)
	req := httptest.NewRequest("POST", "/scan", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 202 {
		t.Errorf("expected 202, got %d", w.Code)
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

package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/scan"
)

// webMock implements scan.Commander with scripted results.
type webMock struct {
	results []mockResult
	calls   [][]string
}
type mockResult struct {
	Stdout []byte
	Stderr []byte
	Err    error
}

func (m *webMock) Run(_ context.Context, _ string, args ...string) ([]byte, []byte, error) {
	m.calls = append(m.calls, args)
	if len(m.results) == 0 {
		return nil, nil, nil
	}
	r := m.results[0]
	m.results = m.results[1:]
	return r.Stdout, r.Stderr, r.Err
}

func detectOK() mockResult {
	return mockResult{Stdout: []byte("device `epsonds:libusb:002:002' is a Epson DS-1630 ESC/I-2\n")}
}

func newTestHandler(t *testing.T, m *webMock) *Handler {
	t.Helper()
	return NewHandler(scan.New(m), t.TempDir())
}

func post(t *testing.T, h *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/scan", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestScan_InvalidResolution(t *testing.T) {
	h := newTestHandler(t, &webMock{})
	rec := post(t, h, `{"source":"Flatbed","mode":"Color","resolution":250}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("250 dpi (in-range, not in set) must be 400, got %d", rec.Code)
	}
}

func TestScan_InvalidSource(t *testing.T) {
	h := newTestHandler(t, &webMock{})
	rec := post(t, h, `{"source":"Telepathy","mode":"Color","resolution":300}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", rec.Code)
	}
}

func TestScan_NoDevice(t *testing.T) {
	m := &webMock{results: []mockResult{{Stdout: []byte("No scanners were identified.\n")}}}
	h := newTestHandler(t, m)
	rec := post(t, h, `{"source":"Flatbed","mode":"Color","resolution":300}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("want 503, got %d", rec.Code)
	}
}

func TestScan_FlatbedSingle(t *testing.T) {
	m := &webMock{results: []mockResult{detectOK(), {}}} // detect, then scan ok
	h := newTestHandler(t, m)
	rec := post(t, h, `{"source":"Flatbed","mode":"Color","resolution":300}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var resp scanResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Pages) != 1 || resp.Pages[0].Side != "single" {
		t.Errorf("want 1 single page, got %+v", resp.Pages)
	}
	if resp.Device != "epsonds:libusb:002:002" {
		t.Errorf("want resolved device, got %q", resp.Device)
	}
}

func TestScan_ADFDuplexLabels(t *testing.T) {
	m := &webMock{results: []mockResult{detectOK(), {
		Stderr: []byte("Scanned page 1.\nScanned page 2.\nDocument feeder out of documents\n"),
		Err:    fmt.Errorf("exit status 7"),
	}}}
	h := newTestHandler(t, m)
	rec := post(t, h, `{"source":"ADF Duplex","mode":"Color","resolution":300}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var resp scanResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Pages) != 2 || resp.Pages[0].Side != "front" || resp.Pages[1].Side != "back" {
		t.Errorf("want front,back, got %+v", resp.Pages)
	}
}

func TestScan_EmptyFeeder(t *testing.T) {
	m := &webMock{results: []mockResult{detectOK(), {
		Stderr: []byte("Document feeder out of documents\n"),
		Err:    fmt.Errorf("exit status 7"),
	}}}
	h := newTestHandler(t, m)
	rec := post(t, h, `{"source":"ADF Front","mode":"Gray","resolution":300}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty feeder want 422, got %d", rec.Code)
	}
}

func TestStatus_DevicePresent(t *testing.T) {
	m := &webMock{results: []mockResult{detectOK()}}
	h := newTestHandler(t, m)
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var resp statusResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !resp.DevicePresent || resp.Device != "epsonds:libusb:002:002" {
		t.Errorf("want present+device, got %+v", resp)
	}
}

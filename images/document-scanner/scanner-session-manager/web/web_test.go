package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/scan"
)

// webMock implements scan.Commander with scripted results.
type webMock struct {
	mu      sync.Mutex
	results []mockResult
	calls   [][]string
	before  func() // optional hook run inside Run, before returning
}
type mockResult struct {
	Stdout []byte
	Stderr []byte
	Err    error
}

func (m *webMock) Run(_ context.Context, _ string, args ...string) ([]byte, []byte, error) {
	m.mu.Lock()
	m.calls = append(m.calls, args)
	var r mockResult
	if len(m.results) > 0 {
		r = m.results[0]
		m.results = m.results[1:]
	}
	before := m.before
	m.mu.Unlock()
	if before != nil {
		before()
	}
	return r.Stdout, r.Stderr, r.Err
}

func (m *webMock) callArgs() [][]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
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
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
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
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
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
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.DevicePresent || resp.Device != "epsonds:libusb:002:002" {
		t.Errorf("want present+device, got %+v", resp)
	}
}

// -- additions beyond the plan's test list --

// The operator escape hatch C2 introduced (SCANNER_DEVICE) survives the
// rewrite: an explicit device skips per-request `scanimage -L` entirely.
func TestScan_DeviceOverrideSkipsDetection(t *testing.T) {
	m := &webMock{results: []mockResult{{}}} // only the scan call is scripted
	h := newTestHandler(t, m)
	h.DeviceOverride = "epsonds:libusb:001:007"
	rec := post(t, h, `{"source":"Flatbed","mode":"Color","resolution":600}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var resp scanResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Device != "epsonds:libusb:001:007" {
		t.Errorf("want the override device, got %q", resp.Device)
	}
	calls := m.callArgs()
	if len(calls) != 1 {
		t.Fatalf("want exactly one scanimage call (the scan), got %d: %v", len(calls), calls)
	}
	if strings.Join(calls[0], " ") == "-L" {
		t.Errorf("override must skip detection, but -L was called")
	}
}

// One scanner, one scan at a time: a second request while the device is busy
// gets 409 rather than racing scanimage for the USB handle.
func TestScan_ConcurrentRequestIsBusy(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	m := &webMock{results: []mockResult{detectOK(), {}}}
	m.before = func() {
		// Only the scan call blocks; detection returns immediately.
		if len(m.callArgs()) == 2 {
			close(entered)
			<-release
		}
	}
	h := newTestHandler(t, m)

	done := make(chan int, 1)
	go func() {
		done <- post(t, h, `{"source":"Flatbed","mode":"Color","resolution":300}`).Code
	}()
	<-entered

	busy := post(t, h, `{"source":"Flatbed","mode":"Color","resolution":300}`)
	if busy.Code != http.StatusConflict {
		t.Errorf("second concurrent scan want 409, got %d (%s)", busy.Code, busy.Body.String())
	}
	close(release)
	if code := <-done; code != http.StatusOK {
		t.Errorf("first scan want 200, got %d", code)
	}
}

func TestHealthz(t *testing.T) {
	h := newTestHandler(t, &webMock{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("want 200, got %d", rec.Code)
	}
}

// The retired session API must not answer any more -- a stale caller should
// get a clean 404, not a half-working endpoint.
func TestRetiredSessionRoutesAreGone(t *testing.T) {
	h := newTestHandler(t, &webMock{})
	for _, path := range []string{"/session/close", "/session/new-document", "/settings"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: want 404 after the driver/processor split, got %d", path, rec.Code)
		}
	}
}

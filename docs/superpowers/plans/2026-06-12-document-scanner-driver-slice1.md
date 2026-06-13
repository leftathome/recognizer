# Document Scanner Driver — Slice 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the deployed `document-scanner` pod actually scan the Epson DS-1630 on node `johnny` — a thin, stateless privileged driver exposing a synchronous `POST /scan` that writes raw TIFFs to the data PVC.

**Architecture:** Strip the driver down to a stateless SANE wrapper (delete the `session`, `notify`, `manifest` packages — they belong to the future unprivileged processor). Resolve the real `epsonds:libusb:…` device per request via `scanimage -L`. Add ADF batch scanning. Wire the Helm chart to mirror the proven optical-ripper passthrough (privileged + hostPath `/dev`, `recognizer-hardware` namespace), gated on `hardware.enabled`.

**Tech Stack:** Go 1.26 (`go test`, `go vet`, `staticcheck`), SANE `scanimage` (open `epsonds` backend), Helm, Talos/Kubernetes.

**Spec:** `docs/superpowers/specs/2026-06-12-document-scanner-driver-design.md`

**Module path:** `github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager`

---

## File Structure

Working dir for Go tasks: `images/document-scanner/scanner-session-manager/`

| File | Action | Responsibility |
|---|---|---|
| `scan/scan.go` | Modify | Add `BuildBatchArgs`, `ScanBatch`, `ErrFeederEmpty`, `countScannedPages`. Keep `DetectDevice`/`ScanPage`/`BuildArgs`. |
| `scan/scan_test.go` | Modify | Tests for batch arg-building + end-of-feed/empty detection. |
| `web/web.go` | Rewrite | Stateless handler: `/status`, `/scan` (sync, validated), `/healthz`. Drop `/session/*`, `/settings`. New `NewHandler(scanner, outputBase)` signature. |
| `web/web_test.go` | Rewrite | Tests for validation (400), routing, duplex labels, error mapping (422/500/503), status. |
| `cmd/scanner/main.go` | Modify | Drop session wiring; new handler wiring; output base `/out/scans`. |
| `session/` | Delete | Moves to processor (Slice 2). Imported by `web`, `cmd`, `notify`, `manifest`. |
| `notify/` | Delete | Processor concern. Imported by nothing; *imports* `session`, so must go when `session` does. |
| `manifest/` | Delete | Processor concern. Imported by nothing; *imports* `session`, so must go when `session` does. |
| `images/document-scanner/Dockerfile` | Modify | Remove `imagemagick` (processor dep). |
| `charts/recognizer/templates/document-scanner/daemonset.yaml` | Modify | hardwareNamespace + privileged + hostPath `/dev`, gated on `hardware.enabled`. |
| `charts/recognizer/templates/document-scanner/configmap.yaml` | Modify | Namespace → hardwareNamespace. |
| `charts/recognizer/templates/document-scanner/service.yaml` | Modify | Namespace → hardwareNamespace. |
| `charts/recognizer/values.yaml` | Modify | Remove dead `smarter-devices/bus-usb` limit. |

---

## Task 1: `scan.ScanBatch` — ADF batch scanning with empty/end detection

**Files:**
- Modify: `images/document-scanner/scanner-session-manager/scan/scan.go`
- Test: `images/document-scanner/scanner-session-manager/scan/scan_test.go`

The existing `BuildArgs` emits `--output-file` (single page). ADF needs `--batch=<pattern>`. End-of-feed and empty-feeder are both reported by `scanimage` as a non-nil error with stderr containing `"out of documents"` — disambiguated by how many pages were already scanned (parsed from `"Scanned page N"` stderr lines). This is fully testable through the existing `mockCommander` (no exit code needed).

- [ ] **Step 1: Write failing tests**

Append to `scan/scan_test.go`:

```go
func TestBuildBatchArgs(t *testing.T) {
	p := Params{
		Resolution: 300,
		Mode:       ModeColor,
		Source:     SourceADFDuplex,
		Format:     FormatTIFF,
		Device:     "epsonds:libusb:002:002",
	}
	args := BuildBatchArgs(p, "/out/scans/x/page_%02d.tiff")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--batch=/out/scans/x/page_%02d.tiff") {
		t.Errorf("expected --batch= pattern, got %q", joined)
	}
	if strings.Contains(joined, "--output-file") {
		t.Errorf("batch args must not use --output-file: %q", joined)
	}
	if !strings.Contains(joined, "--source ADF Duplex") {
		t.Errorf("expected source, got %q", joined)
	}
}

func TestScanBatch_TwoPages(t *testing.T) {
	mock := &mockCommander{results: []mockResult{{
		Stderr: []byte("Scanning page 1\nScanned page 1. (scanner status = 5)\n" +
			"Scanning page 2\nScanned page 2. (scanner status = 5)\n" +
			"Scanning page 3\nscanimage: sane_start: Document feeder out of documents\n"),
		Err: fmt.Errorf("exit status 7"),
	}}}
	s := New(mock)
	n, err := s.ScanBatch(context.Background(),
		Params{Device: "d", Source: SourceADFDuplex, Mode: ModeColor, Format: FormatTIFF, Resolution: 300},
		"/tmp/page_%02d.tiff")
	if err != nil {
		t.Fatalf("end-of-feed after pages must be nil error, got %v", err)
	}
	if n != 2 {
		t.Errorf("got %d pages, want 2", n)
	}
}

func TestScanBatch_EmptyFeeder(t *testing.T) {
	mock := &mockCommander{results: []mockResult{{
		Stderr: []byte("scanimage: sane_start: Document feeder out of documents\n"),
		Err:    fmt.Errorf("exit status 7"),
	}}}
	s := New(mock)
	n, err := s.ScanBatch(context.Background(),
		Params{Device: "d", Source: SourceADFFront, Mode: ModeGray, Format: FormatTIFF, Resolution: 75},
		"/tmp/page_%02d.tiff")
	if n != 0 {
		t.Errorf("got %d pages, want 0", n)
	}
	if !errors.Is(err, ErrFeederEmpty) {
		t.Errorf("want ErrFeederEmpty, got %v", err)
	}
}

func TestScanBatch_RealError(t *testing.T) {
	mock := &mockCommander{results: []mockResult{{
		Stderr: []byte("scanimage: open of device epsonds:libusb:002:002 failed: Invalid argument\n"),
		Err:    fmt.Errorf("exit status 1"),
	}}}
	s := New(mock)
	_, err := s.ScanBatch(context.Background(),
		Params{Device: "d", Source: SourceADFFront, Mode: ModeGray, Format: FormatTIFF, Resolution: 75},
		"/tmp/page_%02d.tiff")
	if err == nil || errors.Is(err, ErrFeederEmpty) {
		t.Errorf("want a real (non-empty) error, got %v", err)
	}
}
```

Add `"errors"` to the test imports (alongside `context`, `fmt`, `strings`, `testing`).

- [ ] **Step 2: Run tests, verify they fail**

```bash
cd images/document-scanner/scanner-session-manager
go test ./scan/ -run 'ScanBatch|BuildBatchArgs' -v
```
Expected: FAIL (undefined: `BuildBatchArgs`, `ScanBatch`, `ErrFeederEmpty`).

- [ ] **Step 3: Implement in `scan/scan.go`**

Add `"bufio"`, `"bytes"`, `"errors"` to imports if not present (`bufio`/`bytes` already are; add `errors`). Append:

```go
// ErrFeederEmpty indicates an ADF scan found no documents in the feeder.
var ErrFeederEmpty = errors.New("document feeder empty")

// BuildBatchArgs returns scanimage args for an ADF batch scan. Unlike BuildArgs,
// it uses --batch=<pattern> (a printf-style path with a %d page counter) instead
// of --output-file.
func BuildBatchArgs(p Params, batchPattern string) []string {
	return []string{
		"--device-name", p.Device,
		"--resolution", fmt.Sprintf("%d", p.Resolution),
		"--mode", string(p.Mode),
		"--source", string(p.Source),
		"--format", string(p.Format),
		fmt.Sprintf("--batch=%s", batchPattern),
	}
}

// countScannedPages counts "Scanned page N" progress lines in scanimage stderr.
func countScannedPages(stderr []byte) int {
	n := 0
	sc := bufio.NewScanner(bytes.NewReader(stderr))
	for sc.Scan() {
		if strings.HasPrefix(strings.TrimSpace(sc.Text()), "Scanned page") {
			n++
		}
	}
	return n
}

// ScanBatch runs an ADF batch scan, writing pages to batchPattern. It returns the
// number of pages scanned. scanimage signals both end-of-feed and empty-feeder with
// a non-nil error whose stderr contains "out of documents"; this is end-of-feed when
// >=1 page was produced, and ErrFeederEmpty when 0 were. Any other error is real.
//
// The count is derived from "Scanned page N" stderr lines (one per file scanimage
// writes); this assumes default --batch numbering from 1, so the handler can
// reconstruct page_01..page_NN filenames without touching the filesystem (keeps it
// unit-testable through the Commander mock).
func (s *Scanner) ScanBatch(ctx context.Context, p Params, batchPattern string) (int, error) {
	if p.Device == "" {
		return 0, fmt.Errorf("device is required")
	}
	if batchPattern == "" {
		return 0, fmt.Errorf("batch pattern is required")
	}
	_, stderr, err := s.cmd.Run(ctx, "scanimage", BuildBatchArgs(p, batchPattern)...)
	count := countScannedPages(stderr)
	if err != nil {
		if bytes.Contains(stderr, []byte("out of documents")) {
			if count == 0 {
				return 0, ErrFeederEmpty
			}
			return count, nil // normal end-of-feed
		}
		return count, fmt.Errorf("scanimage --batch failed: %w: %s", err, string(stderr))
	}
	return count, nil
}
```

- [ ] **Step 4: Run tests, verify pass**

```bash
go test ./scan/ -v
```
Expected: PASS (all scan tests).

- [ ] **Step 5: Commit**

```bash
git add scan/scan.go scan/scan_test.go
git commit -m "feat(scanner): add ScanBatch for ADF with empty/end-of-feed detection"
```

---

## Task 2: Rewrite `web` as a stateless handler

**Files:**
- Rewrite: `images/document-scanner/scanner-session-manager/web/web.go`
- Rewrite: `images/document-scanner/scanner-session-manager/web/web_test.go`

Drop the session manager and the `/session/*` + `/settings` routes. `NewHandler` takes `(scanner *scan.Scanner, outputBase string)`. `/scan` validates against the real enums (discrete resolution set), resolves the device per request, creates a per-scan dir, and scans (flatbed → single, ADF → batch). `/status` reports `device_present` + device.

- [ ] **Step 1: Write the new test file**

Replace `web/web_test.go` entirely:

```go
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
```

- [ ] **Step 2: Run tests, verify they fail to compile**

```bash
cd images/document-scanner/scanner-session-manager
go test ./web/ -v
```
Expected: FAIL (build) — old `web.go` still has the session-based `NewHandler` signature and `scanResponse`/`statusResponse` differ.

- [ ] **Step 3: Rewrite `web/web.go`**

Replace the file entirely:

```go
// Package web provides the scanner driver's stateless HTTP interface.
package web

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/scan"
)

// Handler is the stateless scanner driver HTTP handler.
type Handler struct {
	scanner    *scan.Scanner
	outputBase string
	mux        *http.ServeMux
}

// NewHandler creates the driver handler. outputBase is the root under which each
// scan request gets a unique subdirectory.
func NewHandler(scanner *scan.Scanner, outputBase string) *Handler {
	h := &Handler{scanner: scanner, outputBase: outputBase, mux: http.NewServeMux()}
	h.mux.HandleFunc("/status", h.handleStatus)
	h.mux.HandleFunc("/scan", h.handleScan)
	h.mux.HandleFunc("/healthz", h.handleHealthz)
	return h
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.mux.ServeHTTP(w, r) }

var validSources = map[string]bool{"Flatbed": true, "ADF Front": true, "ADF Duplex": true}
var validModes = map[string]bool{"Lineart": true, "Gray": true, "Color": true}
var validResolutions = map[int]bool{
	50: true, 75: true, 100: true, 150: true, 200: true, 240: true,
	300: true, 360: true, 400: true, 600: true, 1200: true,
}

type scanRequest struct {
	Source     string `json:"source"`
	Mode       string `json:"mode"`
	Resolution int    `json:"resolution"`
}

type pageInfo struct {
	Filename string `json:"filename"`
	Side     string `json:"side"`
}

type scanResponse struct {
	Device    string     `json:"device"`
	OutputDir string     `json:"output_dir"`
	Pages     []pageInfo `json:"pages"`
}

type statusResponse struct {
	DevicePresent bool   `json:"device_present"`
	Device        string `json:"device"`
}

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	device, err := h.scanner.DetectDevice(r.Context())
	writeJSON(w, http.StatusOK, statusResponse{DevicePresent: err == nil, Device: device})
}

func (h *Handler) handleScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req scanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if !validSources[req.Source] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid source"})
		return
	}
	if !validModes[req.Mode] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid mode"})
		return
	}
	if !validResolutions[req.Resolution] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid resolution"})
		return
	}

	ctx := r.Context()
	device, err := h.scanner.DetectDevice(ctx)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "scanner not found"})
		return
	}

	dir, err := h.makeOutputDir()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create output dir"})
		return
	}

	params := scan.Params{
		Resolution: req.Resolution,
		Mode:       scan.ScanMode(req.Mode),
		Source:     scan.ScanSource(req.Source),
		Format:     scan.FormatTIFF,
		Device:     device,
	}

	if req.Source == "Flatbed" {
		params.OutputPath = filepath.Join(dir, "page_01.tiff")
		if err := h.scanner.ScanPage(ctx, params); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, scanResponse{
			Device: device, OutputDir: dir,
			Pages: []pageInfo{{Filename: "page_01.tiff", Side: "single"}},
		})
		return
	}

	// ADF Front / ADF Duplex
	pattern := filepath.Join(dir, "page_%02d.tiff")
	count, err := h.scanner.ScanBatch(ctx, params, pattern)
	if err != nil {
		if errors.Is(err, scan.ErrFeederEmpty) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "feeder empty"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	duplex := req.Source == "ADF Duplex"
	pages := make([]pageInfo, 0, count)
	for i := 1; i <= count; i++ {
		side := "single"
		if duplex {
			if i%2 == 1 {
				side = "front"
			} else {
				side = "back"
			}
		}
		pages = append(pages, pageInfo{Filename: fmt.Sprintf("page_%02d.tiff", i), Side: side})
	}
	writeJSON(w, http.StatusOK, scanResponse{Device: device, OutputDir: dir, Pages: pages})
}

func (h *Handler) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) makeOutputDir() (string, error) {
	dir := filepath.Join(h.outputBase, generateID())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func generateID() string {
	b := make([]byte, 3)
	rand.Read(b)
	return fmt.Sprintf("%s-%x", time.Now().UTC().Format("20060102-150405"), b)
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	data, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(w, `{"error":"marshal failed: %s"}`, err)
		return
	}
	w.Write(data)
}
```

- [ ] **Step 4: Run tests, verify pass**

```bash
go test ./web/ -v
```
Expected: PASS. Note `go build ./...` will still fail here because `cmd/scanner/main.go` calls the old 3-arg `NewHandler` and imports `session`; `go test ./web/` compiles only `web`+`scan`, so it passes. **Do not commit yet** — Task 3 rewires `main.go` and deletes the orphaned packages so the *whole module* builds, and the two changes commit together (one bisect-clean commit).

- [ ] **Step 5: Stage (do not commit yet)**

```bash
git add web/web.go web/web_test.go
```
Continue straight to Task 3; the commit happens there.

---

## Task 3: Rewire `main.go` and delete processor-only packages

**Files:**
- Modify: `images/document-scanner/scanner-session-manager/cmd/scanner/main.go`
- Delete: `session/`, `notify/`, `manifest/`

- [ ] **Step 1: Rewrite `cmd/scanner/main.go`**

```go
// Command scanner is the document scanner driver entrypoint.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/scan"
	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/web"
)

func main() {
	baseDir := os.Getenv("SCANNER_OUTPUT_DIR")
	if baseDir == "" {
		baseDir = "/out/scans"
	}
	listenAddr := os.Getenv("SCANNER_LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = ":8080"
	}

	scanner := scan.New(scan.ExecCommander{})
	handler := web.NewHandler(scanner, baseDir)

	fmt.Printf("Scanner driver listening on %s\n", listenAddr)
	log.Fatal(http.ListenAndServe(listenAddr, handler))
}
```

- [ ] **Step 2: Delete the processor-only packages**

```bash
cd images/document-scanner/scanner-session-manager
git rm -r session notify manifest
```

- [ ] **Step 3: Verify the whole module builds, vets, and tests clean**

```bash
go build ./...
go vet ./...
go test ./...
```
Expected: all PASS, no references to `session`/`notify`/`manifest` remain.

- [ ] **Step 4: Run staticcheck**

```bash
staticcheck ./... || go run honnef.co/go/tools/cmd/staticcheck@latest ./...
```
Expected: no findings. (If `rand.Read`'s ignored return is flagged, assign `_, _ = rand.Read(b)`.)

- [ ] **Step 5: Commit (web rewrite + rewire + deletions together — bisect-clean)**

```bash
git add cmd/scanner/main.go web/web.go web/web_test.go scan/scan.go scan/scan_test.go
git rm -r --cached session notify manifest 2>/dev/null || true
git commit -m "refactor(scanner): stateless driver (sync /scan, /status); strip session/notify/manifest"
```
(`scan/*` was already committed in Task 1; re-adding is a no-op. The point is that this commit leaves `go build ./...` green.)

---

## Task 4: Slim the Dockerfile

**Files:**
- Modify: `images/document-scanner/Dockerfile`

- [ ] **Step 1: Remove `imagemagick`** from the `apt-get install` list (leave `sane-utils`, `libsane-dev`, `curl`, `ca-certificates`). Result:

```dockerfile
RUN apt-get update && apt-get install -y --no-install-recommends \
    sane-utils \
    libsane-dev \
    curl \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*
```

(Leave the commented `epsonscan2` block as-is — documented as unnecessary.)

- [ ] **Step 2: Verify the image builds and scans are driven by the open backend**

```bash
cd /mnt/c/Users/steve/Code/recognizer
docker build -f images/document-scanner/Dockerfile -t document-scanner:slice1-test .
docker run --rm document-scanner:slice1-test sh -c 'command -v scanimage && ! command -v convert && echo OK'
```
Expected: prints the `scanimage` path, no `convert`, then `OK`.

> If no local Docker daemon is available (this repo builds via kaniko in CI), skip the local build and rely on the CI `build:document-scanner` job; the assertion to confirm is the same — `scanimage` present, `convert` (imagemagick) absent.

- [ ] **Step 3: Commit**

```bash
git add images/document-scanner/Dockerfile
git commit -m "build(scanner): drop imagemagick from driver image (processor concern)"
```

---

## Task 5: Chart passthrough — mirror optical-ripper

**Files:**
- Modify: `charts/recognizer/templates/document-scanner/daemonset.yaml`
- Modify: `charts/recognizer/templates/document-scanner/configmap.yaml`
- Modify: `charts/recognizer/templates/document-scanner/service.yaml`
- Modify: `charts/recognizer/values.yaml`

Reference the working pattern in `charts/recognizer/templates/optical-ripper/daemonset.yaml` for exact gating syntax.

- [x] **Step 1: configmap.yaml + service.yaml namespace**

In each, change `namespace: {{ .Release.Namespace }}` to:
```yaml
  namespace: {{ include "recognizer.hardwareNamespace" . }}
```

While in `configmap.yaml`, prune now-dead keys: the new env-only `main.go` reads none of the configmap (it's mounted at `/etc/scanner` but unused), so drop stale entries like `idle_timeout_seconds`, `device_name`, `relay_url`. Leave the configmap (and its mount) in place only if it still carries something live; otherwise it's harmless dead config — pruning is preferred but not blocking.

- [x] **Step 2: daemonset.yaml**

Change the `metadata.namespace` line the same way. Then, on the single container, add a privileged securityContext and a `/dev` mount, and add the hostPath volume — **all gated on `hardware.enabled`** (the namespace helper handles the legacy fallback on its own). Container spec gains:

```yaml
          {{- if .Values.hardware.enabled }}
          securityContext:
            privileged: true
          {{- end }}
```
and in the container's `volumeMounts`:
```yaml
            {{- if .Values.hardware.enabled }}
            - name: host-dev
              mountPath: /dev
            {{- end }}
```
and in the pod `volumes`:
```yaml
        {{- if .Values.hardware.enabled }}
        - name: host-dev
          hostPath:
            path: /dev
            type: Directory
        {{- end }}
```

- [x] **Step 3: values.yaml** — remove the dead `smarter-devices/bus-usb` limit from `documentScanner.resources.limits` (delete the comment block + the `smarter-devices/bus-usb: 1` line). The `limits` block becomes just `cpu`/`memory`.

  > **Deviation from spec §4.1 (intentional):** the spec suggested mirroring optical-ripper by *conditionally re-adding* a `smarter-devices` limit under `hardware.enabled=false`. We drop it unconditionally instead: that per-device USB resource name never matched anyway (it's dynamically numbered), so legacy-mode USB passthrough is non-functional regardless, and `hardware.enabled` defaults true. Simpler and the acceptance criteria don't exercise legacy mode.

- [x] **Step 4: Verify renders (hardware.enabled=true)**

```bash
cd /mnt/c/Users/steve/Code/recognizer
helm template r charts/recognizer --namespace recognizer --kube-version 1.34.0 --set hardware.enabled=true \
  --show-only templates/document-scanner/daemonset.yaml \
  | grep -E 'namespace:|privileged:|path: /dev' 
```
Expected: `namespace: recognizer-hardware`, `privileged: true`, `path: /dev` all present. (The `--kube-version 1.34.0` is required — the chart sets `kubeVersion: ">=1.30.0-0"` and `helm template` otherwise uses the helm binary's compiled-in version, which `.gitlab-ci.yml` documents can be too old.) Confirm **no** `smarter-devices` line:
```bash
helm template r charts/recognizer --namespace recognizer --kube-version 1.34.0 --show-only templates/document-scanner/daemonset.yaml | grep smarter-devices || echo "no smarter-devices (correct)"
```

- [x] **Step 5: Verify renders (hardware.enabled=false — PSS-restricted safe)**

```bash
helm template r charts/recognizer --namespace recognizer --kube-version 1.34.0 --set hardware.enabled=false \
  --show-only templates/document-scanner/daemonset.yaml \
  | grep -E 'privileged:|path: /dev' && echo "UNEXPECTED" || echo "no privileged/dev in legacy mode (correct)"
helm lint charts/recognizer
```
Expected: no privileged/`/dev` in legacy mode; `helm lint` passes.

- [x] **Step 6: Commit**

```bash
git add charts/recognizer/templates/document-scanner/ charts/recognizer/values.yaml
git commit -m "feat(chart): document-scanner privileged /dev passthrough in hardware ns (gated)"
```

---

## Task 6: Cut a release so the cluster deploys the new code

**Files:**
- Modify: `charts/recognizer/Chart.yaml`

Without this, the deployed pod stays pinned to the **current** image/chart version (the `recognizer.image` helper defaults `documentScanner.image.tag` to `Chart.AppVersion`, currently `0.6.1`). CI only pushes a versioned image + chart OCI artifact on a `v*` tag, and Flux deploys the chart version. So new code on `main` alone will **not** reach johnny — the manual gate (Task 7) would keep testing the old stub. Every recent commit is a `release(chart):` bump for exactly this reason.

- [ ] **Step 1:** Bump `charts/recognizer/Chart.yaml` — set both `version` and `appVersion` to `0.7.0` (next minor; this slice is a feature).

- [ ] **Step 2:** Verify the chart still renders/lints at the new version:
```bash
helm lint charts/recognizer
helm template r charts/recognizer --namespace recognizer --kube-version 1.34.0 >/dev/null && echo "renders OK"
```

- [ ] **Step 3: Commit, then merge the branch to `main` and tag the release** (matching repo convention — confirm the exact tag/push flow with the maintainer if unsure):
```bash
git add charts/recognizer/Chart.yaml
git commit -m "release(chart): recognizer 0.7.0 - document-scanner driver scans DS-1630 (Slice 1)"
# after PR/merge to main:
git tag v0.7.0 && git push origin v0.7.0
```

- [ ] **Step 4:** Wait for CI to publish `document-scanner:0.7.0` + the chart OCI artifact, and for **Flux to reconcile** the `recognizer` release. Confirm:
```bash
kubectl get pod -n recognizer-hardware -l app.kubernetes.io/component=document-scanner \
  -o jsonpath='{.items[0].spec.containers[0].image}{"\n"}'
```
Expected: image ends in `document-scanner:0.7.0`.

---

## Task 7: In-cluster acceptance (MANUAL GATE)

Talos nodes have **no SSH**; drive via `kubectl`. Requires a human at `johnny` to load/empty the ADF. Runs **only after Task 6** has the `0.7.0` pod live (do not hand-copy files into the pod — CLAUDE.md: always rebuild a container to deliver code).

- [ ] **Step 1:** Confirm the pod is running on johnny:
```bash
kubectl get pods -n recognizer-hardware -l app.kubernetes.io/component=document-scanner -o wide
```
Expected: one pod, `Running`, `NODE = johnny`.

- [ ] **Step 2: Status check**
```bash
POD=$(kubectl get pod -n recognizer-hardware -l app.kubernetes.io/component=document-scanner -o name)
kubectl exec -n recognizer-hardware $POD -- curl -s localhost:8080/status
```
Expected: `{"device_present":true,"device":"epsonds:libusb:...:..."}`.

- [ ] **Step 3: Empty-feeder (load nothing in the ADF)**
```bash
kubectl exec -n recognizer-hardware $POD -- curl -s -o /dev/null -w '%{http_code}\n' \
  -X POST localhost:8080/scan -d '{"source":"ADF Duplex","mode":"Color","resolution":300}'
```
Expected: `422`.

- [ ] **Step 4: Duplex scan (load one double-sided sheet in the ADF)**
```bash
kubectl exec -n recognizer-hardware $POD -- curl -s \
  -X POST localhost:8080/scan -d '{"source":"ADF Duplex","mode":"Color","resolution":300}'
```
Expected: `200` with `pages` = 2 (`front`, `back`) and an `output_dir`.

- [ ] **Step 5: Confirm files on the PVC**
```bash
DIR=... # output_dir from step 4
kubectl exec -n recognizer-hardware $POD -- ls -l "$DIR"
```
Expected: `page_01.tiff`, `page_02.tiff` present, non-zero size.

- [ ] **Step 6:** Close `archiver-9aj` (chart wiring) and `archiver-hee` (acceptance):
```bash
bd close archiver-9aj archiver-hee --reason "Slice 1: driver reaches DS-1630 on johnny; duplex scan to PVC verified; empty feeder -> 422"
```

---

## Done criteria

- `go build ./... && go vet ./... && go test ./...` clean; `staticcheck` clean.
- `session`/`notify`/`manifest` gone from the driver; driver image has `scanimage`, not `convert`.
- Chart renders privileged + `/dev` + `recognizer-hardware` when `hardware.enabled=true`; clean/restricted when false.
- Manual gate (Task 7) green: status reports the device; empty feeder → 422; duplex → 2 TIFFs on the PVC.

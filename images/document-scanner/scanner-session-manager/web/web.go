// Package web provides the scanner driver's stateless HTTP interface.
//
// This is the driver half of the driver/processor split (Slice 1): it owns
// the SANE device and nothing else. There is no session state, no manifest,
// and no notification -- a request scans, writes TIFFs to the data volume,
// and reports where they landed. Grouping pages into documents, writing
// manifests and telling the relay about them are the (unprivileged)
// processor's job in Slice 2.
package web

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/scan"
)

// Handler is the stateless scanner driver HTTP handler.
type Handler struct {
	scanner    *scan.Scanner
	outputBase string
	mux        *http.ServeMux

	// DeviceOverride, when non-empty, is used verbatim instead of probing
	// with `scanimage -L` on every request. main.go populates it from
	// SCANNER_DEVICE; it exists as the operator escape hatch for a scanner
	// the detection heuristic doesn't recognize.
	DeviceOverride string

	// One scanner, one scan at a time. The device is a single exclusive USB
	// handle, so a second concurrent scanimage would fail (or worse,
	// half-succeed) rather than queue. Callers get 409 instead.
	busy sync.Mutex
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

// The DS-1630's discrete option sets, as reported by `scanimage --help -d
// epsonds:...`. A value that is merely *in range* (e.g. 250 dpi) is still
// rejected by the backend, so validate against the sets rather than bounds.
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
	device, err := h.resolveDevice(r)
	writeJSON(w, http.StatusOK, statusResponse{DevicePresent: err == nil, Device: device})
}

// resolveDevice returns the operator's explicit device if one is configured,
// otherwise probes for it. Probing per request (rather than caching at
// startup) is what lets the driver survive a scanner that is unplugged and
// replugged under a running pod.
func (h *Handler) resolveDevice(r *http.Request) (string, error) {
	if h.DeviceOverride != "" {
		return h.DeviceOverride, nil
	}
	return h.scanner.DetectDevice(r.Context())
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

	// Claimed before detection so a busy scanner isn't probed mid-scan.
	if !h.busy.TryLock() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "scanner busy"})
		return
	}
	defer h.busy.Unlock()

	ctx := r.Context()
	device, err := h.resolveDevice(r)
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

	// The DS-1630 duplexes by feeding each sheet once and emitting front
	// then back, so odd pages are fronts and even pages their backs.
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
	_, _ = rand.Read(b)
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

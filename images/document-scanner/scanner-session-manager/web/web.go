// Package web provides the scanner session manager HTTP interface.
package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/scan"
	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/session"
)

// ScanSettings holds the scan parameters applied to every /scan request.
// Populated from config.Config in main.go.
type ScanSettings struct {
	ResolutionDPI int
	ColorMode     scan.ScanMode
	Format        scan.OutputFormat
}

// Handler holds dependencies for the HTTP handlers.
type Handler struct {
	sessions *session.Manager
	scanner  *scan.Scanner
	settings ScanSettings

	deviceOverride string // from SCANNER_DEVICE / config; empty means auto-detect

	deviceMu     sync.Mutex
	cachedDevice string // populated on first successful auto-detect

	mux *http.ServeMux
}

// NewHandler creates the web handler with all routes registered. device is
// either an explicit override (env SCANNER_DEVICE) or empty to auto-detect
// via scanimage -f on first use (see resolveDevice).
func NewHandler(sessions *session.Manager, scanner *scan.Scanner, device string, settings ScanSettings) *Handler {
	h := &Handler{
		sessions:       sessions,
		scanner:        scanner,
		settings:       settings,
		deviceOverride: device,
		mux:            http.NewServeMux(),
	}
	h.mux.HandleFunc("/status", h.handleStatus)
	h.mux.HandleFunc("/scan", h.handleScan)
	h.mux.HandleFunc("/session/close", h.handleCloseSession)
	h.mux.HandleFunc("/session/new-document", h.handleNewDocument)
	h.mux.HandleFunc("/settings", h.handleSettings)
	h.mux.HandleFunc("/healthz", h.handleHealthz)
	return h
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	current := h.sessions.Current()
	var currentInfo *sessionInfo
	if current != nil {
		currentInfo = &sessionInfo{
			ID:          current.ID,
			InputMethod: string(current.InputMethod),
			PageCount:   len(current.Pages),
		}
	}

	closed := h.sessions.ClosedSessions()
	recent := make([]sessionInfo, 0, len(closed))
	for _, s := range closed {
		recent = append(recent, sessionInfo{
			ID:          s.ID,
			InputMethod: string(s.InputMethod),
			PageCount:   len(s.Pages),
		})
	}

	resp := statusResponse{
		State:          string(h.sessions.State()),
		Device:         h.displayDevice(),
		CurrentSession: currentInfo,
		RecentSessions: recent,
	}
	writeJSON(w, http.StatusOK, resp)
}

// displayDevice reports the device without forcing detection: the explicit
// override, else whatever auto-detect last found (empty if none yet).
func (h *Handler) displayDevice() string {
	if h.deviceOverride != "" {
		return h.deviceOverride
	}
	h.deviceMu.Lock()
	defer h.deviceMu.Unlock()
	return h.cachedDevice
}

// resolveDevice returns the configured device, auto-detecting (and caching)
// via scanimage -f on first use if no override was configured.
func (h *Handler) resolveDevice(r *http.Request) (string, error) {
	if h.deviceOverride != "" {
		return h.deviceOverride, nil
	}
	h.deviceMu.Lock()
	defer h.deviceMu.Unlock()
	if h.cachedDevice != "" {
		return h.cachedDevice, nil
	}
	if h.scanner == nil {
		return "", errors.New("no scanner configured")
	}
	dev, err := h.scanner.ResolveDevice(r.Context(), "")
	if err != nil {
		return "", err
	}
	h.cachedDevice = dev
	return dev, nil
}

// scanRequest is the optional JSON body for POST /scan. All fields are
// optional; an empty/absent body scans a single flatbed page, matching the
// previous stub's default assumption.
type scanRequest struct {
	InputMethod string `json:"input_method"` // "flatbed" (default) or "adf"
	Duplex      bool   `json:"duplex"`
	Side        string `json:"side"` // "front"/"back"/"single"; defaults to "single"
}

func (h *Handler) handleScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req scanRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
			return
		}
	}

	var method session.InputMethod
	switch req.InputMethod {
	case "", "flatbed":
		method = session.InputFlatbed
	case "adf":
		method = session.InputADF
	default:
		http.Error(w, fmt.Sprintf("invalid input_method %q", req.InputMethod), http.StatusBadRequest)
		return
	}
	side := req.Side
	if side == "" {
		side = "single"
	}

	if h.scanner == nil {
		http.Error(w, "scanner not configured", http.StatusServiceUnavailable)
		return
	}

	device, err := h.resolveDevice(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("scanner device unavailable: %v", err), http.StatusServiceUnavailable)
		return
	}

	source := scan.SourceFlatbed
	if method == session.InputADF {
		if req.Duplex {
			source = scan.SourceADFDuplex
		} else {
			source = scan.SourceADFFront
		}
	}

	sess, filename := h.sessions.AddPage(method, req.Duplex, side)

	params := scan.Params{
		Resolution: h.settings.ResolutionDPI,
		Mode:       h.settings.ColorMode,
		Source:     source,
		Format:     h.settings.Format,
		OutputPath: filepath.Join(sess.OutputDir, filename),
		Device:     device,
	}

	if err := h.scanner.ScanPage(r.Context(), params); err != nil {
		// No file was written for this page -- drop its reservation so a
		// later manifest.Build (on session close) doesn't fail stat'ing a
		// file that never existed.
		h.sessions.RemoveLastPage()
		http.Error(w, fmt.Sprintf("scan failed: %v", err), http.StatusBadGateway)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":       "scan complete",
		"session_id":   sess.ID,
		"filename":     filename,
		"page_number":  len(sess.Pages),
		"side":         side,
		"input_method": string(method),
	})
}

func (h *Handler) handleCloseSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	closed := h.sessions.CloseSession()
	if closed == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "no active session"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":     "session closed",
		"session_id": closed.ID,
		"page_count": len(closed.Pages),
	})
}

func (h *Handler) handleNewDocument(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	closed := h.sessions.NewDocument()
	if closed == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "no active session"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":     "document boundary created",
		"closed_id":  closed.ID,
		"page_count": len(closed.Pages),
	})
}

func (h *Handler) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Settings updates will be implemented when config hot-reload is added
	writeJSON(w, http.StatusOK, map[string]string{"status": "settings endpoint ready"})
}

func (h *Handler) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type statusResponse struct {
	State          string        `json:"state"`
	Device         string        `json:"device"`
	CurrentSession *sessionInfo  `json:"current_session"`
	RecentSessions []sessionInfo `json:"recent_sessions"`
}

type sessionInfo struct {
	ID          string `json:"id"`
	InputMethod string `json:"input_method"`
	PageCount   int    `json:"page_count"`
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

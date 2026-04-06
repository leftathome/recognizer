// Package web provides the scanner session manager HTTP interface.
package web

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/leftathome/scanner-session-manager/scan"
	"github.com/leftathome/scanner-session-manager/session"
)

// Handler holds dependencies for the HTTP handlers.
type Handler struct {
	sessions *session.Manager
	scanner  *scan.Scanner
	device   string
	mux      *http.ServeMux
}

// NewHandler creates the web handler with all routes registered.
func NewHandler(sessions *session.Manager, scanner *scan.Scanner, device string) *Handler {
	h := &Handler{
		sessions: sessions,
		scanner:  scanner,
		device:   device,
		mux:      http.NewServeMux(),
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
		Device:         h.device,
		CurrentSession: currentInfo,
		RecentSessions: recent,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "scan triggered"})
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

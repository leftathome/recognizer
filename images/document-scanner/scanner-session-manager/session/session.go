// Package session manages scan session lifecycle: grouping pages, open/close semantics.
package session

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// State represents the session manager's current state.
type State string

const (
	StateIdle       State = "idle"
	StateOpen       State = "open"
	StateClosed     State = "closed"
)

// InputMethod indicates how pages were scanned.
type InputMethod string

const (
	InputADF     InputMethod = "adf"
	InputFlatbed InputMethod = "flatbed"
)

// Page records metadata for a single scanned page.
type Page struct {
	Filename string
	Side     string // "front", "back", "single"
}

// Session represents a group of scanned pages.
type Session struct {
	ID           string
	StartTime    time.Time
	EndTime      time.Time
	InputMethod  InputMethod
	Duplex       bool
	Pages        []Page
	OutputDir    string
}

// Clock abstracts time for testing.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// RealClock uses the system clock.
type RealClock struct{}

func (RealClock) Now() time.Time                         { return time.Now() }
func (RealClock) After(d time.Duration) <-chan time.Time  { return time.After(d) }

// Manager manages scan session lifecycle.
type Manager struct {
	mu           sync.Mutex
	state        State
	current      *Session
	closed       []*Session
	baseDir      string
	idleTimeout  time.Duration
	clock        Clock
	idleTimer    *time.Timer
	onClose      func(*Session) // callback when a session closes
}

// Config holds session manager configuration.
type Config struct {
	BaseDir     string        // root output directory
	IdleTimeout time.Duration // flatbed idle timeout (default 90s)
	Clock       Clock         // time source (nil = real clock)
	OnClose     func(*Session) // called when a session closes
}

// NewManager creates a session manager.
func NewManager(cfg Config) *Manager {
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 90 * time.Second
	}
	clk := cfg.Clock
	if clk == nil {
		clk = RealClock{}
	}
	return &Manager{
		state:       StateIdle,
		baseDir:     cfg.BaseDir,
		idleTimeout: cfg.IdleTimeout,
		clock:       clk,
		onClose:     cfg.OnClose,
	}
}

// State returns the current session state.
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Current returns the active session, or nil if idle.
func (m *Manager) Current() *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current
}

// ClosedSessions returns all completed sessions.
func (m *Manager) ClosedSessions() []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Session, len(m.closed))
	copy(out, m.closed)
	return out
}

func (m *Manager) generateID() string {
	now := m.clock.Now().UTC()
	b := make([]byte, 3)
	rand.Read(b)
	return fmt.Sprintf("%s-%x", now.Format("20060102-150405"), b)
}

func (m *Manager) ensureSession(method InputMethod, duplex bool) *Session {
	if m.current != nil {
		return m.current
	}
	id := m.generateID()
	dir := filepath.Join(m.baseDir, id)
	os.MkdirAll(dir, 0o755)
	m.current = &Session{
		ID:          id,
		StartTime:   m.clock.Now().UTC(),
		InputMethod: method,
		Duplex:      duplex,
		OutputDir:   dir,
	}
	m.state = StateOpen
	return m.current
}

// AddPage records a scanned page in the current session.
// Opens a new session if none is active.
func (m *Manager) AddPage(method InputMethod, duplex bool, side string) (sess *Session, filename string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.stopIdleTimer()

	s := m.ensureSession(method, duplex)
	pageNum := len(s.Pages) + 1
	filename = fmt.Sprintf("page_%03d.tiff", pageNum)
	s.Pages = append(s.Pages, Page{Filename: filename, Side: side})

	return s, filename
}

// CloseSession closes the current session. No-op if idle.
func (m *Manager) CloseSession() *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closeCurrentLocked()
}

// ADFComplete signals that the ADF feeder is empty, closing the session.
func (m *Manager) ADFComplete() *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closeCurrentLocked()
}

// StartIdleTimer starts the flatbed idle timeout.
// When it fires, the current session is closed.
func (m *Manager) StartIdleTimer() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopIdleTimer()
	m.idleTimer = time.AfterFunc(m.idleTimeout, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.closeCurrentLocked()
	})
}

// NewDocument closes the current session and prepares for a new one.
func (m *Manager) NewDocument() *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closeCurrentLocked()
}

func (m *Manager) stopIdleTimer() {
	if m.idleTimer != nil {
		m.idleTimer.Stop()
		m.idleTimer = nil
	}
}

func (m *Manager) closeCurrentLocked() *Session {
	if m.current == nil {
		return nil
	}
	m.stopIdleTimer()
	m.current.EndTime = m.clock.Now().UTC()
	closed := m.current
	m.closed = append(m.closed, closed)
	m.current = nil
	m.state = StateIdle
	if m.onClose != nil {
		go m.onClose(closed)
	}
	return closed
}

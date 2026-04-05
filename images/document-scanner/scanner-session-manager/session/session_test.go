package session

import (
	"os"
	"sync"
	"testing"
	"time"
)

type mockClock struct {
	mu  sync.Mutex
	now time.Time
}

func newMockClock(t time.Time) *mockClock {
	return &mockClock{now: t}
}

func (c *mockClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mockClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- c.now.Add(d)
	return ch
}

func (c *mockClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func baseTime() time.Time {
	return time.Date(2026, 4, 4, 18, 30, 45, 0, time.UTC)
}

func TestNewManager_DefaultIdleTimeout(t *testing.T) {
	m := NewManager(Config{BaseDir: t.TempDir()})
	if m.idleTimeout != 90*time.Second {
		t.Errorf("expected 90s idle timeout, got %v", m.idleTimeout)
	}
}

func TestNewManager_StartsIdle(t *testing.T) {
	m := NewManager(Config{BaseDir: t.TempDir()})
	if m.State() != StateIdle {
		t.Errorf("expected idle state, got %v", m.State())
	}
	if m.Current() != nil {
		t.Error("expected nil current session")
	}
}

func TestAddPage_OpensSession(t *testing.T) {
	clk := newMockClock(baseTime())
	m := NewManager(Config{BaseDir: t.TempDir(), Clock: clk})

	sess, filename := m.AddPage(InputADF, true, "front")

	if m.State() != StateOpen {
		t.Errorf("expected open state, got %v", m.State())
	}
	if sess == nil {
		t.Fatal("expected session, got nil")
	}
	if filename != "page_001.tiff" {
		t.Errorf("expected page_001.tiff, got %s", filename)
	}
	if sess.InputMethod != InputADF {
		t.Errorf("expected ADF, got %v", sess.InputMethod)
	}
	if !sess.Duplex {
		t.Error("expected duplex true")
	}
	if len(sess.Pages) != 1 {
		t.Errorf("expected 1 page, got %d", len(sess.Pages))
	}
}

func TestAddPage_AccumulatesPages(t *testing.T) {
	clk := newMockClock(baseTime())
	m := NewManager(Config{BaseDir: t.TempDir(), Clock: clk})

	m.AddPage(InputADF, true, "front")
	m.AddPage(InputADF, true, "back")
	_, filename := m.AddPage(InputADF, true, "front")

	if filename != "page_003.tiff" {
		t.Errorf("expected page_003.tiff, got %s", filename)
	}
	sess := m.Current()
	if len(sess.Pages) != 3 {
		t.Errorf("expected 3 pages, got %d", len(sess.Pages))
	}
}

func TestADFComplete_ClosesSession(t *testing.T) {
	clk := newMockClock(baseTime())
	var closedSess *Session
	m := NewManager(Config{
		BaseDir: t.TempDir(),
		Clock:   clk,
		OnClose: func(s *Session) { closedSess = s },
	})

	m.AddPage(InputADF, true, "front")
	m.AddPage(InputADF, true, "back")

	clk.Advance(30 * time.Second)
	closed := m.ADFComplete()

	if closed == nil {
		t.Fatal("expected closed session")
	}
	if m.State() != StateIdle {
		t.Errorf("expected idle after ADF complete, got %v", m.State())
	}
	if len(closed.Pages) != 2 {
		t.Errorf("expected 2 pages, got %d", len(closed.Pages))
	}
	if closed.EndTime.IsZero() {
		t.Error("expected end time to be set")
	}

	// onClose callback fires asynchronously
	time.Sleep(10 * time.Millisecond)
	if closedSess == nil {
		t.Error("expected onClose callback to fire")
	}
}

func TestCloseSession_NoOpWhenIdle(t *testing.T) {
	m := NewManager(Config{BaseDir: t.TempDir()})
	closed := m.CloseSession()
	if closed != nil {
		t.Error("expected nil when closing idle session")
	}
}

func TestCloseSession_ClosesOpenSession(t *testing.T) {
	clk := newMockClock(baseTime())
	m := NewManager(Config{BaseDir: t.TempDir(), Clock: clk})

	m.AddPage(InputFlatbed, false, "single")
	closed := m.CloseSession()

	if closed == nil {
		t.Fatal("expected closed session")
	}
	if m.State() != StateIdle {
		t.Errorf("expected idle, got %v", m.State())
	}
}

func TestNewDocument_ClosesAndAllowsNew(t *testing.T) {
	clk := newMockClock(baseTime())
	m := NewManager(Config{BaseDir: t.TempDir(), Clock: clk})

	m.AddPage(InputFlatbed, false, "single")
	first := m.NewDocument()

	if first == nil {
		t.Fatal("expected first session closed")
	}
	if m.State() != StateIdle {
		t.Errorf("expected idle after new document, got %v", m.State())
	}

	// Start a second session
	m.AddPage(InputFlatbed, false, "single")
	second := m.Current()

	if second == nil {
		t.Fatal("expected new session opened")
	}
	if first.ID == second.ID {
		t.Error("expected different session IDs")
	}
}

func TestClosedSessions_Accumulates(t *testing.T) {
	clk := newMockClock(baseTime())
	m := NewManager(Config{BaseDir: t.TempDir(), Clock: clk})

	m.AddPage(InputADF, false, "single")
	m.ADFComplete()

	clk.Advance(time.Second)

	m.AddPage(InputFlatbed, false, "single")
	m.CloseSession()

	closed := m.ClosedSessions()
	if len(closed) != 2 {
		t.Errorf("expected 2 closed sessions, got %d", len(closed))
	}
}

func TestFlatbed_SessionID_Format(t *testing.T) {
	clk := newMockClock(baseTime())
	m := NewManager(Config{BaseDir: t.TempDir(), Clock: clk})

	m.AddPage(InputFlatbed, false, "single")
	sess := m.Current()

	// ID format: YYYYMMDD-HHMMSS-hexrand
	if len(sess.ID) < 16 {
		t.Errorf("session ID too short: %s", sess.ID)
	}
	// Should start with the date/time portion
	prefix := "20260404-183045-"
	if sess.ID[:16] != prefix {
		t.Errorf("expected ID prefix %q, got %q", prefix, sess.ID[:16])
	}
}

func TestIdleTimer_ClosesSession(t *testing.T) {
	m := NewManager(Config{
		BaseDir:     t.TempDir(),
		IdleTimeout: 50 * time.Millisecond,
	})

	m.AddPage(InputFlatbed, false, "single")
	m.StartIdleTimer()

	// Wait for timer to fire
	time.Sleep(100 * time.Millisecond)

	if m.State() != StateIdle {
		t.Errorf("expected idle after timer, got %v", m.State())
	}
	if len(m.ClosedSessions()) != 1 {
		t.Errorf("expected 1 closed session, got %d", len(m.ClosedSessions()))
	}
}

func TestIdleTimer_ResetOnNewPage(t *testing.T) {
	m := NewManager(Config{
		BaseDir:     t.TempDir(),
		IdleTimeout: 80 * time.Millisecond,
	})

	m.AddPage(InputFlatbed, false, "single")
	m.StartIdleTimer()

	// Add another page before timer fires, which resets the timer
	time.Sleep(40 * time.Millisecond)
	m.AddPage(InputFlatbed, false, "single")
	m.StartIdleTimer()

	// Original timer would have fired by now but was reset
	time.Sleep(50 * time.Millisecond)
	if m.State() != StateOpen {
		t.Errorf("expected session still open, got %v", m.State())
	}

	// Wait for the reset timer
	time.Sleep(50 * time.Millisecond)
	if m.State() != StateIdle {
		t.Errorf("expected idle after reset timer, got %v", m.State())
	}
}

func TestOutputDir_Created(t *testing.T) {
	dir := t.TempDir()
	clk := newMockClock(baseTime())
	m := NewManager(Config{BaseDir: dir, Clock: clk})

	m.AddPage(InputFlatbed, false, "single")
	sess := m.Current()

	info, err := os.Stat(sess.OutputDir)
	if err != nil {
		t.Fatalf("session output dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

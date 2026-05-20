package relay

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestPost_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var event map[string]any
		json.NewDecoder(r.Body).Decode(&event)
		if event["schema_version"] != "1.1" {
			t.Errorf("missing schema_version 1.1")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, 3, 10*time.Millisecond)
	err := c.Post(map[string]any{
		"schema_version": "1.1",
		"event_id":       "evt_EXAMPLE_001",
		"media_type":     "archive/google-takeout/mail",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPost_RetriesOn500(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if atomic.LoadInt32(&hits) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, 5, time.Millisecond)
	if err := c.Post(map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Errorf("hits = %d, want 3", got)
	}
}

func TestPost_ExitsAfterMaxRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, 2, time.Millisecond)
	if err := c.Post(map[string]any{}); err == nil {
		t.Fatal("expected error after exhausted retries")
	}
}

func TestPost_BodyIsJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Errorf("body not JSON: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, 1, time.Millisecond)
	c.Post(map[string]any{"k": "v"})
}

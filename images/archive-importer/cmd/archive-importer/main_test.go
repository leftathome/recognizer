package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// runIngest invokes the binary as the integration tests would.
// Builds once, reuses the binary across subtests.
func runIngest(t *testing.T, env map[string]string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	bin := buildBinaryOnce(t)
	cmd := exec.Command(bin, args...)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var so, se strings.Builder
	cmd.Stdout = &so
	cmd.Stderr = &se
	err := cmd.Run()
	if exit, ok := err.(*exec.ExitError); ok {
		exitCode = exit.ExitCode()
	}
	return so.String(), se.String(), exitCode
}

var (
	binOnce sync.Once
	binPath string
)

func buildBinaryOnce(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		out := filepath.Join(os.TempDir(), "archive-importer-itest")
		cmd := exec.Command("go", "build", "-o", out, ".")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build: %v\n%s", err, b)
		}
		binPath = out
	})
	return binPath
}

func TestIngest_FullFixture(t *testing.T) {
	var received []map[string]any
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev map[string]any
		json.NewDecoder(r.Body).Decode(&ev)
		mu.Lock()
		received = append(received, ev)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dataRoot := t.TempDir()
	rawDir := filepath.Join(dataRoot, "raw")
	os.MkdirAll(rawDir, 0755)
	src := buildFixtureZip(t, "../../testdata/fixtures/google-takeout-minimal")
	target := filepath.Join(rawDir, "takeout-EXAMPLE.zip")
	copyFile(t, src, target)
	_, stderr, code := runIngest(t,
		map[string]string{
			"ARCHIVE_DATA_ROOT":      dataRoot,
			"NOTIFICATION_RELAY_URL": srv.URL,
		},
		"ingest", target,
	)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}

	matches, _ := filepath.Glob(filepath.Join(dataRoot, "unpacked", "*-takeout-EXAMPLE"))
	if len(matches) != 1 {
		t.Fatalf("expected one unpacked dir, got %v", matches)
	}
	unpacked := matches[0]

	if _, err := os.Stat(filepath.Join(unpacked, "takeout-EXAMPLE.zip")); err != nil {
		t.Errorf("source archive not moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(unpacked, "Takeout", "Mail")); err != nil {
		t.Errorf("Mail subtree not extracted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(unpacked, "archive-layout-manifest.v1.json")); err != nil {
		t.Errorf("manifest missing: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	subtreeEvents := 0
	completeEvents := 0
	for _, ev := range received {
		switch ev["event_type"] {
		case "archive-subtree-recognized":
			subtreeEvents++
		case "archive-import-complete":
			completeEvents++
		}
	}
	if subtreeEvents != 14 {
		t.Errorf("got %d subtree events, want exactly 14", subtreeEvents)
	}
	if completeEvents != 1 {
		t.Errorf("got %d complete events, want 1", completeEvents)
	}
}

func TestIngest_Idempotent_ReusesEventIDs(t *testing.T) {
	var run1, run2 []string
	mkServer := func(ids *[]string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var ev map[string]any
			json.NewDecoder(r.Body).Decode(&ev)
			if id, ok := ev["event_id"].(string); ok {
				*ids = append(*ids, id)
			}
			w.WriteHeader(http.StatusOK)
		}))
	}

	dataRoot := t.TempDir()
	rawDir := filepath.Join(dataRoot, "raw")
	os.MkdirAll(rawDir, 0755)
	src := buildFixtureZip(t, "../../testdata/fixtures/google-takeout-minimal")
	target := filepath.Join(rawDir, "takeout-EXAMPLE.zip")
	copyFile(t, src, target)

	srv1 := mkServer(&run1)
	runIngest(t, map[string]string{"ARCHIVE_DATA_ROOT": dataRoot, "NOTIFICATION_RELAY_URL": srv1.URL}, "ingest", target)
	srv1.Close()

	movedTarget, _ := filepath.Glob(filepath.Join(dataRoot, "unpacked", "*-takeout-EXAMPLE", "takeout-EXAMPLE.zip"))
	if len(movedTarget) != 1 {
		t.Fatalf("expected one moved archive after run 1, got %v", movedTarget)
	}
	srv2 := mkServer(&run2)
	runIngest(t, map[string]string{"ARCHIVE_DATA_ROOT": dataRoot, "NOTIFICATION_RELAY_URL": srv2.URL}, "ingest", movedTarget[0])
	srv2.Close()

	if len(run1) != len(run2) {
		t.Fatalf("event counts differ: %d vs %d", len(run1), len(run2))
	}
	for i := range run1 {
		if run1[i] != run2[i] {
			t.Errorf("event %d: id %q vs %q", i, run1[i], run2[i])
		}
	}
}

func TestIngest_CorruptZip_Exit1(t *testing.T) {
	dataRoot := t.TempDir()
	rawDir := filepath.Join(dataRoot, "raw")
	os.MkdirAll(rawDir, 0755)
	target := filepath.Join(rawDir, "corrupt.zip")
	os.WriteFile(target, []byte("not a zip"), 0644)
	_, _, code := runIngest(t,
		map[string]string{"ARCHIVE_DATA_ROOT": dataRoot, "NOTIFICATION_RELAY_URL": "http://localhost:1"},
		"ingest", target,
	)
	if code != 1 {
		t.Errorf("got exit %d, want 1", code)
	}
}

func TestIngest_RelayDown_Exit3(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	dataRoot := t.TempDir()
	rawDir := filepath.Join(dataRoot, "raw")
	os.MkdirAll(rawDir, 0755)
	src := buildFixtureZip(t, "../../testdata/fixtures/google-takeout-minimal")
	target := filepath.Join(rawDir, "takeout-EXAMPLE.zip")
	copyFile(t, src, target)
	_, _, code := runIngest(t,
		map[string]string{"ARCHIVE_DATA_ROOT": dataRoot, "NOTIFICATION_RELAY_URL": srv.URL},
		"ingest", target,
	)
	if code != 3 {
		t.Errorf("got exit %d, want 3", code)
	}
}

func TestIngest_NoMatcher_Exit2(t *testing.T) {
	dataRoot := t.TempDir()
	rawDir := filepath.Join(dataRoot, "raw")
	os.MkdirAll(rawDir, 0755)
	src := buildFixtureZip(t, "../../testdata/fixtures/not-an-archive")
	target := filepath.Join(rawDir, "no-match-EXAMPLE.zip")
	copyFile(t, src, target)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	_, stderr, code := runIngest(t,
		map[string]string{"ARCHIVE_DATA_ROOT": dataRoot, "NOTIFICATION_RELAY_URL": srv.URL},
		"ingest", target,
	)
	if code != 2 {
		t.Errorf("got exit %d, want 2; stderr=%q", code, stderr)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	s, _ := os.Open(src)
	defer s.Close()
	d, _ := os.Create(dst)
	defer d.Close()
	io.Copy(d, s)
}

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_MissingFileReturnsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Default()
	if cfg != want {
		t.Errorf("got %+v, want defaults %+v", cfg, want)
	}
}

func TestLoad_ParsesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
idle_timeout_seconds: 45
relay_url: "http://recognizer-notification-relay:8080/event"
scan_resolution: 300
scan_color_mode: "Gray"
scan_format: "png"
device_name: "epson-ds-1630"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.IdleTimeoutSeconds != 45 {
		t.Errorf("idle timeout: got %d, want 45", cfg.IdleTimeoutSeconds)
	}
	if cfg.RelayURL != "http://recognizer-notification-relay:8080/event" {
		t.Errorf("relay url: got %q", cfg.RelayURL)
	}
	if cfg.ScanResolution != 300 {
		t.Errorf("resolution: got %d, want 300", cfg.ScanResolution)
	}
	if cfg.ScanColorMode != "Gray" {
		t.Errorf("color mode: got %q, want Gray", cfg.ScanColorMode)
	}
	if cfg.ScanFormat != "png" {
		t.Errorf("format: got %q, want png", cfg.ScanFormat)
	}
	// device_name must NOT surface anywhere on Config -- see package doc.
}

func TestLoad_PartialFileMergesOverDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	// Only overrides relay_url; everything else should keep its default.
	if err := os.WriteFile(path, []byte("relay_url: \"http://relay.example/event\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RelayURL != "http://relay.example/event" {
		t.Errorf("relay url: got %q", cfg.RelayURL)
	}
	if cfg.IdleTimeoutSeconds != 90 {
		t.Errorf("idle timeout should keep default: got %d", cfg.IdleTimeoutSeconds)
	}
	if cfg.ScanResolution != 600 {
		t.Errorf("resolution should keep default: got %d", cfg.ScanResolution)
	}
}

func TestLoad_InvalidYAMLReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("not: [valid: yaml"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestApplyEnv_OverridesFileValues(t *testing.T) {
	cfg := Default()
	env := map[string]string{
		"SCANNER_IDLE_TIMEOUT_SECONDS": "30",
		"SCANNER_RELAY_URL":            "http://override.example/event",
		"SCANNER_RESOLUTION":           "1200",
		"SCANNER_COLOR_MODE":           "Lineart",
		"SCANNER_FORMAT":               "png",
	}
	getenv := func(k string) string { return env[k] }

	got := ApplyEnv(cfg, getenv)
	if got.IdleTimeoutSeconds != 30 {
		t.Errorf("idle timeout: got %d, want 30", got.IdleTimeoutSeconds)
	}
	if got.RelayURL != "http://override.example/event" {
		t.Errorf("relay url: got %q", got.RelayURL)
	}
	if got.ScanResolution != 1200 {
		t.Errorf("resolution: got %d, want 1200", got.ScanResolution)
	}
	if got.ScanColorMode != "Lineart" {
		t.Errorf("color mode: got %q, want Lineart", got.ScanColorMode)
	}
	if got.ScanFormat != "png" {
		t.Errorf("format: got %q, want png", got.ScanFormat)
	}
}

func TestApplyEnv_NoEnvKeepsInput(t *testing.T) {
	cfg := Default()
	got := ApplyEnv(cfg, func(string) string { return "" })
	if got != cfg {
		t.Errorf("expected unchanged config, got %+v", got)
	}
}

func TestApplyEnv_InvalidIntIgnored(t *testing.T) {
	cfg := Default()
	env := map[string]string{"SCANNER_RESOLUTION": "not-a-number"}
	got := ApplyEnv(cfg, func(k string) string { return env[k] })
	if got.ScanResolution != cfg.ScanResolution {
		t.Errorf("expected resolution unchanged on invalid input, got %d", got.ScanResolution)
	}
}

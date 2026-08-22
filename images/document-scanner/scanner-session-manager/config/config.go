// Package config loads the scanner session manager's runtime configuration
// from the mounted /etc/scanner/config.yaml (see
// charts/recognizer/templates/document-scanner/configmap.yaml) with
// environment variables taking precedence over file values.
//
// Note on device_name: the ConfigMap ships a device_name key that still
// holds the pre-fix friendly literal ("epson-ds-1630") rather than a real
// SANE device string. This package intentionally does NOT parse or expose
// that key -- honoring it as a device override would silently reintroduce
// the bug this packet fixes. SANE device resolution is env (SCANNER_DEVICE)
// or auto-detection only; see scan.ResolveDevice. The unused config.yaml key
// is left in place (owned by another template/agent) and simply ignored.
package config

import (
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Config holds scanner session manager settings sourced from config.yaml
// and/or environment variables.
type Config struct {
	IdleTimeoutSeconds int    `yaml:"idle_timeout_seconds"`
	RelayURL           string `yaml:"relay_url"`
	ScanResolution     int    `yaml:"scan_resolution"`
	ScanColorMode      string `yaml:"scan_color_mode"`
	ScanFormat         string `yaml:"scan_format"`
}

// Default returns the built-in defaults, used when config.yaml is absent
// and no environment override is set.
func Default() Config {
	return Config{
		IdleTimeoutSeconds: 90,
		RelayURL:           "",
		ScanResolution:     600,
		ScanColorMode:      "Color",
		ScanFormat:         "tiff",
	}
}

// Load reads and parses the YAML config file at path, merging its values
// over Default(). A missing file is not an error -- the mount is optional
// in some deployment modes -- and Default() is returned unchanged. A
// present-but-unparseable file IS an error so misconfiguration is visible
// in logs rather than silently falling back.
func Load(path string) (Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read config file %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config file %s: %w", path, err)
	}
	return cfg, nil
}

// ApplyEnv returns a copy of cfg with any set environment variables
// overriding the corresponding field. getenv is injected for testability
// (production code passes os.Getenv).
func ApplyEnv(cfg Config, getenv func(string) string) Config {
	if v := getenv("SCANNER_IDLE_TIMEOUT_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.IdleTimeoutSeconds = n
		}
	}
	if v := getenv("SCANNER_RELAY_URL"); v != "" {
		cfg.RelayURL = v
	}
	if v := getenv("SCANNER_RESOLUTION"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.ScanResolution = n
		}
	}
	if v := getenv("SCANNER_COLOR_MODE"); v != "" {
		cfg.ScanColorMode = v
	}
	if v := getenv("SCANNER_FORMAT"); v != "" {
		cfg.ScanFormat = v
	}
	return cfg
}

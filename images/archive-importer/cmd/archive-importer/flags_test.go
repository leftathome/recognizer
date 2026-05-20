package main

import (
	"testing"
)

func TestParseFlags_Defaults(t *testing.T) {
	t.Setenv("ARCHIVE_DATA_ROOT", "")
	t.Setenv("NOTIFICATION_RELAY_URL", "")
	t.Setenv("INCLUDE_UNRECOGNIZED", "")
	t.Setenv("LOG_LEVEL", "")
	c, err := parseFlags([]string{"ingest", "/path/to/archive.zip"})
	if err != nil {
		t.Fatal(err)
	}
	if c.ArchivePath != "/path/to/archive.zip" {
		t.Errorf("ArchivePath = %q", c.ArchivePath)
	}
	if c.DataRoot != "/data/incoming/archives" {
		t.Errorf("DataRoot default = %q", c.DataRoot)
	}
	if c.IncludeUnrecognized {
		t.Error("IncludeUnrecognized default = true")
	}
	if c.LogLevel != "info" {
		t.Errorf("LogLevel default = %q", c.LogLevel)
	}
}

func TestParseFlags_EnvOverrides(t *testing.T) {
	t.Setenv("ARCHIVE_DATA_ROOT", "/custom/root")
	t.Setenv("NOTIFICATION_RELAY_URL", "http://relay.example/notify")
	t.Setenv("INCLUDE_UNRECOGNIZED", "true")
	c, _ := parseFlags([]string{"ingest", "x.zip"})
	if c.DataRoot != "/custom/root" || c.RelayURL != "http://relay.example/notify" || !c.IncludeUnrecognized {
		t.Errorf("env overrides: %+v", c)
	}
}

func TestParseFlags_CLIBeatsEnv(t *testing.T) {
	t.Setenv("ARCHIVE_DATA_ROOT", "/from-env")
	c, _ := parseFlags([]string{"ingest", "--data-root", "/from-cli", "x.zip"})
	if c.DataRoot != "/from-cli" {
		t.Errorf("DataRoot = %q, want /from-cli", c.DataRoot)
	}
}

func TestParseFlags_UnknownVerb(t *testing.T) {
	_, err := parseFlags([]string{"frobnicate", "x.zip"})
	if err == nil {
		t.Error("expected error")
	}
}

func TestParseFlags_MissingArchive(t *testing.T) {
	_, err := parseFlags([]string{"ingest"})
	if err == nil {
		t.Error("expected error")
	}
}

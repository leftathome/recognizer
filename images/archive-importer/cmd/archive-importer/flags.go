package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

type Config struct {
	ArchivePath         string
	DataRoot            string
	RelayURL            string
	IncludeUnrecognized bool
	Force               bool
	IDOverride          string
	DryRun              bool
	LogLevel            string
	// Glovebox archive-delivery (spec 13 / tus.io). When URL+Token+SourceID
	// are all set, recognized subtrees are pushed to glovebox after the
	// matcher pass and Delivery records are written into the manifest.
	GloveboxIngestURL      string
	GloveboxIngestToken    string
	GloveboxIngestSourceID string
}

func parseFlags(args []string) (*Config, error) {
	if len(args) < 1 {
		return nil, errors.New("usage: archive-importer ingest <archive-path>")
	}
	if args[0] != "ingest" {
		return nil, fmt.Errorf("unknown verb %q", args[0])
	}
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	c := &Config{}
	fs.StringVar(&c.DataRoot, "data-root", envOr("ARCHIVE_DATA_ROOT", "/data/incoming/archives"), "Base under which raw/ and unpacked/ live")
	fs.StringVar(&c.RelayURL, "relay-url", envOr("NOTIFICATION_RELAY_URL", ""), "Notification relay URL")
	fs.BoolVar(&c.IncludeUnrecognized, "include-unrecognized", envBool("INCLUDE_UNRECOGNIZED"), "Emit events for unrecognized subtrees")
	fs.BoolVar(&c.Force, "force", false, "Re-extract even if unpacked/<id>/ exists")
	fs.StringVar(&c.IDOverride, "id", "", "Override the derived <id>")
	fs.BoolVar(&c.DryRun, "dry-run", false, "Log everything, emit no events, write no files")
	fs.StringVar(&c.LogLevel, "log-level", envOr("LOG_LEVEL", "info"), "debug / info / warn")
	fs.StringVar(&c.GloveboxIngestURL, "glovebox-url", envOr("GLOVEBOX_INGEST_URL", ""), "Glovebox archive-delivery base URL (enables push)")
	fs.StringVar(&c.GloveboxIngestToken, "glovebox-token", envOr("GLOVEBOX_INGEST_TOKEN", ""), "Glovebox bearer token")
	fs.StringVar(&c.GloveboxIngestSourceID, "glovebox-source-id", envOr("GLOVEBOX_INGEST_SOURCE_ID", ""), "Glovebox-side source_id (e.g. recognizer-smoke-test)")
	if err := fs.Parse(args[1:]); err != nil {
		return nil, err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return nil, errors.New("ingest requires exactly one positional argument: <archive-path>")
	}
	c.ArchivePath = rest[0]
	return c, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string) bool {
	v := os.Getenv(key)
	return v == "1" || v == "true" || v == "yes"
}

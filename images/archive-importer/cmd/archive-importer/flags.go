package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/leftathome/recognizer/images/archive-importer/internal/delivery"
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
	// Glovebox archive-delivery (spec 13 / tus.io). When URL+SourceID are
	// set and a token is available (Token or TokenFile), recognized
	// subtrees are pushed to glovebox after the matcher pass and Delivery
	// records are written into the manifest.
	GloveboxIngestURL       string
	GloveboxIngestToken     string
	GloveboxIngestTokenFile string
	GloveboxIngestSourceID  string
	// GloveboxIngestCAFile, when set, pins an additional trusted CA for
	// the glovebox TLS connection (private CA support). Empty means
	// "system roots only".
	GloveboxIngestCAFile string
	// GloveboxIngestRequireTLS refuses to enable delivery against an
	// http:// GloveboxIngestURL. Off by default (glovebox's bearer
	// listener is plaintext today); forward wiring for the PHI path.
	GloveboxIngestRequireTLS bool
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
	fs.StringVar(&c.GloveboxIngestToken, "glovebox-token", envOr(delivery.EnvToken, ""), "Glovebox bearer token (fallback; prefer -glovebox-token-file)")
	fs.StringVar(&c.GloveboxIngestTokenFile, "glovebox-token-file", envOr(delivery.EnvTokenFile, ""), "Path to a file holding the glovebox bearer token, re-read per request")
	fs.StringVar(&c.GloveboxIngestSourceID, "glovebox-source-id", envOr("GLOVEBOX_INGEST_SOURCE_ID", ""), "Glovebox-side source_id (e.g. recognizer-smoke-test)")
	fs.StringVar(&c.GloveboxIngestCAFile, "glovebox-ca-file", envOr(delivery.EnvCAFile, ""), "PEM bundle of additional trusted CAs for the glovebox TLS connection")
	fs.BoolVar(&c.GloveboxIngestRequireTLS, "glovebox-require-tls", envBool(delivery.EnvRequireTLS), "Refuse to enable glovebox delivery against an http:// URL")
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

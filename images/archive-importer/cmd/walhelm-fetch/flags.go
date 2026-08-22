package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/leftathome/recognizer/images/archive-importer/internal/delivery"
)

// Config holds the resolved flags/env for one walhelm-fetch invocation.
type Config struct {
	SessionFile      string
	SubjectPrincipal string
	StateDir         string
	IngestURL        string
	// IngestToken is the static bearer token fallback, used only when
	// IngestTokenFile is unset.
	IngestToken string
	// IngestTokenFile, when set, is re-read per request so Vault token
	// rotation propagates without a pod restart.
	IngestTokenFile string
	IngestSourceID  string
	// IngestCAFile optionally pins an additional trusted CA for the
	// glovebox TLS connection.
	IngestCAFile string
	// IngestRequireTLS refuses to run against an http:// IngestURL.
	IngestRequireTLS bool
	LogLevel         string
}

// errConfig wraps a configuration problem so main() can map it to exit code 2.
type errConfig struct{ msg string }

func (e *errConfig) Error() string { return e.msg }

func configErrorf(format string, args ...any) error {
	return &errConfig{msg: fmt.Sprintf(format, args...)}
}

// parseFlags resolves flags and environment for walhelm-fetch. Required values
// that are missing produce an *errConfig (mapped to exit 2 by main()).
//
// Flag/env precedence mirrors cmd/archive-importer: an explicit flag wins,
// otherwise the env var, otherwise the documented default. There is no verb
// (this binary does one thing), so all args are flags.
func parseFlags(args []string) (*Config, error) {
	fs := flag.NewFlagSet("walhelm-fetch", flag.ContinueOnError)
	c := &Config{}
	fs.StringVar(&c.SessionFile, "session-file", os.Getenv("WALHELM_SESSION_FILE"), "Path to the walhelm session JSON (env WALHELM_SESSION_FILE)")
	fs.StringVar(&c.SubjectPrincipal, "subject-principal", os.Getenv("WALHELM_SUBJECT_PRINCIPAL"), "data_subject principal, e.g. walhelm:9f2a (env WALHELM_SUBJECT_PRINCIPAL)")
	fs.StringVar(&c.StateDir, "state-dir", envOr("WALHELM_STATE_DIR", "/data/walhelm"), "Directory holding per-subject cursor state (env WALHELM_STATE_DIR)")
	fs.StringVar(&c.IngestURL, "glovebox-url", os.Getenv("GLOVEBOX_INGEST_URL"), "Glovebox archive-delivery base URL (env GLOVEBOX_INGEST_URL)")
	fs.StringVar(&c.IngestToken, "glovebox-token", os.Getenv(delivery.EnvToken), "Glovebox bearer token, fallback (env GLOVEBOX_INGEST_TOKEN)")
	fs.StringVar(&c.IngestTokenFile, "glovebox-token-file", os.Getenv(delivery.EnvTokenFile), "Path to a file holding the glovebox bearer token, re-read per request (env GLOVEBOX_INGEST_TOKEN_FILE)")
	fs.StringVar(&c.IngestSourceID, "glovebox-source-id", os.Getenv("GLOVEBOX_INGEST_SOURCE_ID"), "Glovebox-side source_id (env GLOVEBOX_INGEST_SOURCE_ID)")
	fs.StringVar(&c.IngestCAFile, "glovebox-ca-file", os.Getenv(delivery.EnvCAFile), "PEM bundle of additional trusted CAs for the glovebox TLS connection (env GLOVEBOX_INGEST_CA_FILE)")
	fs.BoolVar(&c.IngestRequireTLS, "glovebox-require-tls", envBool(delivery.EnvRequireTLS), "Refuse to run against an http:// glovebox URL (env GLOVEBOX_INGEST_REQUIRE_TLS)")
	fs.StringVar(&c.LogLevel, "log-level", envOr("LOG_LEVEL", "info"), "debug / info / warn")
	if err := fs.Parse(args); err != nil {
		// flag parse errors are configuration errors (exit 2).
		return nil, configErrorf("%s", err)
	}

	var missing []string
	if c.SessionFile == "" {
		missing = append(missing, "WALHELM_SESSION_FILE/-session-file")
	}
	if c.SubjectPrincipal == "" {
		missing = append(missing, "WALHELM_SUBJECT_PRINCIPAL/-subject-principal")
	}
	if c.IngestURL == "" {
		missing = append(missing, "GLOVEBOX_INGEST_URL/-glovebox-url")
	}
	if c.IngestToken == "" && c.IngestTokenFile == "" {
		missing = append(missing, "GLOVEBOX_INGEST_TOKEN_FILE/-glovebox-token-file (or GLOVEBOX_INGEST_TOKEN/-glovebox-token)")
	}
	if c.IngestSourceID == "" {
		missing = append(missing, "GLOVEBOX_INGEST_SOURCE_ID/-glovebox-source-id")
	}
	if len(missing) > 0 {
		return nil, configErrorf("missing required configuration: %s", strings.Join(missing, ", "))
	}
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

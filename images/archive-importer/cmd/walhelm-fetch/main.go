// Package main is the walhelm-fetch CLI entrypoint: a thin wrapper that loads
// a walhelm session, builds the real walhelm client, and runs one incremental
// fetch->tar->deliver cycle via walhelmsrc.RunOnce. All testable orchestration
// lives in internal/walhelm; this file only maps config + outcomes to exit codes.
//
// Exit codes:
//
//	0 -- success (delivered, or nothing new to deliver)
//	1 -- delivery/other runtime error
//	2 -- configuration error (missing required flag/env)
//	3 -- session expired/invalid (operator must refresh the Vault secret)
//
// See docs/superpowers/plans/2026-06-03-sp3-walhelm-source.md (Task 7).
package main

import (
	"context"
	"errors"
	"log"
	"os"

	walhelmsrc "github.com/leftathome/recognizer/images/archive-importer/internal/walhelm"
)

const (
	exitOK        = 0
	exitError     = 1
	exitConfig    = 2
	exitSessExpir = 3
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run does the work and returns the process exit code. Splitting it out of
// main() keeps os.Exit at a single call site and lets the logic stay linear.
func run(args []string) int {
	logger := log.New(os.Stderr, "walhelm-fetch: ", log.LstdFlags|log.LUTC)

	cfg, err := parseFlags(args)
	if err != nil {
		logger.Printf("config error: %v", err)
		return exitConfig
	}

	sess, err := walhelmsrc.LoadSession(cfg.SessionFile)
	if err != nil {
		if errors.Is(err, walhelmsrc.ErrSessionExpired) {
			logger.Printf("session expired/invalid; run `walhelm login` and refresh the Vault secret: %v", err)
			return exitSessExpir
		}
		logger.Printf("load session: %v", err)
		return exitError
	}

	cli, err := walhelmsrc.NewClientFromSession(sess)
	if err != nil {
		logger.Printf("build client: %v", err)
		return exitError
	}

	rc := walhelmsrc.RunConfig{
		SubjectPrincipal: cfg.SubjectPrincipal,
		StateDir:         cfg.StateDir,
		IngestURL:        cfg.IngestURL,
		IngestToken:      cfg.IngestToken,
		IngestSourceID:   cfg.IngestSourceID,
	}

	delivered, err := walhelmsrc.RunOnce(context.Background(), cli, rc)
	if err != nil {
		logger.Printf("run: %v", err)
		return exitError
	}
	if delivered {
		logger.Printf("delivered walhelm export for subject %s", cfg.SubjectPrincipal)
	} else {
		logger.Printf("nothing new for subject %s; cursors unchanged", cfg.SubjectPrincipal)
	}
	return exitOK
}

// Package main is the archive-importer CLI entrypoint.
// See docs/specs/03-archive-importer-google-takeout.md for the design.
package main

import (
	"fmt"
	"os"
)

func main() {
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = cfg
	// Real ingest flow is wired in Task C2.
	fmt.Fprintln(os.Stderr, "archive-importer: parsed config; main flow not yet wired")
	os.Exit(1)
}

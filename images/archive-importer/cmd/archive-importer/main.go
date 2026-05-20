// Package main is the archive-importer CLI entrypoint.
// See docs/specs/03-archive-importer-google-takeout.md for the design.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "ingest" {
		fmt.Fprintln(os.Stderr, "usage: archive-importer ingest <archive-path>")
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "archive-importer: not implemented yet")
	os.Exit(1)
}

// Command scanner is the document scanner driver entrypoint.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/scan"
	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/web"
)

func main() {
	baseDir := os.Getenv("SCANNER_OUTPUT_DIR")
	if baseDir == "" {
		baseDir = "/out/scans"
	}
	listenAddr := os.Getenv("SCANNER_LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = ":8080"
	}

	scanner := scan.New(scan.ExecCommander{})
	handler := web.NewHandler(scanner, baseDir)

	fmt.Printf("Scanner driver listening on %s\n", listenAddr)
	log.Fatal(http.ListenAndServe(listenAddr, handler))
}

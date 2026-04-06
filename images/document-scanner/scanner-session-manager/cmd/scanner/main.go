// Command scanner is the document scanner session manager entrypoint.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/scan"
	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/session"
	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/web"
)

func main() {
	baseDir := os.Getenv("SCANNER_OUTPUT_DIR")
	if baseDir == "" {
		baseDir = "/out/scans/sessions"
	}
	listenAddr := os.Getenv("SCANNER_LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = ":8080"
	}
	deviceName := os.Getenv("SCANNER_DEVICE_NAME")
	if deviceName == "" {
		deviceName = "epson-ds-1630"
	}

	mgr := session.NewManager(session.Config{
		BaseDir:     baseDir,
		IdleTimeout: 90 * time.Second,
		OnClose: func(s *session.Session) {
			log.Printf("Session %s closed: %d pages", s.ID, len(s.Pages))
		},
	})

	scanner := scan.New(scan.ExecCommander{})
	handler := web.NewHandler(mgr, scanner, deviceName)

	fmt.Printf("Scanner session manager listening on %s\n", listenAddr)
	log.Fatal(http.ListenAndServe(listenAddr, handler))
}

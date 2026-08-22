// Command scanner is the document scanner driver entrypoint.
//
// The driver is stateless: every knob that used to live in the mounted
// ConfigMap (resolution, colour mode, idle timeout) is now a per-request
// field on POST /scan, so the only configuration left is where to write and
// where to listen.
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

	// SCANNER_DEVICE pins the SANE device string when auto-detection can't
	// identify the scanner; SCANNER_DEVICE_NAME is the older spelling, kept
	// so an existing deployment's env doesn't silently stop taking effect.
	handler.DeviceOverride = os.Getenv("SCANNER_DEVICE")
	if handler.DeviceOverride == "" {
		handler.DeviceOverride = os.Getenv("SCANNER_DEVICE_NAME")
	}

	fmt.Printf("Scanner driver listening on %s\n", listenAddr)
	log.Fatal(http.ListenAndServe(listenAddr, handler))
}

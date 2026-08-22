// Command scanner is the document scanner session manager entrypoint.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/config"
	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/manifest"
	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/notify"
	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/scan"
	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/session"
	"github.com/leftathome/recognizer/images/document-scanner/scanner-session-manager/web"
)

func main() {
	cfgPath := os.Getenv("SCANNER_CONFIG_FILE")
	if cfgPath == "" {
		cfgPath = "/etc/scanner/config.yaml"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Printf("warning: %v; continuing with defaults+env overrides", err)
	}
	cfg = config.ApplyEnv(cfg, os.Getenv)

	baseDir := os.Getenv("SCANNER_OUTPUT_DIR")
	if baseDir == "" {
		baseDir = "/out/scans/sessions"
	}
	listenAddr := os.Getenv("SCANNER_LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = ":8080"
	}

	// Device resolution: SCANNER_DEVICE (or SCANNER_DEVICE_NAME, kept for
	// back-compat with earlier deployments) wins if set, otherwise the
	// first device reported by `scanimage -f '%d%n'` is used. The
	// ConfigMap's device_name key is deliberately NOT consulted here -- see
	// the config package doc comment.
	deviceOverride := os.Getenv("SCANNER_DEVICE")
	if deviceOverride == "" {
		deviceOverride = os.Getenv("SCANNER_DEVICE_NAME")
	}

	scanner := scan.New(scan.ExecCommander{})

	device, err := scanner.ResolveDevice(context.Background(), deviceOverride)
	if err != nil {
		log.Printf("warning: could not resolve scanner device: %v; /scan will retry auto-detection per request", err)
		device = ""
	}

	mgr := session.NewManager(session.Config{
		BaseDir:     baseDir,
		IdleTimeout: time.Duration(cfg.IdleTimeoutSeconds) * time.Second,
		OnClose:     closeHandler(scanner, device, cfg, nil),
	})

	settings := web.ScanSettings{
		ResolutionDPI: cfg.ScanResolution,
		ColorMode:     scan.ScanMode(cfg.ScanColorMode),
		Format:        scan.OutputFormat(cfg.ScanFormat),
	}
	handler := web.NewHandler(mgr, scanner, device, settings)

	fmt.Printf("Scanner session manager listening on %s\n", listenAddr)
	log.Fatal(http.ListenAndServe(listenAddr, handler))
}

// closeHandler returns the session.Manager OnClose callback: it builds and
// writes manifest.json for the closed session, then forwards a
// scan-session-complete event to the relay. Nothing here is fatal to the
// process -- a manifest/notify failure is logged and the manager moves on;
// retry and dead-lettering are the relay's job (see notification-relay),
// not this process's.
//
// send is injected for testability; production callers pass nil, which
// defaults to notify.Send.
func closeHandler(scanner *scan.Scanner, device string, cfg config.Config, send func(notify.Event, string) error) func(*session.Session) {
	if send == nil {
		send = notify.Send
	}
	return func(s *session.Session) {
		widthPx, heightPx := scan.StandardPageDimensions(cfg.ScanResolution)
		pages := make([]manifest.PageInfo, 0, len(s.Pages))
		for _, p := range s.Pages {
			pages = append(pages, manifest.PageInfo{
				Filename: p.Filename,
				Side:     p.Side,
				WidthPx:  widthPx,
				HeightPx: heightPx,
			})
		}

		colorMode := scan.SchemaColorMode(scan.ScanMode(cfg.ScanColorMode))
		m, err := manifest.Build(s, device, cfg.ScanResolution, colorMode, pages)
		if err != nil {
			log.Printf("session %s: build manifest: %v", s.ID, err)
			return
		}
		if _, err := manifest.WriteJSON(m, s.OutputDir); err != nil {
			log.Printf("session %s: write manifest: %v", s.ID, err)
			return
		}

		if cfg.RelayURL == "" {
			log.Printf("session %s: relay URL not configured; skipping scan-session-complete notification", s.ID)
			return
		}

		event := notify.BuildEvent(s, device)
		if err := send(event, cfg.RelayURL); err != nil {
			log.Printf("session %s: notify relay: %v", s.ID, err)
		}
	}
}

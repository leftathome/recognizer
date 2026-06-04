package walhelmsrc

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/leftathome/recognizer/images/archive-importer/internal/delivery"
)

// RunConfig carries everything RunOnce needs beyond the WalhelmClient itself:
// the data-subject principal, where cursor state lives, and the glovebox
// archive-delivery target.
type RunConfig struct {
	// SubjectPrincipal is the data_subject, e.g. "walhelm:9f2a" (required).
	SubjectPrincipal string
	// StateDir is the directory in which per-subject cursors are persisted.
	StateDir string
	// IngestURL is the glovebox archive-delivery base URL.
	IngestURL string
	// IngestToken is the bearer token for glovebox.
	IngestToken string
	// IngestSourceID is the recognizer's glovebox source-id.
	IngestSourceID string
	// HTTPClient is optional; nil yields a sane default via delivery.NewClient.
	HTTPClient *http.Client
	// TempDir is where the tar is written; "" falls back to os.TempDir().
	TempDir string
}

// RunOnce performs a single incremental cycle for one Walhelm subject:
// load cursor -> fetch new items -> tar the synthesized tree -> deliver to
// glovebox -> persist the advanced cursor.
//
// Semantics:
//   - Nothing new to deliver (Fetch returns items == 0): returns
//     (false, nil) WITHOUT saving state, so cursors do not advance.
//   - Fetch error: returned verbatim (wrapped by Fetch); no delivery, no
//     state save.
//   - Delivery error: returned; state is NOT saved. This is the at-least-once
//     guarantee -- a failed delivery means the next run re-fetches and re-delivers.
//   - Success: SaveState is called to advance the cursor and (true, nil) is
//     returned. A SaveState failure is wrapped and returned, but note the
//     delivery has already succeeded; the worst case is a duplicate (idempotent)
//     delivery on the next run.
func RunOnce(ctx context.Context, cli WalhelmClient, cfg RunConfig) (delivered bool, err error) {
	st, err := LoadState(cfg.StateDir, cfg.SubjectPrincipal)
	if err != nil {
		return false, fmt.Errorf("load state: %w", err)
	}

	treeDir, newState, items, err := Fetch(ctx, cli, st)
	if err != nil {
		return false, err
	}
	if items == 0 {
		// Nothing to deliver; do not save state (cursors stay put).
		return false, nil
	}
	// Clean up the synthesized tree regardless of what happens below.
	defer os.RemoveAll(treeDir)

	tarPath, sha, size, err := delivery.TarSubtree(treeDir, tmpDirOrDefault(cfg))
	if err != nil {
		return false, fmt.Errorf("walhelm run: tar tree: %w", err)
	}
	defer os.Remove(tarPath)

	item := delivery.Item{
		ArchiveID:       archiveID(cfg.SubjectPrincipal, newState),
		ArchiveFilename: "walhelm-export.tar",
		MediaType:       "archive/walhelm-export",
		MatcherID:       "walhelm/export",
		SHA256:          sha,
		SizeBytes:       size,
		AcqProvider:     "kp-wa",
		AcqAccountID:    cli.AcqAccountID(),
		AcqAuthMethod:   "browser_session",
		DataSubject:     cfg.SubjectPrincipal,
		Audience:        []string{"subject"},
	}
	if verr := item.Validate(cfg.IngestSourceID); verr != nil {
		return false, fmt.Errorf("walhelm run: invalid delivery item: %w", verr)
	}

	client := delivery.NewClient(cfg.IngestURL, cfg.IngestToken, cfg.IngestSourceID, cfg.HTTPClient)
	if _, uerr := client.Upload(ctx, item, tarPath); uerr != nil {
		// At-least-once: do NOT save state on a failed delivery.
		return false, fmt.Errorf("walhelm run: deliver: %w", uerr)
	}

	// Delivery succeeded; advance the cursor. If the save fails the delivery
	// still happened, so report delivered=true and wrap the save error.
	if serr := SaveState(cfg.StateDir, cfg.SubjectPrincipal, newState); serr != nil {
		return true, fmt.Errorf("walhelm run: delivery succeeded but SaveState failed (next run will re-deliver idempotently): %w", serr)
	}
	return true, nil
}

// tmpDirOrDefault returns cfg.TempDir when set, otherwise os.TempDir().
func tmpDirOrDefault(cfg RunConfig) string {
	if cfg.TempDir != "" {
		return cfg.TempDir
	}
	return os.TempDir()
}

// archiveID derives a stable, glovebox-compliant (^[a-zA-Z0-9._-]{1,128}$)
// archive_id from the sanitized subject principal and the fetch-start time
// captured in the new cursor. It is deterministic given the fetch, which is
// what makes glovebox replays idempotent and lets tests assert the value.
func archiveID(principal string, st State) string {
	id := "walhelm-" + safeName(principal, 0) + "-" + st.MessagesSince.UTC().Format("20060102T150405Z")
	if len(id) > 128 {
		id = id[:128]
	}
	return id
}

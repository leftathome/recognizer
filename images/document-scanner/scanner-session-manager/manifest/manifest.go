// Package manifest generates scan session manifest.json files.
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/openclaw/scanner-session-manager/session"
)

// PageEntry represents a single page in the manifest.
type PageEntry struct {
	Filename   string     `json:"filename"`
	Side       string     `json:"side"`
	SizeBytes  int64      `json:"size_bytes"`
	Dimensions Dimensions `json:"dimensions"`
}

// Dimensions holds image pixel dimensions.
type Dimensions struct {
	WidthPx  int `json:"width_px"`
	HeightPx int `json:"height_px"`
}

// Manifest is the scan session manifest matching scan-session-manifest.v1 schema.
type Manifest struct {
	SchemaVersion string      `json:"schema_version"`
	SessionID     string      `json:"session_id"`
	TimestampStart string     `json:"timestamp_start"`
	TimestampEnd   string     `json:"timestamp_end"`
	SourceDevice  string      `json:"source_device"`
	InputMethod   string      `json:"input_method"`
	Duplex        bool        `json:"duplex"`
	ResolutionDPI int         `json:"resolution_dpi"`
	ColorMode     string      `json:"color_mode"`
	PageCount     int         `json:"page_count"`
	Pages         []PageEntry `json:"pages"`
	OutputPath    string      `json:"output_path"`
	UserTags      []string    `json:"user_tags"`
	Notes         string      `json:"notes"`
}

// PageInfo provides file-level details needed to build a page entry.
type PageInfo struct {
	Filename string
	Side     string
	WidthPx  int
	HeightPx int
}

// Build constructs a Manifest from a closed session and page info.
func Build(sess *session.Session, device string, resolution int, colorMode string, pages []PageInfo) (*Manifest, error) {
	if sess == nil {
		return nil, fmt.Errorf("session is nil")
	}

	entries := make([]PageEntry, 0, len(pages))
	for _, p := range pages {
		path := filepath.Join(sess.OutputDir, p.Filename)
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", path, err)
		}
		entries = append(entries, PageEntry{
			Filename:  p.Filename,
			Side:      p.Side,
			SizeBytes: info.Size(),
			Dimensions: Dimensions{
				WidthPx:  p.WidthPx,
				HeightPx: p.HeightPx,
			},
		})
	}

	return &Manifest{
		SchemaVersion:  "1.0",
		SessionID:      sess.ID,
		TimestampStart: sess.StartTime.Format("2006-01-02T15:04:05Z"),
		TimestampEnd:   sess.EndTime.Format("2006-01-02T15:04:05Z"),
		SourceDevice:   device,
		InputMethod:    string(sess.InputMethod),
		Duplex:         sess.Duplex,
		ResolutionDPI:  resolution,
		ColorMode:      colorMode,
		PageCount:      len(entries),
		Pages:          entries,
		OutputPath:     sess.OutputDir,
		UserTags:       []string{},
		Notes:          "",
	}, nil
}

// WriteJSON writes the manifest to manifest.json in the session directory.
// Uses atomic write (temp file + rename) to avoid partial reads.
func WriteJSON(m *Manifest, dir string) (string, error) {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal manifest: %w", err)
	}

	outPath := filepath.Join(dir, "manifest.json")
	tmpPath := outPath + ".tmp"

	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return "", fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmpPath, outPath); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("rename manifest: %w", err)
	}

	return outPath, nil
}

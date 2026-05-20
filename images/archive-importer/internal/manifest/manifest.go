// Package manifest reads and writes the archive-layout-manifest.v1.json
// sidecar. See docs/specs/03-archive-importer-google-takeout.md § 6.2.
package manifest

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const ManifestFilename = "archive-layout-manifest.v1.json"

type Manifest struct {
	SchemaVersion        string                `json:"schema_version"`
	ArchiveID            string                `json:"archive_id"`
	Source               Source                `json:"source"`
	Provider             *string               `json:"provider"`
	MatcherVersion       string                `json:"matcher_version"`
	Timestamps           Timestamps            `json:"timestamps"`
	SubtreesRecognized   []SubtreeRecognized   `json:"subtrees_recognized"`
	SubtreesUnrecognized []SubtreeUnrecognized `json:"subtrees_unrecognized"`
	EventsEmitted        []EventEmitted        `json:"events_emitted"`
}

type Source struct {
	OriginalFilename string `json:"original_filename"`
	MovedTo          string `json:"moved_to"`
	SHA256           string `json:"sha256"`
	SizeBytes        int64  `json:"size_bytes"`
	Mtime            string `json:"mtime"`
	ArchiveFormat    string `json:"archive_format"`
}

type Timestamps struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type SubtreeRecognized struct {
	MediaType  string `json:"media_type"`
	OutputPath string `json:"output_path"`
	ItemCount  *int64 `json:"item_count"`
	ByteSize   int64  `json:"byte_size"`
	EventID    string `json:"event_id"`
}

type SubtreeUnrecognized struct {
	Path         string `json:"path"`
	FirstSeen    string `json:"first_seen"`
	ByteSize     int64  `json:"byte_size"`
	EmittedEvent bool   `json:"emitted_event"`
}

type EventEmitted struct {
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"`
	MediaType string `json:"media_type"`
	Timestamp string `json:"timestamp"`
}

func Write(path string, m *Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0644)
}

func Read(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := &Manifest{}
	if err := json.Unmarshal(b, m); err != nil {
		return nil, err
	}
	return m, nil
}

func Exists(dirPath string) bool {
	_, err := os.Stat(filepath.Join(dirPath, ManifestFilename))
	return err == nil
}

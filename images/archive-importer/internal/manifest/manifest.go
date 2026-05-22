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
	// Deliveries records glovebox archive-delivery state per recognized
	// subtree. Optional; omitted when no delivery transport is wired up.
	// Schema bump to v1.1 when this field is non-empty.
	Deliveries []Delivery `json:"deliveries,omitempty"`
}

// Delivery is a single push attempt to glovebox's archive ingest
// endpoint (spec 13 / tus.io). One entry per recognized subtree.
type Delivery struct {
	// MatcherID identifies the recognizer-side subtree provenance,
	// e.g. "google-takeout/mail" or "meta-facebook/ads-information".
	MatcherID string `json:"matcher_id"`
	// MediaType is what we sent on the wire (one of glovebox's allow-
	// listed values: archive/mbox, archive/google-takeout-subtree,
	// archive/generic-tarball, archive/imap-export).
	MediaType string `json:"media_type"`
	// SourcePath is the local file/dir we delivered (absolute under
	// the unpacked dir). For raw deliveries it's a file; for tar
	// deliveries it's the directory whose contents we tarballed.
	SourcePath string `json:"source_path"`
	// ArchiveID is the upload's archive_id metadata sent to glovebox
	// (the idempotency key on their side).
	ArchiveID string `json:"archive_id"`
	// SHA256 + SizeBytes describe the upload body (for tar deliveries
	// these are the tarball's hash/size, NOT the directory's).
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
	// UploadURL is the Location glovebox assigned on POST.
	UploadURL string `json:"upload_url,omitempty"`
	// Status is one of "completed", "failed".
	Status string `json:"status"`
	// FailureReason carries the glovebox error code on Status=failed.
	FailureReason string `json:"failure_reason,omitempty"`
	DeliveredAt   string `json:"delivered_at"`
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

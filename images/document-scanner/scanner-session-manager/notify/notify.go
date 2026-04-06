// Package notify constructs and sends scan session notification events.
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/openclaw/scanner-session-manager/session"
)

// Event matches the notification-event.v1 schema.
type Event struct {
	SchemaVersion string   `json:"schema_version"`
	Source        string   `json:"source"`
	EventType     string   `json:"event_type"`
	Timestamp     string   `json:"timestamp"`
	OutputPath    string   `json:"output_path"`
	MediaType     string   `json:"media_type"`
	Metadata      Metadata `json:"metadata"`
	NodeName      string   `json:"node_name"`
}

// Metadata holds scan-specific fields.
type Metadata struct {
	Title        *string `json:"title"`
	Year         *int    `json:"year"`
	SourceDevice string  `json:"source_device"`
	DiscLabel    *string `json:"disc_label"`
	PageCount    *int    `json:"page_count"`
	SessionID    *string `json:"session_id"`
}

// BuildEvent constructs a notification event from a closed session.
func BuildEvent(sess *session.Session, device string) Event {
	pageCount := len(sess.Pages)
	sessionID := sess.ID

	var mediaType string
	switch sess.InputMethod {
	case session.InputADF:
		mediaType = "scan/adf-session"
	case session.InputFlatbed:
		mediaType = "scan/flatbed-session"
	default:
		mediaType = "scan/flatbed-session"
	}

	nodeName := os.Getenv("HOSTNAME")
	if nodeName == "" {
		nodeName = os.Getenv("NODE_NAME")
	}
	if nodeName == "" {
		nodeName = "unknown"
	}

	return Event{
		SchemaVersion: "1.0",
		Source:        "document-scanner",
		EventType:     "scan-session-complete",
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		OutputPath:    sess.OutputDir,
		MediaType:     mediaType,
		Metadata: Metadata{
			Title:        nil,
			Year:         nil,
			SourceDevice: device,
			DiscLabel:    nil,
			PageCount:    &pageCount,
			SessionID:    &sessionID,
		},
		NodeName: nodeName,
	}
}

// Send POSTs the event to the relay URL.
func Send(event Event, relayURL string) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	resp, err := http.Post(relayURL, "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("POST to relay: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("relay returned %d", resp.StatusCode)
	}
	return nil
}

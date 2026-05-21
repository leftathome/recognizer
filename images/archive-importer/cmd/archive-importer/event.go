package main

import (
	"os"
	"time"
)

// Event matches the v1.1 schema in schemas/notification-event.v1.1.schema.json.
type Event struct {
	SchemaVersion string         `json:"schema_version"`
	Source        string         `json:"source"`
	EventType     string         `json:"event_type"`
	EventID       string         `json:"event_id"`
	Timestamp     string         `json:"timestamp"`
	MediaType     string         `json:"media_type"`
	OutputPath    string         `json:"output_path"`
	Metadata      map[string]any `json:"metadata"`
	NodeName      string         `json:"node_name"`
}

// nodeName resolves the Kubernetes node name from the NODE_NAME env var
// (set by the chart via the downward API), falling back to the host's
// kernel hostname. Required by both v1.0 and v1.1 of the notification
// event schema.
func nodeName() string {
	if v := os.Getenv("NODE_NAME"); v != "" {
		return v
	}
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "unknown"
}

func newEvent(eventType, mediaType, outputPath, eventID string) Event {
	return Event{
		SchemaVersion: "1.1",
		Source:        "archive-recognizer",
		EventType:     eventType,
		EventID:       eventID,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		MediaType:     mediaType,
		OutputPath:    outputPath,
		Metadata:      map[string]any{},
		NodeName:      nodeName(),
	}
}

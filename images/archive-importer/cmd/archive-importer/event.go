package main

import "time"

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
	}
}

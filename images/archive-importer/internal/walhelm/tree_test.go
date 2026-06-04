package walhelmsrc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	walhelm "github.com/leftathome/walhelm-go"
)

// makeConversation builds a minimal *walhelm.Conversation with the given ID.
func makeConversation(id string) *walhelm.Conversation {
	return &walhelm.Conversation{
		ConversationSummary: walhelm.ConversationSummary{
			ID:      id,
			Subject: "subject-" + id,
		},
	}
}

// makeLabPanel builds a minimal walhelm.LabPanel with the given ID.
func makeLabPanel(id string) walhelm.LabPanel {
	return walhelm.LabPanel{
		ID:        id,
		Name:      "lab-" + id,
		OrderedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// makeMedicalRecord builds a minimal walhelm.MedicalRecord with the given ID.
func makeMedicalRecord(id string) walhelm.MedicalRecord {
	return walhelm.MedicalRecord{
		ID:    id,
		Title: "record-" + id,
	}
}

// TestWriteTree_NominalCase verifies that WriteTree writes exactly the expected
// files when given 2 conversations, 1 lab, and 1 record.
func TestWriteTree_NominalCase(t *testing.T) {
	root := t.TempDir()

	msgs := []*walhelm.Conversation{
		makeConversation("conv-001"),
		makeConversation("conv-002"),
	}
	labs := []walhelm.LabPanel{
		makeLabPanel("lab-001"),
	}
	recs := []walhelm.MedicalRecord{
		makeMedicalRecord("rec-001"),
	}

	count, err := WriteTree(root, msgs, labs, recs)
	if err != nil {
		t.Fatalf("WriteTree returned error: %v", err)
	}
	if count != 4 {
		t.Errorf("count = %d, want 4", count)
	}

	// Verify conversation files.
	for _, conv := range msgs {
		path := filepath.Join(root, "messages", conv.ID+".json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("reading conversation file %s: %v", path, err)
			continue
		}
		var got walhelm.Conversation
		if err := json.Unmarshal(data, &got); err != nil {
			t.Errorf("unmarshaling conversation file %s: %v", path, err)
			continue
		}
		if got.ID != conv.ID {
			t.Errorf("conversation ID = %q, want %q", got.ID, conv.ID)
		}
	}

	// Verify lab file.
	labPath := filepath.Join(root, "labs", "lab-001.json")
	labData, err := os.ReadFile(labPath)
	if err != nil {
		t.Errorf("reading lab file %s: %v", labPath, err)
	} else {
		var got walhelm.LabPanel
		if err := json.Unmarshal(labData, &got); err != nil {
			t.Errorf("unmarshaling lab file %s: %v", labPath, err)
		} else if got.ID != "lab-001" {
			t.Errorf("lab ID = %q, want %q", got.ID, "lab-001")
		}
	}

	// Verify record file.
	recPath := filepath.Join(root, "records", "rec-001.json")
	recData, err := os.ReadFile(recPath)
	if err != nil {
		t.Errorf("reading record file %s: %v", recPath, err)
	} else {
		var got walhelm.MedicalRecord
		if err := json.Unmarshal(recData, &got); err != nil {
			t.Errorf("unmarshaling record file %s: %v", recPath, err)
		} else if got.ID != "rec-001" {
			t.Errorf("record ID = %q, want %q", got.ID, "rec-001")
		}
	}
}

// TestWriteTree_EmptyInputs verifies that empty slices produce count 0 and no files.
func TestWriteTree_EmptyInputs(t *testing.T) {
	root := t.TempDir()

	count, err := WriteTree(root, nil, nil, nil)
	if err != nil {
		t.Fatalf("WriteTree returned error for empty inputs: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}

	// No subdirectories should be created.
	for _, sub := range []string{"messages", "labs", "records"} {
		p := filepath.Join(root, sub)
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("subdir %q should not exist, but stat returned: %v", sub, err)
		}
	}
}

// TestWriteTree_UnsafeIDs verifies that IDs containing "/" or ".." are sanitized
// so files land directly under the type subdir with no path traversal.
func TestWriteTree_UnsafeIDs(t *testing.T) {
	root := t.TempDir()

	slashID := "some/evil/id"
	dotdotID := "../escape"

	msgs := []*walhelm.Conversation{
		makeConversation(slashID),
		makeConversation(dotdotID),
	}

	count, err := WriteTree(root, msgs, nil, nil)
	if err != nil {
		t.Fatalf("WriteTree returned error: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}

	// Walk the messages subdir and confirm no files escaped.
	msgDir := filepath.Join(root, "messages")
	entries, err := os.ReadDir(msgDir)
	if err != nil {
		t.Fatalf("reading messages dir: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 files in messages dir, got %d", len(entries))
	}

	for _, e := range entries {
		// All entries must be plain files (not directories or symlinks).
		if e.IsDir() {
			t.Errorf("unexpected directory in messages dir: %s", e.Name())
		}
		// Confirm the file is a direct child of msgDir (no nested path).
		fullPath := filepath.Join(msgDir, e.Name())
		parent := filepath.Dir(fullPath)
		if parent != msgDir {
			t.Errorf("file %s escaped messages dir", fullPath)
		}
	}
}

// TestWriteTree_SafeName_EmptyID verifies that an empty ID does not produce
// a file called ".json" (the safeName fallback must emit a non-empty name).
func TestWriteTree_SafeName_EmptyID(t *testing.T) {
	root := t.TempDir()

	msgs := []*walhelm.Conversation{
		makeConversation(""),
	}

	count, err := WriteTree(root, msgs, nil, nil)
	if err != nil {
		t.Fatalf("WriteTree returned error for empty ID: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}

	msgDir := filepath.Join(root, "messages")
	entries, err := os.ReadDir(msgDir)
	if err != nil {
		t.Fatalf("reading messages dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 file in messages dir, got %d", len(entries))
	}
	name := entries[0].Name()
	// Must not be ".json" (empty stem).
	if name == ".json" {
		t.Errorf("filename = %q, must not be .json for empty ID", name)
	}
}

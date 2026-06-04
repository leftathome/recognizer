package walhelmsrc

import (
	"context"
	"testing"
	"time"

	walhelm "github.com/leftathome/walhelm-go"
)

// fakeWalhelmClient is a test double implementing WalhelmClient with canned data.
// It exists so downstream SP3 tasks (session/tree/state/fetch) can be driven
// without a live walhelm-go client.
type fakeWalhelmClient struct {
	folders       []walhelm.Folder
	conversations []walhelm.ConversationSummary
	conversation  *walhelm.Conversation
	labPanels     []walhelm.LabPanel
	records       []walhelm.MedicalRecord
	acctID        string
}

func (f *fakeWalhelmClient) GetFolders(ctx context.Context) ([]walhelm.Folder, error) {
	return f.folders, nil
}

func (f *fakeWalhelmClient) ListConversations(ctx context.Context, folderID string, since time.Time) ([]walhelm.ConversationSummary, error) {
	return f.conversations, nil
}

func (f *fakeWalhelmClient) GetConversation(ctx context.Context, id string) (*walhelm.Conversation, error) {
	return f.conversation, nil
}

func (f *fakeWalhelmClient) ListLabPanels(ctx context.Context, since time.Time) ([]walhelm.LabPanel, error) {
	return f.labPanels, nil
}

func (f *fakeWalhelmClient) ListRecords(ctx context.Context, since time.Time) ([]walhelm.MedicalRecord, error) {
	return f.records, nil
}

func (f *fakeWalhelmClient) AcqAccountID() string {
	return f.acctID
}

// Compile-time assertions that both the fake and the production wrapper satisfy
// the seam interface.
var (
	_ WalhelmClient = (*fakeWalhelmClient)(nil)
	_ WalhelmClient = (*realClient)(nil)
)

func TestFakeWalhelmClientReturnsCannedData(t *testing.T) {
	ctx := context.Background()
	fake := &fakeWalhelmClient{
		folders: []walhelm.Folder{
			{ID: "f1", Name: "Inbox", Count: 3, UnreadCount: 1},
		},
		conversations: []walhelm.ConversationSummary{
			{ID: "c1", FolderID: "f1", Subject: "Test thread"},
		},
		conversation: &walhelm.Conversation{
			ConversationSummary: walhelm.ConversationSummary{ID: "c1", Subject: "Test thread"},
			Messages: []walhelm.Message{
				{ID: "m1", Sender: "doctor", Body: "hello"},
			},
		},
		labPanels: []walhelm.LabPanel{
			{ID: "lp1", Name: "CMP", Status: "Final"},
		},
		records: []walhelm.MedicalRecord{
			{ID: "r1", Title: "Visit summary", Type: "note"},
		},
		acctID: "acct-123",
	}

	folders, err := fake.GetFolders(ctx)
	if err != nil {
		t.Fatalf("GetFolders error: %v", err)
	}
	if len(folders) != 1 || folders[0].ID != "f1" {
		t.Fatalf("GetFolders = %+v, want one folder with ID f1", folders)
	}

	convs, err := fake.ListConversations(ctx, "f1", time.Time{})
	if err != nil {
		t.Fatalf("ListConversations error: %v", err)
	}
	if len(convs) != 1 || convs[0].ID != "c1" {
		t.Fatalf("ListConversations = %+v, want one summary with ID c1", convs)
	}

	conv, err := fake.GetConversation(ctx, "c1")
	if err != nil {
		t.Fatalf("GetConversation error: %v", err)
	}
	if conv == nil || conv.ID != "c1" || len(conv.Messages) != 1 {
		t.Fatalf("GetConversation = %+v, want conversation c1 with one message", conv)
	}

	panels, err := fake.ListLabPanels(ctx, time.Now())
	if err != nil {
		t.Fatalf("ListLabPanels error: %v", err)
	}
	if len(panels) != 1 || panels[0].ID != "lp1" {
		t.Fatalf("ListLabPanels = %+v, want one panel with ID lp1", panels)
	}

	records, err := fake.ListRecords(ctx, time.Now())
	if err != nil {
		t.Fatalf("ListRecords error: %v", err)
	}
	if len(records) != 1 || records[0].ID != "r1" {
		t.Fatalf("ListRecords = %+v, want one record with ID r1", records)
	}

	if got := fake.AcqAccountID(); got != "acct-123" {
		t.Fatalf("AcqAccountID = %q, want acct-123", got)
	}
}

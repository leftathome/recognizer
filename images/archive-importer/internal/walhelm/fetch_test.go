package walhelmsrc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	walhelm "github.com/leftathome/walhelm-go"
)

// TestFetchHappyPath drives Fetch with one folder containing two conversations
// (each with a detail), one lab panel, and one record. It asserts the item
// count, that a tree directory was produced with the expected per-type files,
// and that the returned cursors fall within the wall-clock window of the call.
func TestFetchHappyPath(t *testing.T) {
	ctx := context.Background()
	fake := &fakeWalhelmClient{
		folders: []walhelm.Folder{
			{ID: "f1", Name: "Inbox"},
		},
		convsByFolder: map[string][]walhelm.ConversationSummary{
			"f1": {
				{ID: "c1", FolderID: "f1", Subject: "First"},
				{ID: "c2", FolderID: "f1", Subject: "Second"},
			},
		},
		convByID: map[string]*walhelm.Conversation{
			"c1": {
				ConversationSummary: walhelm.ConversationSummary{ID: "c1", Subject: "First"},
				Messages:            []walhelm.Message{{ID: "m1", Body: "hi"}},
			},
			"c2": {
				ConversationSummary: walhelm.ConversationSummary{ID: "c2", Subject: "Second"},
				Messages:            []walhelm.Message{{ID: "m2", Body: "yo"}},
			},
		},
		labPanels: []walhelm.LabPanel{
			{ID: "lp1", Name: "CMP", Status: "Final"},
		},
		records: []walhelm.MedicalRecord{
			{ID: "r1", Title: "Visit", Type: "note"},
		},
	}

	before := time.Now().UTC()
	treeDir, newState, items, err := Fetch(ctx, fake, State{})
	after := time.Now().UTC()
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}
	if treeDir != "" {
		defer os.RemoveAll(treeDir)
	}

	if items != 4 {
		t.Fatalf("items = %d, want 4", items)
	}
	if treeDir == "" {
		t.Fatal("treeDir is empty, want a non-empty temp dir")
	}

	// Sanity-check a couple of expected paths.
	wantPaths := []string{
		filepath.Join(treeDir, "messages", "c1.json"),
		filepath.Join(treeDir, "messages", "c2.json"),
		filepath.Join(treeDir, "labs", "lp1.json"),
		filepath.Join(treeDir, "records", "r1.json"),
	}
	for _, p := range wantPaths {
		if _, statErr := os.Stat(p); statErr != nil {
			t.Errorf("expected file %s: %v", p, statErr)
		}
	}

	// Cursors are time.Now()-based at fetch start, so they must fall within the
	// window [before, after].
	for name, ts := range map[string]time.Time{
		"MessagesSince": newState.MessagesSince,
		"LabsSince":     newState.LabsSince,
		"RecordsSince":  newState.RecordsSince,
	} {
		if ts.Before(before) || ts.After(after) {
			t.Errorf("%s = %v, want within [%v, %v]", name, ts, before, after)
		}
	}
}

// TestFetchSinceHonored asserts that the input State's per-type since cursors
// are passed through to the corresponding client calls unchanged.
func TestFetchSinceHonored(t *testing.T) {
	ctx := context.Background()
	t1 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	t2 := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	t3 := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

	fake := &fakeWalhelmClient{
		folders: []walhelm.Folder{{ID: "f1"}},
		convsByFolder: map[string][]walhelm.ConversationSummary{
			"f1": {{ID: "c1", FolderID: "f1"}},
		},
		convByID: map[string]*walhelm.Conversation{
			"c1": {ConversationSummary: walhelm.ConversationSummary{ID: "c1"}},
		},
		labPanels: []walhelm.LabPanel{{ID: "lp1"}},
		records:   []walhelm.MedicalRecord{{ID: "r1"}},
	}

	treeDir, _, _, err := Fetch(ctx, fake, State{
		MessagesSince: t1,
		LabsSince:     t2,
		RecordsSince:  t3,
	})
	if treeDir != "" {
		defer os.RemoveAll(treeDir)
	}
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}

	if !fake.sinceConversations.Equal(t1) {
		t.Errorf("ListConversations since = %v, want %v", fake.sinceConversations, t1)
	}
	if !fake.sinceLabs.Equal(t2) {
		t.Errorf("ListLabPanels since = %v, want %v", fake.sinceLabs, t2)
	}
	if !fake.sinceRecords.Equal(t3) {
		t.Errorf("ListRecords since = %v, want %v", fake.sinceRecords, t3)
	}
}

// TestFetchEmpty asserts that when there is nothing new across all three types,
// Fetch returns items 0, an empty treeDir, the input State unchanged, and no
// error so the caller can skip delivery without advancing cursors.
func TestFetchEmpty(t *testing.T) {
	ctx := context.Background()
	in := State{
		MessagesSince: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		LabsSince:     time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		RecordsSince:  time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC),
	}
	fake := &fakeWalhelmClient{} // no folders, convs, labs, or records

	treeDir, newState, items, err := Fetch(ctx, fake, in)
	if treeDir != "" {
		defer os.RemoveAll(treeDir)
	}
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}
	if items != 0 {
		t.Errorf("items = %d, want 0", items)
	}
	if treeDir != "" {
		t.Errorf("treeDir = %q, want empty", treeDir)
	}
	if newState != in {
		t.Errorf("newState = %+v, want unchanged %+v", newState, in)
	}
}

// TestFetchErrorPropagation asserts that each of the five client error paths
// surfaces the sentinel via errors.Is, returns treeDir=="", items==0, and
// leaves the input State unchanged. For convErr the fake must return at least
// one folder and one conversation summary so that GetConversation is reached.
func TestFetchErrorPropagation(t *testing.T) {
	ctx := context.Background()
	sentinel := errors.New("boom")

	// baseFolder and baseSummary are shared by the cases that need to reach a
	// later call in the pipeline (convsErr, convErr, labsErr, recordsErr).
	baseFolder := []walhelm.Folder{{ID: "f1"}}
	baseSummary := map[string][]walhelm.ConversationSummary{
		"f1": {{ID: "c1", FolderID: "f1"}},
	}
	baseConv := map[string]*walhelm.Conversation{
		"c1": {ConversationSummary: walhelm.ConversationSummary{ID: "c1"}},
	}

	cases := []struct {
		name string
		fake *fakeWalhelmClient
	}{
		{
			name: "foldersErr",
			fake: &fakeWalhelmClient{
				foldersErr: sentinel,
			},
		},
		{
			name: "convsErr",
			fake: &fakeWalhelmClient{
				folders:  baseFolder,
				convsErr: sentinel,
			},
		},
		{
			name: "convErr",
			// Must have a folder + a conversation summary so GetConversation is called.
			fake: &fakeWalhelmClient{
				folders:       baseFolder,
				convsByFolder: baseSummary,
				convErr:       sentinel,
			},
		},
		{
			name: "labsErr",
			fake: &fakeWalhelmClient{
				folders:       baseFolder,
				convsByFolder: baseSummary,
				convByID:      baseConv,
				labsErr:       sentinel,
			},
		},
		{
			name: "recordsErr",
			fake: &fakeWalhelmClient{
				folders:       baseFolder,
				convsByFolder: baseSummary,
				convByID:      baseConv,
				recordsErr:    sentinel,
			},
		},
	}

	inState := State{} // zero value; must be returned unchanged on error

	for _, tc := range cases {
		tc := tc // capture range var
		t.Run(tc.name, func(t *testing.T) {
			treeDir, newState, items, err := Fetch(ctx, tc.fake, inState)
			if treeDir != "" {
				defer os.RemoveAll(treeDir)
			}
			if err == nil {
				t.Fatal("Fetch error = nil, want non-nil")
			}
			if !errors.Is(err, sentinel) {
				t.Errorf("Fetch error = %v, want wrapping sentinel %v", err, sentinel)
			}
			if treeDir != "" {
				t.Errorf("treeDir = %q, want empty (no deliverable tree)", treeDir)
			}
			if items != 0 {
				t.Errorf("items = %d, want 0", items)
			}
			if newState != inState {
				t.Errorf("newState = %+v, want input state %+v unchanged", newState, inState)
			}
		})
	}
}

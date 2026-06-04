package walhelmsrc

import (
	"context"
	"fmt"
	"os"
	"time"

	walhelm "github.com/leftathome/walhelm-go"
)

// Fetch performs one incremental pull from a Walhelm account and materializes
// the new items into a temporary file tree.
//
// It enumerates folders and, for each, lists conversation summaries created
// after st.MessagesSince, fetching the full conversation for each summary. It
// then lists lab panels (since st.LabsSince) and medical records (since
// st.RecordsSince). The collected items are written via WriteTree into a fresh
// temp dir.
//
// Cursor choice: the new cursor for every type is the wall-clock time captured
// at the START of the fetch (start). This is the conservative option: it can
// re-deliver an item that was created during the run (at-least-once), but it
// never skips one. Using the maximum observed item timestamp instead would risk
// dropping items created after enumeration began but before the run completed.
//
// Fetch does NOT persist state and does NOT deliver. It returns newState for
// the caller to save only after a successful delivery (Task 7).
//
// Return contract:
//   - Empty fetch (zero conversations, labs, and records): treeDir == "",
//     newState == st (UNCHANGED so cursors do not advance), items == 0, err == nil.
//     The caller skips delivery.
//   - Non-empty fetch: treeDir is a fresh temp dir the caller owns (and must
//     clean up), newState carries start in all three cursors, items is the file
//     count.
//   - Any client error: err is returned (wrapped with the failing step) and no
//     deliverable tree is left behind (treeDir == "").
func Fetch(ctx context.Context, cli WalhelmClient, st State) (treeDir string, newState State, items int, err error) {
	// Capture the cursor up front (see cursor-choice note above).
	start := time.Now().UTC()

	// Messages: folders -> conversation summaries -> full conversations.
	folders, err := cli.GetFolders(ctx)
	if err != nil {
		return "", st, 0, fmt.Errorf("walhelm fetch: get folders: %w", err)
	}

	var msgs []*walhelm.Conversation
	for _, folder := range folders {
		summaries, lerr := cli.ListConversations(ctx, folder.ID, st.MessagesSince)
		if lerr != nil {
			return "", st, 0, fmt.Errorf("walhelm fetch: list conversations (folder %s): %w", folder.ID, lerr)
		}
		for _, summary := range summaries {
			full, gerr := cli.GetConversation(ctx, summary.ID)
			if gerr != nil {
				return "", st, 0, fmt.Errorf("walhelm fetch: get conversation %s: %w", summary.ID, gerr)
			}
			if full != nil {
				msgs = append(msgs, full)
			}
		}
	}

	// Labs.
	labs, err := cli.ListLabPanels(ctx, st.LabsSince)
	if err != nil {
		return "", st, 0, fmt.Errorf("walhelm fetch: list lab panels: %w", err)
	}

	// Records.
	recs, err := cli.ListRecords(ctx, st.RecordsSince)
	if err != nil {
		return "", st, 0, fmt.Errorf("walhelm fetch: list records: %w", err)
	}

	// Nothing new anywhere: skip delivery and leave cursors untouched.
	if len(msgs) == 0 && len(labs) == 0 && len(recs) == 0 {
		return "", st, 0, nil
	}

	dir, err := os.MkdirTemp("", "walhelm-tree-*")
	if err != nil {
		return "", st, 0, fmt.Errorf("walhelm fetch: create temp dir: %w", err)
	}

	count, err := WriteTree(dir, msgs, labs, recs)
	if err != nil {
		// Do not leave a partial tree the caller would deliver.
		_ = os.RemoveAll(dir)
		return "", st, 0, fmt.Errorf("walhelm fetch: write tree: %w", err)
	}

	newState = State{
		MessagesSince: start,
		LabsSince:     start,
		RecordsSince:  start,
	}
	return dir, newState, count, nil
}

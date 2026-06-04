package walhelmsrc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	walhelm "github.com/leftathome/walhelm-go"
)

// WriteTree serializes conversations, lab panels, and medical records into a
// per-item file tree rooted at root:
//
//	root/messages/<safe-id>.json   -- one per Conversation
//	root/labs/<safe-id>.json       -- one per LabPanel
//	root/records/<safe-id>.json    -- one per MedicalRecord
//
// Each file is indented JSON. count is the total number of files written.
// Subdirectories are created only when there is at least one item of that type.
// Empty inputs produce count 0 and no filesystem changes.
//
// Collision safety: two IDs that sanitize to the same base name are written as
// <base>.json, <base>-1.json, <base>-2.json, ... so no item is silently dropped.
// Each type subdir has its own independent collision counter.
func WriteTree(
	root string,
	msgs []*walhelm.Conversation,
	labs []walhelm.LabPanel,
	recs []walhelm.MedicalRecord,
) (count int, err error) {
	if len(msgs) > 0 {
		dir := filepath.Join(root, "messages")
		if err = os.MkdirAll(dir, 0o755); err != nil {
			return count, fmt.Errorf("WriteTree: mkdir messages: %w", err)
		}
		seen := make(map[string]int)
		for i, conv := range msgs {
			name := uniqueName(conv.ID, i, seen)
			data, merr := json.MarshalIndent(conv, "", "  ")
			if merr != nil {
				return count, fmt.Errorf("WriteTree: marshal conversation %q: %w", conv.ID, merr)
			}
			path := filepath.Join(dir, name+".json")
			if werr := os.WriteFile(path, data, 0o644); werr != nil {
				return count, fmt.Errorf("WriteTree: write %s: %w", path, werr)
			}
			count++
		}
	}

	if len(labs) > 0 {
		dir := filepath.Join(root, "labs")
		if err = os.MkdirAll(dir, 0o755); err != nil {
			return count, fmt.Errorf("WriteTree: mkdir labs: %w", err)
		}
		seen := make(map[string]int)
		for i, lab := range labs {
			name := uniqueName(lab.ID, i, seen)
			data, merr := json.MarshalIndent(lab, "", "  ")
			if merr != nil {
				return count, fmt.Errorf("WriteTree: marshal lab %q: %w", lab.ID, merr)
			}
			path := filepath.Join(dir, name+".json")
			if werr := os.WriteFile(path, data, 0o644); werr != nil {
				return count, fmt.Errorf("WriteTree: write %s: %w", path, werr)
			}
			count++
		}
	}

	if len(recs) > 0 {
		dir := filepath.Join(root, "records")
		if err = os.MkdirAll(dir, 0o755); err != nil {
			return count, fmt.Errorf("WriteTree: mkdir records: %w", err)
		}
		seen := make(map[string]int)
		for i, rec := range recs {
			name := uniqueName(rec.ID, i, seen)
			data, merr := json.MarshalIndent(rec, "", "  ")
			if merr != nil {
				return count, fmt.Errorf("WriteTree: marshal record %q: %w", rec.ID, merr)
			}
			path := filepath.Join(dir, name+".json")
			if werr := os.WriteFile(path, data, 0o644); werr != nil {
				return count, fmt.Errorf("WriteTree: write %s: %w", path, werr)
			}
			count++
		}
	}

	return count, nil
}

// uniqueName returns a collision-safe filename stem for an item. It computes
// the base safe name for the ID; if that base has already been used in the
// current subdir (tracked by seen), it appends "-<n>" where n is the running
// count for that base (starting at 1). The first occurrence is always the
// bare base with no suffix.
//
// seen maps each base to the number of times it has been assigned so far; the
// caller must pass a fresh map per type subdir and must not share it across
// subdirs.
func uniqueName(id string, fallbackIndex int, seen map[string]int) string {
	base := safeName(id, fallbackIndex)
	n := seen[base]
	seen[base] = n + 1
	if n == 0 {
		return base
	}
	return fmt.Sprintf("%s-%d", base, n)
}

// safeName converts an arbitrary ID string into a safe single-component
// filename stem. It:
//   - replaces any character that is not alphanumeric, hyphen, underscore, or
//     dot with "_" (this removes "/" and path separators),
//   - strips leading dots to avoid hidden-file or ".." confusion,
//   - falls back to a counter-based name when the result is empty.
//
// The result never contains a "/" and never equals ".." so it is safe to use
// as a filename directly under a known directory.
func safeName(id string, fallbackIndex int) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	s := strings.TrimLeft(b.String(), ".")
	if s == "" {
		return fmt.Sprintf("item-%d", fallbackIndex)
	}
	return s
}

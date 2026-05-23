package delivery

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/leftathome/recognizer/images/archive-importer/internal/manifest"
)

// Orchestrator drives end-to-end delivery of recognizer's recognized
// subtrees to glovebox. It owns the tar-or-raw decision per matcher
// media_type, the glovebox archive_id derivation, and the per-subtree
// retry+receipt loop.
type Orchestrator struct {
	Client  *Client
	TempDir string // where temp tarballs are written (and deleted)
}

// DeliverAll runs Deliver for every entry in subtrees, returning the
// resulting Delivery records (one per subtree, even failures).
// Idempotency on re-runs: pass existing manifest.Deliveries as
// alreadyDelivered; subtrees whose source_path matches an entry with
// Status=completed are skipped.
func (o *Orchestrator) DeliverAll(
	ctx context.Context,
	recogArchiveID, originalFilename string,
	subtrees []manifest.SubtreeRecognized,
	alreadyDelivered []manifest.Delivery,
) []manifest.Delivery {
	doneIdx := map[string]int{}
	out := make([]manifest.Delivery, 0, len(subtrees))
	for i, d := range alreadyDelivered {
		if d.Status == "completed" {
			doneIdx[d.SourcePath] = i
		}
	}
	for _, st := range subtrees {
		if i, ok := doneIdx[st.OutputPath]; ok {
			out = append(out, alreadyDelivered[i])
			continue
		}
		out = append(out, o.Deliver(ctx, recogArchiveID, originalFilename, st))
	}
	return out
}

// Deliver pushes a single recognized subtree to glovebox. Always
// returns a Delivery record; Status is "completed" on success or
// "failed" otherwise (FailureReason carries the glovebox error code
// or transport reason).
func (o *Orchestrator) Deliver(
	ctx context.Context,
	recogArchiveID, originalFilename string,
	st manifest.SubtreeRecognized,
) manifest.Delivery {
	now := func() string { return time.Now().UTC().Format(time.RFC3339) }
	d := manifest.Delivery{
		MatcherID:   recognizerMatcherID(st.MediaType),
		MediaType:   gloveboxMediaTypeFor(st.MediaType),
		SourcePath:  st.OutputPath,
		DeliveredAt: now(),
	}

	gid, err := gloveboxArchiveID(recogArchiveID, st.MediaType)
	if err != nil {
		d.Status = "failed"
		d.FailureReason = "archive_id: " + err.Error()
		return d
	}
	d.ArchiveID = gid

	// Build the body: either tar a directory or hash a raw file.
	info, err := os.Stat(st.OutputPath)
	if err != nil {
		d.Status = "failed"
		d.FailureReason = "stat: " + err.Error()
		return d
	}

	var bodyPath string
	var cleanup func()
	if info.IsDir() {
		tmpPath, hash, size, err := TarSubtree(st.OutputPath, o.TempDir)
		if err != nil {
			d.Status = "failed"
			d.FailureReason = "tar: " + err.Error()
			return d
		}
		bodyPath = tmpPath
		cleanup = func() { os.Remove(tmpPath) }
		d.SHA256 = hash
		d.SizeBytes = size
	} else {
		hash, size, err := HashFile(st.OutputPath)
		if err != nil {
			d.Status = "failed"
			d.FailureReason = "hash: " + err.Error()
			return d
		}
		bodyPath = st.OutputPath
		cleanup = func() {}
		d.SHA256 = hash
		d.SizeBytes = size
	}
	defer cleanup()

	item := Item{
		ArchiveID:       gid,
		ArchiveFilename: pickArchiveFilename(st, originalFilename, d.MediaType),
		MediaType:       d.MediaType,
		MatcherID:       d.MatcherID,
		SHA256:          d.SHA256,
		SizeBytes:       d.SizeBytes,
	}
	if d.MediaType == "archive/google-takeout-subtree" {
		item.SubtreeRelativePath = gtSubtreeRelativePath(st.OutputPath)
	}

	uploadURL, err := o.Client.Upload(ctx, item, bodyPath)
	d.UploadURL = uploadURL
	if err != nil {
		d.Status = "failed"
		d.FailureReason = err.Error()
		return d
	}
	d.Status = "completed"
	return d
}

// gloveboxMediaTypeFor maps recognizer's per-matcher media_type to
// glovebox's 4-value allow-list.
func gloveboxMediaTypeFor(recognizerMT string) string {
	switch recognizerMT {
	case "archive/google-takeout/mail":
		// The mail mbox is a raw file, not a dir, so it ships as
		// archive/mbox (not /google-takeout-subtree).
		return "archive/mbox"
	}
	if strings.HasPrefix(recognizerMT, "archive/google-takeout/") {
		return "archive/google-takeout-subtree"
	}
	if strings.HasPrefix(recognizerMT, "archive/meta-facebook/") {
		return "archive/generic-tarball"
	}
	return "archive/generic-tarball"
}

// recognizerMatcherID strips the leading "archive/" so the glovebox
// matcher_id is a clean correlation id like "google-takeout/mail" or
// "meta-facebook/ads-information".
func recognizerMatcherID(recognizerMT string) string {
	return strings.TrimPrefix(recognizerMT, "archive/")
}

var archiveIDChars = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

// gloveboxArchiveID derives a deterministic, format-compliant
// archive_id for glovebox by combining the recognizer-side archive_id's
// 8-char hash prefix with a sanitized matcher media_type.
//
// Example: recogArchiveID="5c2599d6-All mail Including Spam and
// Trash-002", recognizerMT="archive/google-takeout/mail" ->
// "5c2599d6-google-takeout-mail".
//
// Glovebox's format is ^[a-zA-Z0-9._-]{1,128}$; we sanitize and
// truncate to fit.
func gloveboxArchiveID(recogArchiveID, recognizerMT string) (string, error) {
	prefix, _, found := strings.Cut(recogArchiveID, "-")
	if !found || prefix == "" {
		return "", fmt.Errorf("recognizer archive_id has no '-' separator: %q", recogArchiveID)
	}
	suffix := strings.TrimPrefix(recognizerMT, "archive/")
	suffix = archiveIDChars.ReplaceAllString(suffix, "-")
	id := prefix + "-" + suffix
	if len(id) > 128 {
		id = id[:128]
	}
	return id, nil
}

// gtSubtreeRelativePath returns the relative path inside a Google
// Takeout archive (the "Mail" / "Calendar" / etc. dir name). The
// importer's output_path is .../unpacked/<archive_id>/Takeout/<svc>.
// We want just "<svc>" relative to Takeout/.
func gtSubtreeRelativePath(outputPath string) string {
	// Walk up until we find a parent named "Takeout"; return the
	// remainder. Fallback: just the basename.
	base := filepath.Base(outputPath)
	parent := filepath.Dir(outputPath)
	for parent != "/" && parent != "." {
		if filepath.Base(parent) == "Takeout" {
			rel, err := filepath.Rel(parent, outputPath)
			if err == nil {
				return filepath.ToSlash(rel)
			}
		}
		parent = filepath.Dir(parent)
	}
	return base
}

// pickArchiveFilename picks the archive_filename metadata value
// glovebox uses to name the file inside archives/<id>/. For raw mbox
// deliveries, use the original filename. For tarball deliveries, use a
// sanitized name derived from the subtree's basename + extension.
func pickArchiveFilename(st manifest.SubtreeRecognized, originalFilename, gloveMT string) string {
	if gloveMT == "archive/mbox" || gloveMT == "archive/imap-export" {
		// Sanitize: spaces -> "-", strip anything outside the allow-set.
		return sanitizeFilename(originalFilename)
	}
	base := sanitizeFilename(filepath.Base(st.OutputPath))
	return base + ".tar.gz"
}

var filenameUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func sanitizeFilename(s string) string {
	s = filenameUnsafe.ReplaceAllString(s, "-")
	if len(s) > 240 {
		s = s[:240]
	}
	return s
}


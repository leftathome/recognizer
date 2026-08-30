// Package delivery implements the recognizer-side client for glovebox's
// archive-delivery endpoint (tus.io v1.0.0). See the glovebox handoff
// doc at docs/handoffs/recognizer-archive-delivery.md in the glovebox
// repo for the wire contract.
package delivery

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// Item describes a single archive-delivery payload. Fields map 1:1
// onto glovebox's required Upload-Metadata keys (spec 13 §4.2).
type Item struct {
	// ArchiveID is glovebox's idempotency key: same ArchiveID + same
	// SHA256 from this source_id is a no-op replay (303 from POST).
	// Format ^[a-zA-Z0-9._-]{1,128}$.
	ArchiveID string
	// ArchiveFilename is what the final file gets named under
	// archives/<id>/. [A-Za-z0-9._-]+, no "..", <=256 B.
	ArchiveFilename string
	// MediaType is one of glovebox's allow-listed values; see
	// AllowedMediaTypes below. Anything else returns 400
	// unknown_media_type.
	MediaType string
	// MatcherID is a free-form correlation id we control. Format
	// ^[A-Za-z0-9._/-]{1,256}$. We use it to encode the recognizer-
	// side matcher provenance ("google-takeout/mail",
	// "meta-facebook/ads-information").
	MatcherID string
	// SubtreeRelativePath is only sent for media_type
	// archive/google-takeout-subtree (per spec 13 §4.2 conditional
	// required). UTF-8, no NUL, no C0 controls except \t, <=1024 B.
	SubtreeRelativePath string
	// SHA256 of the upload body, lowercase hex. Glovebox recomputes
	// during PATCH and verifies at finalize.
	SHA256 string
	// SizeBytes of the upload body. Must equal Upload-Length exactly;
	// mismatch is rejected at POST with size_mismatch.
	SizeBytes int64

	// Spec-15 provenance fields. Required only for archive/walhelm-export;
	// must be left zero for all other media types (they are not emitted on
	// the wire when empty, preserving byte-identical headers for existing
	// media types).

	// AcqProvider identifies the health-data service that was scraped
	// (e.g. "kp-wa"). Maps to Upload-Metadata key acq_provider.
	AcqProvider string
	// AcqAccountID is the end-user account identifier at the provider
	// (e.g. "leftathome"). Maps to Upload-Metadata key acq_account_id.
	AcqAccountID string
	// AcqAuthMethod describes how the session was authenticated
	// (e.g. "browser_session"). Maps to Upload-Metadata key acq_auth_method.
	AcqAuthMethod string
	// DataSubject is the URN of the data subject within the provider's
	// namespace (e.g. "walhelm:9f2a"). Maps to Upload-Metadata key
	// data_subject.
	DataSubject string
	// Audience is the ordered list of audience tokens that are permitted
	// to access the archive (e.g. ["subject", "guardians"]). Emitted as
	// a single comma-joined b64 value under the Upload-Metadata key
	// audience. Required (len > 0) for archive/walhelm-export.
	Audience []string
}

// AllowedMediaTypes are the media_type values glovebox accepts at the
// archive-delivery endpoint (spec 13 §4.3 allow-list). Anything else
// fails at POST with 400 unknown_media_type.
var AllowedMediaTypes = map[string]struct{}{
	"archive/mbox":                   {},
	"archive/google-takeout-subtree": {},
	"archive/imap-export":            {},
	"archive/generic-tarball":        {},
	"archive/walhelm-export":         {},
	// The document scanner's lane. Glovebox gates this media type
	// fail-closed to the authenticated source-id "recognizer-scanner"
	// (403 source_not_authorized from any other source), and its
	// finalize scans the bundled tree/ocr.txt -- a missing or
	// whitespace-only ocr.txt fails finalize with an opaque
	// 500 internal_finalize (see delivery.ErrFinalizeContent).
	"archive/recognizer-scan": {},
}

// Validate returns an error if the Item is malformed. We do the cheap
// client-side checks so a misconfigured matcher doesn't burn an HTTP
// round trip per subtree.
func (i Item) Validate(sourceID string) error {
	if i.ArchiveID == "" {
		return fmt.Errorf("ArchiveID is required")
	}
	if i.ArchiveFilename == "" {
		return fmt.Errorf("ArchiveFilename is required")
	}
	if _, ok := AllowedMediaTypes[i.MediaType]; !ok {
		return fmt.Errorf("MediaType %q is not on glovebox's allow-list", i.MediaType)
	}
	if i.MatcherID == "" {
		return fmt.Errorf("MatcherID is required")
	}
	if sourceID == "" {
		return fmt.Errorf("source_id is required")
	}
	if i.MediaType == "archive/google-takeout-subtree" && i.SubtreeRelativePath == "" {
		return fmt.Errorf("SubtreeRelativePath is required for archive/google-takeout-subtree")
	}
	if i.MediaType == "archive/walhelm-export" {
		if i.AcqProvider == "" {
			return fmt.Errorf("AcqProvider is required for archive/walhelm-export")
		}
		if i.AcqAccountID == "" {
			return fmt.Errorf("AcqAccountID is required for archive/walhelm-export")
		}
		if i.AcqAuthMethod == "" {
			return fmt.Errorf("AcqAuthMethod is required for archive/walhelm-export")
		}
		if i.DataSubject == "" {
			return fmt.Errorf("DataSubject is required for archive/walhelm-export")
		}
		if len(i.Audience) == 0 {
			return fmt.Errorf("Audience is required (len > 0) for archive/walhelm-export")
		}
	}
	if len(i.SHA256) != 64 {
		return fmt.Errorf("SHA256 must be 64 hex chars, got %d", len(i.SHA256))
	}
	if i.SizeBytes < 0 {
		return fmt.Errorf("SizeBytes must be >= 0")
	}
	return nil
}

// UploadMetadataHeader returns the Upload-Metadata header value for the
// item. Per spec 13 §4.2, values are base64-encoded with StdEncoding
// (NOT URL-safe), joined "key BASE64(value)" with commas.
//
// The reserved keys delivered_by and delivered_at are NOT emitted here;
// glovebox writes those server-side from the auth-resolved source_id
// and the wall-clock. Including them returns 400 metadata_reserved_key.
//
// sourceID is the recognizer's glovebox source-id (e.g.
// recognizer-smoke-test); it goes in the provider field per the
// handoff doc.
func (i Item) UploadMetadataHeader(sourceID string) string {
	pairs := []string{
		"archive_id " + b64(i.ArchiveID),
		"archive_filename " + b64(i.ArchiveFilename),
		"media_type " + b64(i.MediaType),
		"matcher_id " + b64(i.MatcherID),
		"provider " + b64(sourceID),
		"sha256 " + b64(i.SHA256),
		"size_bytes " + b64(strconv.FormatInt(i.SizeBytes, 10)),
	}
	if i.SubtreeRelativePath != "" {
		pairs = append(pairs, "subtree_relative_path "+b64(i.SubtreeRelativePath))
	}
	if i.AcqProvider != "" {
		pairs = append(pairs, "acq_provider "+b64(i.AcqProvider))
	}
	if i.AcqAccountID != "" {
		pairs = append(pairs, "acq_account_id "+b64(i.AcqAccountID))
	}
	if i.AcqAuthMethod != "" {
		pairs = append(pairs, "acq_auth_method "+b64(i.AcqAuthMethod))
	}
	if i.DataSubject != "" {
		pairs = append(pairs, "data_subject "+b64(i.DataSubject))
	}
	if len(i.Audience) > 0 {
		pairs = append(pairs, "audience "+b64(strings.Join(i.Audience, ",")))
	}
	return strings.Join(pairs, ",")
}

func b64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

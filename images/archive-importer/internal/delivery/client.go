package delivery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// DefaultChunkSize is the per-PATCH body size. The handoff doc
// recommends 16-64 MiB; 32 MiB balances memory pressure vs request
// overhead. Glovebox's idle timeout is 5 minutes per PATCH, so chunks
// must transfer faster than that.
const DefaultChunkSize int64 = 32 << 20

// Client speaks tus.io v1.0.0 to glovebox's /v1/archives endpoint.
type Client struct {
	BaseURL   string // e.g. http://glovebox.glovebox.svc.cluster.local:9091
	Token     string // bearer token from the Vault-projected Secret
	SourceID  string // recognizer-smoke-test, recognizer-v1, etc.
	HTTP      *http.Client
	ChunkSize int64
	// MaxRetries bounds the number of times we'll retry a 5xx/429.
	// Per chunk, not per upload.
	MaxRetries int
}

// NewClient returns a Client with sensible defaults. HTTP timeouts are
// generous to accommodate slow links + the 5-minute per-PATCH idle
// timeout on the server. Pass nil for httpClient to use the default.
//
// Auto-redirect is disabled: tus.io's 303 on a duplicate POST is a
// replay signal, not a "follow me" indication. Following it would hit
// a GET on the upload-state URL and lose our intended semantics.
func NewClient(baseURL, token, sourceID string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	if httpClient.CheckRedirect == nil {
		httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		Token:      token,
		SourceID:   sourceID,
		HTTP:       httpClient,
		ChunkSize:  DefaultChunkSize,
		MaxRetries: 3,
	}
}

// Upload runs the full tus.io lifecycle for one archive item against
// bodyPath: POST (or 303-replay resume), HEAD-resync on transient
// failures, ChunkSize-sized PATCH loop, completion when server's
// Upload-Offset equals item.SizeBytes. Returns the absolute upload URL
// on success.
//
// On a 303 reply to POST (glovebox sees an in-flight or finalized
// upload for the same archive_id+sha256+source_id), we HEAD the
// existing upload to discover the server's current offset and resume
// from there -- treating an already-complete upload as immediate
// success and a partial upload as a resumable continuation.
//
// On a PATCH error (network drop, 5xx after retries, 409
// offset_mismatch from a mid-chunk truncation), we HEAD the upload to
// re-sync our local offset against the server's authoritative
// Upload-Offset and continue from there.
func (c *Client) Upload(ctx context.Context, item Item, bodyPath string) (uploadURL string, err error) {
	if err := item.Validate(c.SourceID); err != nil {
		return "", fmt.Errorf("delivery: item validate: %w", err)
	}

	loc, offset, err := c.createOrResume(ctx, item)
	if err != nil {
		return "", err
	}
	if offset >= item.SizeBytes {
		// Server already has the full upload (303 replay of a
		// completed upload, or earlier-pod partial that filled all the
		// way before glovebox sent us back here). Nothing to do.
		return loc, nil
	}

	f, err := os.Open(bodyPath)
	if err != nil {
		return "", fmt.Errorf("delivery: open body: %w", err)
	}
	defer f.Close()

	for offset < item.SizeBytes {
		end := offset + c.ChunkSize
		if end > item.SizeBytes {
			end = item.SizeBytes
		}
		newOffset, err := c.patchChunk(ctx, loc, f, offset, end-offset)
		if err != nil {
			// Try a HEAD-resync before failing the whole upload. The
			// server is the authoritative source of truth on how much
			// it accepted; our local view may be stale after a
			// transport blip or a 409 offset_mismatch.
			srvOffset, hErr := c.head(ctx, loc)
			if hErr == nil && srvOffset > offset {
				offset = srvOffset
				continue
			}
			return "", err
		}
		offset = newOffset
	}
	return loc, nil
}

// createOrResume POSTs a new upload (offset starts at 0) or, on a 303
// replay, HEADs the existing upload to discover where to resume from.
// Returned offset is the start position for the PATCH loop.
func (c *Client) createOrResume(ctx context.Context, item Item) (uploadURL string, offset int64, err error) {
	loc, replay, err := c.create(ctx, item)
	if err != nil {
		return "", 0, err
	}
	if !replay {
		return loc, 0, nil
	}
	srvOffset, err := c.head(ctx, loc)
	if err != nil {
		return "", 0, err
	}
	return loc, srvOffset, nil
}

// head probes the upload state and returns the server's current
// Upload-Offset. Used both after a 303 replay (to find where to
// resume) and after a PATCH failure (to re-sync against the
// authoritative server-side offset).
func (c *Client) head(ctx context.Context, uploadURL string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, "HEAD", uploadURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, fmt.Errorf("HEAD %s: %w", uploadURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HEAD %s: HTTP %d", uploadURL, resp.StatusCode)
	}
	off := resp.Header.Get("Upload-Offset")
	n, perr := strconv.ParseInt(off, 10, 64)
	if perr != nil {
		return 0, fmt.Errorf("parse Upload-Offset %q: %w", off, perr)
	}
	return n, nil
}

// create POSTs to /v1/archives. Returns the absolute upload URL.
func (c *Client) create(ctx context.Context, item Item) (uploadURL string, replay bool, err error) {
	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/v1/archives", nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Upload-Length", strconv.FormatInt(item.SizeBytes, 10))
	req.Header.Set("Upload-Metadata", item.UploadMetadataHeader(c.SourceID))

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("delivery POST: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	switch resp.StatusCode {
	case http.StatusCreated:
		// 201 + Location relative or absolute. Resolve against BaseURL.
		return c.resolve(resp.Header.Get("Location"))
	case http.StatusSeeOther:
		// 303 same archive_id+sha256 replay.
		loc, _, _ := c.resolve(resp.Header.Get("Location"))
		return loc, true, nil
	default:
		return "", false, &HTTPError{
			Op: "POST /v1/archives", Status: resp.StatusCode, Body: string(body),
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}
}

func (c *Client) resolve(loc string) (string, bool, error) {
	if loc == "" {
		return "", false, errors.New("server returned no Location header")
	}
	u, err := url.Parse(loc)
	if err != nil {
		return "", false, fmt.Errorf("parse Location: %w", err)
	}
	if u.IsAbs() {
		return u.String(), false, nil
	}
	return c.BaseURL + loc, false, nil
}

// patchChunk sends one PATCH and returns the new server offset.
func (c *Client) patchChunk(ctx context.Context, uploadURL string, f *os.File, offset, length int64) (int64, error) {
	var lastErr error
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return 0, fmt.Errorf("seek: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, "PATCH", uploadURL, io.LimitReader(f, length))
		if err != nil {
			return 0, err
		}
		req.Header.Set("Tus-Resumable", "1.0.0")
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("Content-Type", "application/offset+octet-stream")
		req.Header.Set("Upload-Offset", strconv.FormatInt(offset, 10))
		req.ContentLength = length

		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = err
			backoff(attempt)
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()

		switch {
		case resp.StatusCode == http.StatusNoContent:
			// 204 success; new offset in header.
			off := resp.Header.Get("Upload-Offset")
			n, perr := strconv.ParseInt(off, 10, 64)
			if perr != nil {
				return 0, fmt.Errorf("parse Upload-Offset %q: %w", off, perr)
			}
			return n, nil
		case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
			lastErr = &HTTPError{
				Op: "PATCH " + uploadURL, Status: resp.StatusCode, Body: string(body),
				RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
			}
			backoff(attempt)
		default:
			// 4xx non-retryable.
			return 0, &HTTPError{Op: "PATCH " + uploadURL, Status: resp.StatusCode, Body: string(body)}
		}
	}
	return 0, fmt.Errorf("delivery PATCH exhausted retries: %w", lastErr)
}

func backoff(attempt int) {
	d := time.Duration(1<<attempt) * time.Second
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	time.Sleep(d)
}

func parseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if n, err := strconv.Atoi(h); err == nil {
		return time.Duration(n) * time.Second
	}
	return 0
}

// HTTPError carries glovebox's response detail for non-2xx outcomes.
// The handoff doc enumerates the error codes glovebox returns; the
// Body field contains the JSON {"error":"<code>","message":"..."} when
// present, otherwise raw text.
type HTTPError struct {
	Op         string
	Status     int
	Body       string
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s: HTTP %d: %s", e.Op, e.Status, e.Body)
}

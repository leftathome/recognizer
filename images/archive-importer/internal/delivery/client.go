package delivery

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
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

// Environment variables shared by both cmd/archive-importer and
// cmd/walhelm-fetch. Keeping the names here (rather than re-declared
// per-binary) is what lets both flag-parsers and the chart agree on a
// single source of truth.
const (
	// EnvToken is the static bearer token fallback, used only when
	// EnvTokenFile is unset. Read once; a rotated value requires a pod
	// restart to take effect (Vault ExternalSecret only refreshes the
	// backing Secret, not already-injected env).
	EnvToken = "GLOVEBOX_INGEST_TOKEN"
	// EnvTokenFile points at a file containing the bearer token. When
	// set, it takes precedence over EnvToken and is re-read (and
	// trimmed) on every request, so Vault rotation propagates within
	// the ~1-6 minute window the handoff doc documents -- no restart,
	// and no token sitting in /proc/<pid>/environ.
	EnvTokenFile = "GLOVEBOX_INGEST_TOKEN_FILE"
	// EnvCAFile optionally points at a PEM bundle of additional trusted
	// CAs for the glovebox TLS connection (private CA pinning).
	EnvCAFile = "GLOVEBOX_INGEST_CA_FILE"
	// EnvRequireTLS, when truthy ("true"/"1"), refuses to construct a
	// client against an http:// base URL. Off by default because
	// glovebox's bearer listener is plaintext today; this is forward
	// wiring for the day it isn't (PHI must not transit in the clear).
	EnvRequireTLS = "GLOVEBOX_INGEST_REQUIRE_TLS"
)

// DefaultChunkSize is the per-PATCH body size. The handoff doc
// recommends 16-64 MiB; 32 MiB balances memory pressure vs request
// overhead. Glovebox's idle timeout is 5 minutes per PATCH, so chunks
// must transfer faster than that.
const DefaultChunkSize int64 = 32 << 20

// TokenSource returns the current bearer token. It is called once per
// outbound request (POST/HEAD/PATCH), never cached across requests, so a
// file-backed implementation naturally picks up Vault rotation without a
// restart. Returning an error fails that request with a clear cause
// instead of sending a stale or empty Authorization header.
type TokenSource func() (string, error)

// Client speaks tus.io v1.0.0 to glovebox's /v1/archives endpoint.
type Client struct {
	BaseURL string // e.g. http://glovebox.glovebox.svc.cluster.local:9091
	// Token is a static bearer token, used as a fallback when
	// TokenSource is nil. Prefer TokenSource (via ResolveTokenSource)
	// for anything that must survive Vault rotation.
	Token       string
	TokenSource TokenSource
	SourceID    string // recognizer-smoke-test, recognizer-v1, etc.
	HTTP        *http.Client
	ChunkSize   int64
	// MaxRetries bounds the number of times we'll retry a 5xx/429.
	// Per chunk, not per upload.
	MaxRetries int
}

// token resolves the bearer token for one outbound request: TokenSource
// wins when set (re-invoked every call, so file rotation is picked up
// per request); otherwise falls back to the static Token field. Neither
// set is a configuration error, surfaced to the caller rather than sent
// as an empty/absent Authorization header.
func (c *Client) token() (string, error) {
	if c.TokenSource != nil {
		tok, err := c.TokenSource()
		if err != nil {
			return "", fmt.Errorf("delivery: resolve bearer token: %w", err)
		}
		if tok == "" {
			return "", errors.New("delivery: TokenSource returned an empty token")
		}
		return tok, nil
	}
	if c.Token == "" {
		return "", errors.New("delivery: no bearer token configured (set Token or TokenSource)")
	}
	return c.Token, nil
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

// ResolveTokenSource implements the GLOVEBOX_INGEST_TOKEN_FILE /
// GLOVEBOX_INGEST_TOKEN precedence shared by both cmd/archive-importer
// and cmd/walhelm-fetch: tokenFile, when non-empty, wins and is re-read
// (and trimmed) on every call -- this is what lets Vault rotation land
// within the handoff doc's ~1-6 minute window without a pod restart, and
// keeps the token out of /proc/<pid>/environ. tokenFile empty falls back
// to the static token. Both empty is a configuration error, returned
// here (at construction time) rather than deferred to the first request.
func ResolveTokenSource(tokenFile, token string) (TokenSource, error) {
	if tokenFile != "" {
		return func() (string, error) {
			b, err := os.ReadFile(tokenFile)
			if err != nil {
				return "", fmt.Errorf("read token file %s: %w", tokenFile, err)
			}
			tok := strings.TrimSpace(string(b))
			if tok == "" {
				return "", fmt.Errorf("token file %s is empty", tokenFile)
			}
			return tok, nil
		}, nil
	}
	if token != "" {
		static := token
		return func() (string, error) { return static, nil }, nil
	}
	return nil, fmt.Errorf("delivery: neither %s nor %s is set", EnvTokenFile, EnvToken)
}

// ClientConfig groups every construction-time option pulled from
// flags/env by both binaries: identity, token/rotation, and TLS trust.
// NewClientFromConfig is the single place that turns these into a ready
// Client (or a config error) -- both cmd/archive-importer and
// cmd/walhelm-fetch should build their Client through it so a change
// here (a new env var, a stricter check) reaches both with one edit.
type ClientConfig struct {
	BaseURL  string
	SourceID string
	// Token is the static bearer token fallback (GLOVEBOX_INGEST_TOKEN).
	Token string
	// TokenFile, when set, is re-read per request (GLOVEBOX_INGEST_TOKEN_FILE).
	TokenFile string
	// CAFile, when set, is a PEM bundle of additional trusted CAs
	// (GLOVEBOX_INGEST_CA_FILE) loaded into the transport's RootCAs.
	CAFile string
	// RequireTLS refuses to build a client against an http:// BaseURL
	// (GLOVEBOX_INGEST_REQUIRE_TLS).
	RequireTLS bool
	// HTTPClient is optional; nil yields NewClient's default.
	HTTPClient *http.Client
}

// NewClientFromConfig builds a Client from ClientConfig, applying the
// RequireTLS gate, the token/token-file resolution, and (when CAFile is
// set) a custom Transport pinning that CA -- the single construction
// path shared by cmd/archive-importer and cmd/walhelm-fetch.
func NewClientFromConfig(cfg ClientConfig) (*Client, error) {
	if cfg.RequireTLS {
		u, err := url.Parse(cfg.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("delivery: parse base URL %q: %w", cfg.BaseURL, err)
		}
		if u.Scheme != "https" {
			return nil, fmt.Errorf(
				"delivery: %s is set but base URL %q is not https:// -- glovebox archive payloads may carry PHI and must not transit in plaintext",
				EnvRequireTLS, cfg.BaseURL)
		}
	}

	ts, err := ResolveTokenSource(cfg.TokenFile, cfg.Token)
	if err != nil {
		return nil, err
	}

	httpClient := cfg.HTTPClient
	if cfg.CAFile != "" {
		transport, terr := transportWithCA(cfg.CAFile)
		if terr != nil {
			return nil, terr
		}
		if httpClient == nil {
			httpClient = &http.Client{}
		} else {
			// Copy so we don't mutate a client the caller may reuse
			// elsewhere with different TLS expectations.
			cp := *httpClient
			httpClient = &cp
		}
		httpClient.Transport = transport
	}

	c := NewClient(cfg.BaseURL, "", cfg.SourceID, httpClient)
	c.TokenSource = ts
	return c, nil
}

// transportWithCA builds an *http.Transport trusting the system roots
// plus the PEM bundle at caFile, with TLS 1.2 as the floor (1.3 is
// preferred automatically since Go negotiates the highest mutually
// supported version).
func transportWithCA(caFile string) (*http.Transport, error) {
	pemBytes, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("delivery: read CA file %s: %w", caFile, err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("delivery: no certificates parsed from CA file %s", caFile)
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.TLSClientConfig = &tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
	}
	return base, nil
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
	tok, err := c.token()
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
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
	tok, err := c.token()
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
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

// ErrFinalizeContent marks a finalize that failed for a reason retrying will
// never fix -- glovebox v0.8.0 returns an opaque `500 internal_finalize` for
// client-caused finalize failures, in practice a missing or whitespace-only
// `ocr.txt` at the tar root of an archive/recognizer-scan tarball
// (ErrScanMissingOCR server-side). Retrying re-runs a full server-side untar
// and scan of a multi-GB tarball that will never succeed, so we stop at the
// first one. Recovery is to fix the tarball and re-POST: a failed finalize
// publishes nothing and cleans up, so the same archive_id returns 201, not
// 409. Upstream ergonomics tracked as glovebox#71.
var ErrFinalizeContent = errors.New("delivery: finalize failed for content reasons (not retryable)")

// isFinalizeContentError reports whether a 5xx body carries glovebox's
// `internal_finalize` code. The body is the documented {"error":"...",
// "message":"..."} shape; anything undecodable is treated as retryable,
// since a truncated or proxy-generated 5xx really is transient.
func isFinalizeContentError(body []byte) bool {
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		return false
	}
	return e.Error == "internal_finalize"
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
		tok, terr := c.token()
		if terr != nil {
			return 0, terr
		}
		req.Header.Set("Authorization", "Bearer "+tok)
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
		case resp.StatusCode >= 500 && isFinalizeContentError(body):
			// Content bug, not backpressure: fail now rather than burning
			// MaxRetries re-uploading the final chunk into the same verdict.
			return 0, fmt.Errorf("%w: %s", ErrFinalizeContent,
				(&HTTPError{Op: "PATCH " + uploadURL, Status: resp.StatusCode, Body: string(body)}).Error())
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

# Walhelm Health Source -- Design Specification

**Version 0.1 -- June 2026** (implemented; messages/labs/records — record-download zip + proxy deferred per §2.2/§11)

*This document specifies a new content **source** for the recognizer (archive-importer): a fetcher that pulls Kaiser Permanente Washington health data (secure messages, lab results, medical records) via the [walhelm-go](https://github.com/leftathome/walhelm-go) library and delivers it to Glovebox as an `archive/walhelm-export` archive carrying producer-asserted provenance (acquisition identity + opaque subject principal + audience), per Glovebox spec 15. Unlike the existing Google Takeout / Meta sources -- which unpack and classify an archive file already on disk -- the walhelm source FETCHES from a live API and synthesizes the directory tree itself, then reuses the existing spec-13 delivery client to ship it. This is SP3 of the "health connector" program; it depends on the Glovebox SP1 contract (spec 15, now on Glovebox `main`) and on walhelm-go's existing fixture-backed read API. The live record-download (zip) and proxy/multi-patient capabilities are out of scope here (blocked on live-portal reverse-engineering); the session is acquired out-of-band and injected.*

---

## 1. Purpose

The recognizer already delivers archives to Glovebox (`internal/delivery`, a complete tus.io `/v1/archives` client). What it lacks is a way to ingest **health data**, which (a) is fetched from a live API rather than handed over as a file, and (b) must carry the per-record provenance Glovebox spec 15 requires so the scanner can resolve "whose data is this" and route/quarantine accordingly.

This spec adds a `walhelm` source that:
1. loads an out-of-band-acquired KP session,
2. fetches messages / labs / records via walhelm-go,
3. writes them as per-item files into a directory tree,
4. delivers that tree to Glovebox as `archive/walhelm-export` with the three provenance facts spec 15 defines,

and a minimal, additive extension of the delivery layer to carry that provenance.

## 2. Scope

### 2.1 In Scope
- A new fetch command (`cmd/walhelm-fetch`) in the `archive-importer` Go module, reusing `internal/delivery`.
- A `WalhelmClient` interface over walhelm-go's read API (messages, labs, records) so the fetcher is unit-testable against a fake (no live KP).
- Synthesizing a directory tree (`messages/`, `labs/`, `records/`) of per-item files and packaging + delivering it as one `archive/walhelm-export` per fetch.
- **Additive** extension of `internal/delivery`: `Item` gains `AcqProvider`/`AcqAccountID`/`AcqAuthMethod`/`DataSubject`/`Audience`; `UploadMetadataHeader` emits them when set; `archive/walhelm-export` added to `AllowedMediaTypes`; `Validate` requires the provenance fields for that media type.
- Out-of-band **session injection** (a session file mounted from a Vault-synced Secret) consumed via walhelm-go `WithSession`/`ImportSession`.
- Per-data-type **incremental `since` cursors** persisted to the PVC.
- Config, secrets, and Helm wiring (parallel to the existing `gloveboxIngest` block).
- Fixture-backed tests (fake `WalhelmClient`, fake Glovebox tus.io server -- the latter already exists in `internal/delivery/client_test.go`).

### 2.2 Out of Scope (Deferred)
- **Live record-download (the zip)** -- a **v0.1 scope choice, NOT a library gap.** walhelm-go's `DownloadRecord(ctx, recordID, w)` IS implemented (records.go:172-213, "verified live 2026-06-02": queue -> poll released-records -> stream the ZIP). v0.1 fetches record **metadata** (`ListRecords`) only and writes `records/<id>.json`; wiring `DownloadRecord` to also write `records/<id>.zip` (staged as `application/zip` per spec 15 §6) is a small, fixture-testable later increment. It is deferred here only to keep the first delivery minimal -- see §11 decision #6.
- **Proxy / multi-patient** -- walhelm-go is single-member in v0.1; multi-subject fetching needs live `ProxySwitch` capture. v0.1 emits exactly one subject (the signed-in member). The contract (one subject principal per archive, spec 15 §4.3) already accommodates multi-subject later via one-archive-per-subject.
- **In-cluster session minting** (browser automation / MFA / browserless) -- the session is acquired out-of-band by the operator (`walhelm login`) and injected. Automating that in-cluster is a separate, unsolved design problem (see §11).
- **Message attachments** -- walhelm-go has no attachment download in v0.1.
- **Live KP integration testing** -- all tests here are fixture-backed; a live smoke is a separate, operator-run step.

## 3. Architecture

```
  operator (out-of-band)                   recognizer (this spec)                 Glovebox (spec 15, done)
  ----------------------                   ----------------------                 -----------------------
  walhelm login  --->  session.json  --->  [Vault -> ESO -> Secret -> mount]
  (browser+MFA)                                     |
                                                    v
                                            cmd/walhelm-fetch
                                            1. ImportSession(session file)
                                            2. WalhelmClient.Fetch{Messages,Labs,Records}(since)
                                            3. write tree: messages/ labs/ records/  (per-item files)
                                            4. delivery.Orchestrator -> TarSubtree -> Client.Upload
                                               media_type=archive/walhelm-export
                                               Upload-Metadata: acq_* + data_subject + audience  ----> POST /v1/archives
                                            5. persist since-cursors
```

The walhelm source does **not** use the `matcher.Provider` / `SubtreeMatcher` machinery (that classifies unknown archives on disk). It produces a known-structure tree directly from the API and hands it to the delivery layer.

### 3.1 One archive per fetch (single subject, v0.1)
A fetch run produces **one** `archive/walhelm-export` whose tree contains up to three top-level subdirs -- `messages/`, `labs/`, `records/` -- and carries one `data_subject` (the member) and one `audience` (`["subject"]`, the member's own data per spec 15 §9). Glovebox's walhelm importer derives a per-file rule key (`walhelm:<top-subdir>`) so the operator can still route messages vs labs vs records to different destination agents from a single archive. Multi-subject (proxy) later = one archive per subject (spec 15 §4.3); no contract change needed.

## 4. Provenance mapping (recognizer -> spec 15 `Upload-Metadata`)

| spec-15 wire key | walhelm source value | Source |
|---|---|---|
| `acq_provider` | `kp-wa` | constant |
| `acq_account_id` | the member's KP user id | `Session.UserID` |
| `acq_auth_method` | `browser_session` | constant (matches the v1 enum) |
| `data_subject` | `walhelm:<subject-id>` (opaque principal, spec 15 decision #2) | configured/derived; v0.1 = the member |
| `audience` | `subject` | spec 15 §9 (member's own data) |
| `media_type` | `archive/walhelm-export` | constant |

The **subject principal is opaque** (`walhelm:<stable-id>`, never a human name); Glovebox's registry maps it to an `entity_id`. The recognizer holds the source-id -> principal correspondence (a small config map); v0.1 maps the single member's KP id to one principal. The acquisition account (`acq_account_id`) and the subject principal collapse onto the member in v0.1 and diverge under proxy (v0.2).

## 5. Delivery-layer extension (additive)

In `internal/delivery/metadata.go`:
- `Item` gains: `AcqProvider, AcqAccountID, AcqAuthMethod, DataSubject string` and `Audience []string`.
- `AllowedMediaTypes` gains `"archive/walhelm-export": {}`.
- `UploadMetadataHeader` emits `acq_provider`/`acq_account_id`/`acq_auth_method`/`data_subject` when non-empty and `audience` (comma-joined) when non-empty -- **only when set**, so existing Takeout/Meta deliveries are byte-identical on the wire. `delivered_by`/`delivered_at` remain unemitted (server-set).
- `Validate` requires `AcqProvider`/`AcqAccountID`/`AcqAuthMethod`/`DataSubject` (non-empty) and a valid `Audience` when `MediaType == "archive/walhelm-export"`; for the four existing media types nothing changes.

This mirrors the Glovebox-side SP1 metadata extension exactly (same keys, same "required only for walhelm-export" gating), so the two ends agree.

## 6. The walhelm fetcher

### 6.1 `WalhelmClient` interface (testability seam)
This is the **recognizer's own** abstraction (not walhelm-go's API verbatim) so the fetcher is unit-testable against a fake. It takes a plain `since time.Time`; the production wrapper adapts each call to the library's real variadic-option API.
```go
type WalhelmClient interface {
    GetFolders(ctx context.Context) ([]walhelm.Folder, error)
    ListConversations(ctx context.Context, folderID string, since time.Time) ([]walhelm.ConversationSummary, error)
    GetConversation(ctx context.Context, id string) (*walhelm.Conversation, error)
    ListLabPanels(ctx context.Context, since time.Time) ([]walhelm.LabPanel, error)
    ListRecords(ctx context.Context, since time.Time) ([]walhelm.MedicalRecord, error)
    AcqAccountID() string // the member's KP user id (acq_account_id)
}
```
**Library-adaptation notes (production wrapper):**
- The real walhelm-go signatures are variadic options: `ListConversations(ctx, folderID string, opts ...ListOption)`, `ListLabPanels(ctx, opts ...ListOption)`, `ListRecords(ctx, opts ...ListOption)` (messages.go:167, labs.go:94, records.go:57). The wrapper translates this interface's `since time.Time` into `walhelm.WithSince(since)` (a `ListOption`); a zero `since` passes no option (full pull).
- `GetFolders(ctx) ([]walhelm.Folder, error)` is real (messages.go:91); the wrapper passes it through.
- There is NO `Client.UserID()` method. The wrapper returns `client.ExportSession().UserID` from `AcqAccountID()` (`ExportSession()` is real, client.go:129; `Session.UserID` is the member's KP id).
- The client is built with `walhelm.WithSession(importedSession)`.

Tests supply a fake implementing this interface, returning fixtures.

### 6.2 Tree layout (per-item files)
- `messages/<conversation-id>.json` -- one file per conversation (summary + messages), `content_type` → `application/json` on the Glovebox side. (An `.eml` rendering is a possible later refinement; JSON is faithful to walhelm-go's structs.)
- `labs/<panel-id>.json` -- one file per lab panel (components, ranges, flags).
- `records/<record-id>.json` -- one file per record's **metadata** (the downloadable document is deferred).

Files are written under a temp dir; the existing `delivery.TarSubtree` tars the tree (rejects symlinks, computes sha256). The Glovebox walhelm importer stages one item per file.

### 6.3 Fetch loop
1. `ImportSession` from the session file; if invalid/expired → exit code "session" (operator refreshes out-of-band).
2. Load per-type `since` cursors (§8).
3. For each type, list since the cursor; for messages, fetch each conversation's detail; write per-item files.
4. If nothing new across all types → exit 0 without delivering (no empty archives).
5. Package + deliver via `delivery.Orchestrator`/`Client` with the provenance `Item`.
6. On delivery success, advance the cursors and persist.

## 7. Session acquisition (out-of-band) + secrets
- The operator runs `walhelm login` (browser + MFA) on their workstation, producing a session (cookies). That session JSON is stored in Vault (e.g. `secret/walhelm/session/<member>`), projected by ESO into a K8s Secret, and mounted read-only at `WALHELM_SESSION_FILE` (e.g. `/etc/walhelm/session.json`).
- The fetcher reads the file and `ImportSession`s it. It never performs login itself.
- Session expiry (walhelm-go returns `ErrCodeSessionExpired`) → distinct non-zero exit code + a clear log line directing the operator to re-login and refresh the Vault entry. (Automating refresh in-cluster is the deferred browserless problem, §11.)
- The Glovebox ingest bearer token + source-id reuse the **existing** `gloveboxIngest` config/secret (the delivery client already consumes them).

## 8. Incremental state
- A per-subject state file on the shared PVC (e.g. `/data/walhelm/state-<subject>.json`): `{ "messages_since": RFC3339, "labs_since": RFC3339, "records_since": RFC3339 }`.
- Cursors advance **only after** a successful delivery (at-least-once; the Glovebox side dedups/quarantines, and re-delivery of an overlapping window is safe -- archives are content-addressed by sha256 + the importer is idempotent per archive).
- Absent/corrupt state → full fetch (since zero) with a warning.

## 9. Config + deployment
New config (flags + env, mirroring `flags.go`):
- `WALHELM_SESSION_FILE` (path, required for the walhelm command)
- `WALHELM_SUBJECT_PRINCIPAL` (the `walhelm:<id>` to stamp; required)
- `WALHELM_STATE_DIR` (default `/data/walhelm`)
- reuse `GLOVEBOX_INGEST_URL` / `_TOKEN` / `_SOURCE_ID`.

Helm (`charts/recognizer`): a `walhelmSource` values block (enabled, subjectPrincipal, sessionSecret ref, schedule) + an `ExternalSecret` projecting the walhelm session from Vault + a CronJob/Job for `walhelm-fetch` (parallel to the archive-importer CronJob; suspended by default). The `openclaw-recognizer` namespace label + glovebox NetworkPolicy already permit delivery.

## 10. Testing strategy (fixture-backed; no live KP)
- **Delivery extension**: `UploadMetadataHeader` emits the provenance keys for walhelm-export and omits them for other media types; `Validate` requires them for walhelm-export only; `archive/walhelm-export` accepted. Round-trip via the existing fake tus.io server (`client_test.go`).
- **Fetcher**: a fake `WalhelmClient` returns fixture conversations/labs/records; assert the written tree (per-item files, correct paths/content-types), the `since` filtering, the empty-fetch no-op, and the persisted-cursor advance-only-on-success.
- **End-to-end (fixture)**: fake client → fetch → tree → real `delivery.Orchestrator` → fake Glovebox server; assert the POST carried `media_type=archive/walhelm-export` + `acq_*` + `data_subject` + `audience`, and the tar body contains the expected per-item files.
- `go test ./... -race`, `go vet` clean.

## 11. Decisions (resolved 2026-06-03)

Decision **#6 (record-download)**: **metadata-only in v0.1**; `records/<id>.json` only. The zip (`DownloadRecord` -> `records/<id>.zip`) is a v0.1.1 fast-follow. Decisions **#1-#5**: the **proposed defaults are adopted** (new `cmd/walhelm-fetch` in the `archive-importer` module reusing `internal/delivery`; enumerate message folders via `GetFolders`; JSON message files; one-shot CronJob suspended by default; session-expiry exit-and-alert). They remain recorded below as the rationale.

### Open-decision rationale (for reference)
1. **Command home**: a new `cmd/walhelm-fetch` in the `archive-importer` module reusing `internal/delivery` (proposed -- lightest, shares the proven client), vs a separate `images/walhelm-fetch` module/image. Proposed: same module, new command, separate image build target.
2. **Folder enumeration for messages**: fetch all folders via `GetFolders` and pull each, vs a fixed default (inbox/conversations). Proposed: enumerate via `GetFolders`, fetch the conversation folders, skip empties.
3. **Message file format**: JSON (faithful to walhelm-go structs, proposed) vs `.eml`/RFC822 rendering (closer to the mbox path, enables the email enrichers). Proposed: JSON for v0.1; revisit `.eml` if the enrichers add value for messages.
4. **Run shape**: one-shot Job (operator/cron-triggered, matches the archive-importer pattern, proposed) vs long-running poller. Proposed: one-shot + CronJob, suspended by default.
5. **Session-expiry handling**: exit-and-alert (proposed) for v0.1; in-cluster auto-refresh (browserless) is explicitly deferred.
6. **Record-download (zip) inclusion**: walhelm-go's `DownloadRecord` is implemented and verified, so the zip is now buildable + fixture-testable (a fake returns a fixture zip; the real wrapper calls `DownloadRecord`). v0.1 proposes **metadata-only** (`records/<id>.json`) to keep the first delivery minimal, with `records/<id>.zip` (content_type `application/zip`) as a fast follow. **Open for the operator**: include the zip in v0.1, or defer to v0.1.1? (The earlier deferral assumed the library couldn't do it; that's no longer true -- the only remaining gate on exercising it for real is a live KP session, which is needed for any live run regardless.)

## 12. Acceptance Criteria
1. `internal/delivery` carries the five provenance fields, emits them on the wire only for `archive/walhelm-export`, validates them for that media type, and leaves the four existing media types byte-identical (verified by test).
2. A `WalhelmClient` interface + a production walhelm-go-backed implementation + a fake for tests.
3. `cmd/walhelm-fetch` fetches messages/labs/records since the persisted cursors, writes the per-item tree, and delivers one `archive/walhelm-export` with the correct provenance, advancing cursors only on success.
4. Empty fetch delivers nothing and exits 0; session-expiry exits with a distinct code + actionable log.
5. Fixture-backed tests cover the delivery extension, the fetcher, and the end-to-end fetch→deliver path; `go test -race` + `go vet` clean.
6. Helm wiring (values block, ExternalSecret for the session, suspended CronJob) renders (`helm lint`/`template`).
7. Record-download (zip) and proxy remain cleanly stubbed/absent with no half-built surface; their deferral is documented.

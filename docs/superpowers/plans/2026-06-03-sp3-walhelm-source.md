# SP3: Walhelm Health Source — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `walhelm` source to the recognizer that fetches KP messages/labs/records via walhelm-go and delivers them to Glovebox as one `archive/walhelm-export` carrying spec-15 provenance, reusing the existing tus.io delivery client.

**Architecture:** A new `cmd/walhelm-fetch` in the `archive-importer` Go module. It loads an injected KP session, fetches via a `WalhelmClient` seam (real impl wraps walhelm-go; fake for tests), writes per-item files into a `messages/`/`labs/`/`records/` tree, then tars + delivers via the existing `internal/delivery` `Client.Upload` + `TarSubtree` — extended additively to carry `acq_*`/`data_subject`/`audience` and the new media type. Session is acquired out-of-band; record-download zip + proxy are deferred (spec §2.2, §11). Spec: `docs/specs/04-walhelm-health-source.md`.

**Tech Stack:** Go 1.26 (module `github.com/leftathome/recognizer/images/archive-importer`); walhelm-go (`github.com/leftathome/walhelm-go`, private — see Task 2 dep note); tus.io delivery; Helm; GitLab CI (kaniko). All work in worktree `/root/.config/superpowers/worktrees/recognizer/walhelm-source` (branch `feat/walhelm-source`), module dir `images/archive-importer`.

**Baseline gate (do first):** from `images/archive-importer`, `go build ./... && go test ./... -count=1` — record result; if red at baseline, STOP and report. **Note:** the moment walhelm-go is added to `go.mod` (Task 2), `go build ./...` only succeeds once the dependency recipe (Task 2 step 0) is in place — resolve the dependency BEFORE the first build so the gate stays meaningful.

**Conventions:** paths relative to the worktree root. No emoji in Go. `go vet` + `go test -race` the touched packages before each commit. Commit per task with the message shown. Mirror existing recognizer idioms (read the neighbor file before writing).

---

## File Structure

**New**
- `images/archive-importer/internal/walhelm/client.go` — `WalhelmClient` interface + production wrapper over walhelm-go (`since time.Time` → `walhelm.WithSince`; `AcqAccountID()` → `ExportSession().UserID`).
- `images/archive-importer/internal/walhelm/session.go` — load a `walhelm.Session` from `WALHELM_SESSION_FILE`; build a `*walhelm.Client` via `WithSession`.
- `images/archive-importer/internal/walhelm/tree.go` — synthesize the `messages/`/`labs/`/`records/` per-item file tree from fetched data.
- `images/archive-importer/internal/walhelm/state.go` — per-subject `since`-cursor load/save.
- `images/archive-importer/internal/walhelm/fetch.go` — the fetch→tree→deliver orchestration.
- `images/archive-importer/internal/walhelm/*_test.go` — fake `WalhelmClient` + fixtures for each of the above.
- `images/archive-importer/cmd/walhelm-fetch/main.go`, `flags.go` — thin entrypoint: build the real client + call `walhelm.RunOnce` (the testable orchestration func lives in `internal/walhelm/`).
- `images/archive-importer/cmd/walhelm-fetch/main_test.go` — config/exit-code only (real binary; can't reach KP). The fake-client e2e lives with `RunOnce` in `internal/walhelm/`.
- `images/archive-importer/Dockerfile.walhelm-fetch` — second top-level Dockerfile (mirrors `images/archive-importer/Dockerfile`; builds `./cmd/walhelm-fetch`). There is NO per-cmd Dockerfile in this repo.
- `charts/recognizer/templates/walhelm-fetch/{cronjob.yaml,configmap.yaml,externalsecret-walhelm-session.yaml,externalsecret-glovebox-token.yaml,serviceaccount.yaml}`

**Modified**
- `images/archive-importer/internal/delivery/metadata.go` — `Item` provenance fields; `AllowedMediaTypes`; `UploadMetadataHeader`; `Validate`.
- `images/archive-importer/internal/delivery/metadata_test.go`
- `images/archive-importer/go.mod` / `go.sum` — add walhelm-go.
- `.gitlab-ci.yml` — build/test for walhelm-fetch.
- `charts/recognizer/values.yaml` — `walhelmSource` block.

---

## Phase A — Delivery-layer provenance extension

### Task 1: Carry spec-15 provenance through the delivery `Item`
**Files:** Modify `internal/delivery/metadata.go`; test `internal/delivery/metadata_test.go`.

Context (verified): `Item` (metadata.go:14-..), `AllowedMediaTypes` (4 entries), `Validate(sourceID)` (per-media-type checks incl. the google-takeout-subtree conditional), `UploadMetadataHeader(sourceID)` (emits archive_id/archive_filename/media_type/matcher_id/provider/sha256/size_bytes [+subtree_relative_path]; `b64` helper). This must EXACTLY match Glovebox spec 15 §4.2's wire keys.

- [ ] **Step 1 (TDD):** add failing tests:
  - `UploadMetadataHeader` for an `archive/walhelm-export` item with `AcqProvider/AcqAccountID/AcqAuthMethod/DataSubject` set + `Audience=["subject"]` emits `acq_provider`,`acq_account_id`,`acq_auth_method`,`data_subject` (b64) and `audience` (b64 of the comma-joined tokens) — assert by decoding the header.
  - For a non-walhelm item (e.g. `archive/mbox`) with those fields empty, the header is byte-identical to today (no acq_*/data_subject/audience keys).
  - `Validate`: `archive/walhelm-export` requires non-empty `AcqProvider/AcqAccountID/AcqAuthMethod/DataSubject` and a non-empty valid `Audience` → returns error when any missing; passes when all present. `archive/walhelm-export` is accepted by the allow-list. Existing media types unaffected.
- [ ] **Step 2:** `go test ./internal/delivery/ -run 'Metadata|Validate' -v` → FAIL.
- [ ] **Step 3:** implement:
  - Add to `Item`: `AcqProvider, AcqAccountID, AcqAuthMethod, DataSubject string` and `Audience []string`.
  - `AllowedMediaTypes["archive/walhelm-export"] = struct{}{}`.
  - In `UploadMetadataHeader`, after the existing pairs, append `acq_provider`/`acq_account_id`/`acq_auth_method`/`data_subject` via `b64` **when non-empty**, and `audience` as `b64(strings.Join(i.Audience, ","))` when `len>0`. (Keep `delivered_by`/`delivered_at` unemitted.)
  - In `Validate`, add: `if i.MediaType == "archive/walhelm-export" { require AcqProvider, AcqAccountID, AcqAuthMethod, DataSubject non-empty; require len(Audience)>0 }`. (Mirror the existing `fmt.Errorf` style. Audience-token validity is the producer's responsibility / Glovebox re-validates; a non-empty check here is sufficient — do NOT import Glovebox's validator.)
- [ ] **Step 4:** `go test ./internal/delivery/ -v` → PASS; `go vet ./internal/delivery/`.
- [ ] **Step 5:** commit `feat(delivery): carry spec-15 provenance (acq_*/data_subject/audience) for archive/walhelm-export`.

---

## Phase B — Walhelm client, session, tree, state, fetch

### Task 2: `WalhelmClient` interface + production wrapper + dependency
**Files:** Create `internal/walhelm/client.go`, `client_test.go`; modify `go.mod`/`go.sum`.

> **Step 0 — resolve the walhelm-go dependency FIRST (do before any code/build).** walhelm-go's module path is `github.com/leftathome/walhelm-go` but its only remote is `https://gitlab.orac.local/steve/walhelm-go.git` (vanity-path/host mismatch). Plain `go get` will NOT resolve it. Use the `insteadOf` rewrite (committable; works locally + in CI):
> ```sh
> # GOPRIVATE must match the MODULE PATH (github.com/leftathome/...), NOT the host — verified 2026-06-03.
> export GOPRIVATE='github.com/leftathome/*'
> git config --global url."https://gitlab.orac.local/steve/walhelm-go.git".insteadOf "https://github.com/leftathome/walhelm-go"
> cd images/archive-importer && go get github.com/leftathome/walhelm-go@v0.1.0-rc.1   # pinned tag, verified to resolve+build
> ```
> This writes a normal `require` (NO `replace` committed). **Verified working in this worktree** (downloads from gitlab via insteadOf, skips proxy/sumdb because GOPRIVATE matches the module path; v0.1.0-rc.1 is a real tag). Verify `go build ./...` resolves it. (Setting `GOPRIVATE=gitlab.orac.local` — the host — does NOT work: go keys proxy/sumdb decisions off the module path, so it tried the public sumdb and failed.)
> - **Fallback if the rewrite can't be made to work in this environment:** add a TEMPORARY local replace with an ABSOLUTE path — `replace github.com/leftathome/walhelm-go => /mnt/c/Users/steve/Code/walhelm-go` — to build/test locally, but DO NOT COMMIT the replace (it's machine-specific and breaks kaniko, whose build context is the recognizer repo, not `/mnt/c`). Note clearly which path you took.
> - Pin a specific commit. CI (Task 10) replicates the GOPRIVATE + `insteadOf` (with a CI token in the URL).
> - If you genuinely cannot resolve the module, STOP and report (don't fake the types).

- [ ] **Step 1 (TDD):** define the interface (spec §6.1) and write a test that a `fakeWalhelmClient` satisfies it and returns canned `walhelm.ConversationSummary`/`Conversation`/`LabPanel`/`MedicalRecord`/`Folder` (import the real types from walhelm-go).
- [ ] **Step 2:** test FAILs to compile (no interface yet).
- [ ] **Step 3:** implement the interface exactly as spec §6.1:
  ```go
  type WalhelmClient interface {
      GetFolders(ctx context.Context) ([]walhelm.Folder, error)
      ListConversations(ctx context.Context, folderID string, since time.Time) ([]walhelm.ConversationSummary, error)
      GetConversation(ctx context.Context, id string) (*walhelm.Conversation, error)
      ListLabPanels(ctx context.Context, since time.Time) ([]walhelm.LabPanel, error)
      ListRecords(ctx context.Context, since time.Time) ([]walhelm.MedicalRecord, error)
      AcqAccountID() string
  }
  ```
  Production `realClient struct { c *walhelm.Client }`: each method adapts `since` → variadic `walhelm.WithSince(since)` (omit the option when `since.IsZero()`); `AcqAccountID()` returns `c.ExportSession().UserID`. (Verified real APIs: `ListConversations(ctx, folderID, ...ListOption)`, `ListLabPanels(ctx, ...ListOption)`, `ListRecords(ctx, ...ListOption)`, `GetFolders(ctx)`, `ExportSession()`, `WithSince`.)
- [ ] **Step 4:** `go build ./... && go test ./internal/walhelm/ -v`; `go vet`.
- [ ] **Step 5:** commit `feat(walhelm): WalhelmClient seam over walhelm-go (since→WithSince, acct via ExportSession)`.

### Task 3: Session loading
**Files:** `internal/walhelm/session.go`, `session_test.go`.
- [ ] **Step 1 (TDD):** `LoadSession(path string) (*walhelm.Session, error)` — reads a JSON session file, unmarshals to `walhelm.Session`; error on missing/malformed. `NewClientFromSession(*walhelm.Session) (WalhelmClient, error)` builds the real client via `walhelm.NewClient(walhelm.WithSession(s))` wrapped in `realClient`. Test with a fixture session JSON (a minimal valid `walhelm.Session` — check `session.go` for required fields). The fixture MUST set a FUTURE `expires_at` so `Session.IsValid()` returns true (else the session-expired path fires in tests); also add a test with a PAST `expires_at` to exercise the expired branch. Test missing-file + malformed-file errors.
- [ ] **Step 2-4:** fail → implement → pass; `go vet`.
- [ ] **Step 5:** commit `feat(walhelm): load injected session file -> walhelm client`.

### Task 4: Tree synthesis
**Files:** `internal/walhelm/tree.go`, `tree_test.go`.
- [ ] **Step 1 (TDD):** `WriteTree(root string, msgs []*walhelm.Conversation, labs []walhelm.LabPanel, recs []walhelm.MedicalRecord) (count int, err error)` writes `root/messages/<conv-id>.json`, `root/labs/<panel-id>.json`, `root/records/<rec-id>.json` (each `json.MarshalIndent` of the item). Test: given fixtures, the expected files exist at the expected paths with parseable JSON; ids are filename-sanitized (reuse/mirror `sanitizeFilename` from delivery.go if exported, else a local sanitizer — no `/`, no `..`); empty inputs create no files / return count 0; `count` = total files.
- [ ] **Step 2-4:** fail → implement → pass; `go vet`.
- [ ] **Step 5:** commit `feat(walhelm): synthesize messages/labs/records per-item file tree`.

### Task 5: Incremental `since`-cursor state
**Files:** `internal/walhelm/state.go`, `state_test.go`.
- [ ] **Step 1 (TDD):** `type State struct { MessagesSince, LabsSince, RecordsSince time.Time }` with `LoadState(dir, subject string) (State, error)` (absent/corrupt → zero State + no fatal error, log-worthy) and `SaveState(dir, subject string, s State) error` (atomic write). Test round-trip, absent→zero, corrupt→zero.
- [ ] **Step 2-4:** fail → implement → pass; `go vet`.
- [ ] **Step 5:** commit `feat(walhelm): per-subject since-cursor state (advance only on delivery success)`.

### Task 6: Fetch orchestration
**Files:** `internal/walhelm/fetch.go`, `fetch_test.go`.
- [x] **Step 1 (TDD):** `Fetch(ctx, cli WalhelmClient, st State) (treeDir string, newState State, items int, err error)`:
  - enumerate folders via `GetFolders`; for each conversation folder, `ListConversations(folderID, st.MessagesSince)` then `GetConversation` per summary.
  - `ListLabPanels(st.LabsSince)`, `ListRecords(st.RecordsSince)`.
  - `WriteTree` into a fresh temp dir.
  - cursor choice: `time.Now().UTC()` captured at fetch START (documented in fetch.go) — conservative/at-least-once, never skips items created mid-run.
  - return `items==0` when nothing new (caller skips delivery); on empty fetch newState == input st (cursors do NOT advance).
  Test with a fake client: since-filtering is honored (fake records the `since` it received), tree written, `items` correct, empty fetch → items 0 + no tree, error path (ListLabPanels err) → items 0 + no tree + wrapped err.
- [x] **Step 2-4:** fail → implement → pass; `go vet`; `go test -race`.
- [x] **Step 5:** commit `feat(walhelm): fetch loop (folders->conversations, labs, records) -> tree`.

---

## Phase C — Command + end-to-end delivery

### Task 7: `cmd/walhelm-fetch` entrypoint
**Files:** `cmd/walhelm-fetch/main.go`, `flags.go`. Mirror `cmd/archive-importer/{main.go,flags.go}` for flags/exit-code style.

> **Structure for testability (ties to Task 8):** put the fetch→tar→build-Item→Upload→SaveState orchestration in `internal/walhelm.RunOnce(ctx context.Context, cli WalhelmClient, cfg RunConfig) error` (takes the client + delivery params as a struct). `main()` parses flags, loads the session, builds the REAL client, and calls `RunOnce`. This is what lets Task 8 test the whole path with a fake client + fake glovebox server without a process boundary.
- [ ] Flags/env: `WALHELM_SESSION_FILE` (req), `WALHELM_SUBJECT_PRINCIPAL` (req, the `walhelm:<id>`), `WALHELM_STATE_DIR` (default `/data/walhelm`), reuse `GLOVEBOX_INGEST_URL`/`_TOKEN`/`_SOURCE_ID`, `-log-level`, `-dry-run`. Validate required flags → exit 2 on config error.
- [ ] Wire: LoadSession → NewClientFromSession → LoadState → `walhelm.Fetch` → if `items==0` exit 0 → else `path, sha256, size, err := delivery.TarSubtree(treeDir, os.TempDir())` (TWO args; returns path+sha256hex+size) → build provenance `Item{MediaType:"archive/walhelm-export", ArchiveID: <derive>, ArchiveFilename:"walhelm-export.tar", MatcherID:"walhelm/export", SHA256, SizeBytes, AcqProvider:"kp-wa", AcqAccountID: cli.AcqAccountID(), AcqAuthMethod:"browser_session", DataSubject: subjectPrincipal, Audience:["subject"]}` → `delivery.NewClient(url,token,sourceID,httpClient).Upload(ctx, item, tarPath)` → on success `SaveState(newState)`.
  - ArchiveID derivation: stable + idempotent-friendly, e.g. `walhelm-<subject-sanitized>-<fetchdate>` within `^[a-zA-Z0-9._-]{1,128}$` (no per-run randomness that breaks the sha256 idempotency — but since content varies per fetch, a date/seq is fine).
- [ ] Exit codes: 0 success/no-op; 2 config; distinct code for session-expired (`walhelm.IsSessionExpired(err)`) with an actionable log; 1 generic; a delivery-failure code. Document them.
- [ ] `go build ./cmd/walhelm-fetch/` succeeds; `go vet`.
- [ ] Commit `feat(walhelm-fetch): command wiring session->fetch->deliver with provenance`.

### Task 8: End-to-end fixture test
**Files:** the e2e test belongs with the ORCHESTRATION function (e.g. `internal/walhelm/deliver_test.go` or a `cmd/walhelm-fetch` test that calls the orchestration func directly), NOT the binary harness.

> **Decision (not optional):** `cmd/archive-importer/main_test.go` drives a COMPILED BINARY via `exec.Command` — there is NO seam to inject a Go fake into `main()`. So do not try to mirror that harness for the fake-client e2e (dead end). Instead: extract the fetch→tar→deliver orchestration into a testable function (e.g. `func RunOnce(ctx, cli WalhelmClient, cfg) error` in `internal/walhelm/`), and `main()` is a thin wrapper that builds the real client and calls it. Test `RunOnce` with the **fake `WalhelmClient`** + a **real `delivery.Client`** pointed at the **fake glovebox tus.io server** (reuse the `fakeGlovebox` pattern from `internal/delivery/client_test.go`). Keep a SEPARATE, smaller `main_test.go` that exercises only the config/exit-code paths through the real binary (missing flags → exit 2; it cannot reach KP, so don't assert delivery there).

- [ ] Stand up the fake glovebox tus.io server (POST/HEAD/PATCH) and call `RunOnce` with the fake `WalhelmClient`. Assert: the POST carried `media_type=archive/walhelm-export`, `acq_provider=kp-wa`, `acq_account_id`, `acq_auth_method=browser_session`, `data_subject=<principal>`, `audience=subject` (decode Upload-Metadata); the PATCH body is a tar containing `messages/…`, `labs/…`, `records/…` files; cursors advanced only after the 204 finalize. Add: session-expired fake (`walhelm.IsSessionExpired`) → distinct return + no delivery + cursors unchanged.
- [ ] `go test ./cmd/walhelm-fetch/ -v -race`; `go vet`.
- [ ] Commit `test(walhelm-fetch): e2e fixture (fake KP -> tree -> real delivery -> fake glovebox)`.

---

## Phase D — Packaging + deployment

### Task 9: Dockerfile
- [ ] Create `images/archive-importer/Dockerfile.walhelm-fetch` by mirroring the REAL `images/archive-importer/Dockerfile` (a single module-level multi-stage build; there is NO `cmd/*/Dockerfile`). Change only the build target to `./cmd/walhelm-fetch` and the output binary name. Distroless, nonroot, CGO disabled. The build stage's `go mod download` must resolve walhelm-go via the GOPRIVATE+`insteadOf`+token recipe (Task 2 step 0) — pass the token as a build secret/arg, NOT a committed credential. Build target compiles (`go build ./cmd/walhelm-fetch`; `docker build -f images/archive-importer/Dockerfile.walhelm-fetch` if docker + dep-token available — note which ran). Commit `chore(walhelm-fetch): distroless Dockerfile.walhelm-fetch`.

### Task 10: CI
**File:** `.gitlab-ci.yml`.
- [ ] Add a kaniko image-build job for `walhelm-fetch` mirroring the `archive-importer` build job. Ensure the existing `test:go:archive-importer` job covers `./...` (it should, since walhelm packages are in the same module — confirm). Add `GOPRIVATE=gitlab.orac.local` + the CI token wiring needed to `go get` walhelm-go (mirror any existing private-module handling; if none exists, document the new requirement clearly). Validate YAML parses. Commit `ci: build walhelm-fetch image; GOPRIVATE for walhelm-go`.

### Task 11: Helm
**Files:** `charts/recognizer/templates/walhelm-fetch/*`, `charts/recognizer/values.yaml`.
- [ ] `values.yaml`: a `walhelmSource` block (`enabled: false`, `subjectPrincipal`, `schedule`, `sessionSecret` ref, reuse `gloveboxIngest`). Templates mirroring `archive-importer/`: a suspended `CronJob` running `walhelm-fetch`, a `configmap` (non-secret env), an `externalsecret-walhelm-session.yaml` (Vault `secret/walhelm/session/<member>` → projected to a Secret mounted at `WALHELM_SESSION_FILE`) — mirror `charts/recognizer/templates/archive-importer/externalsecret-glovebox-token.yaml` (the closest existing pattern) for the ESO shape; reuse the same glovebox-token ExternalSecret for the ingest bearer token; a serviceaccount. Gate all on `.Values.walhelmSource.enabled`.
- [ ] `helm lint charts/recognizer` + `helm template` render (gated off by default → no walhelm objects unless enabled; also template with it enabled to verify). Commit `feat(chart): walhelm-fetch CronJob + session ExternalSecret (suspended, opt-in)`.

---

## Phase E — Close-out
### Task 12
- [ ] From `images/archive-importer`: `go test ./... -race`, `go vet ./...`, `staticcheck ./...` (note pre-existing findings, if any, separately). `helm lint charts/recognizer`.
- [ ] Update `docs/specs/04-walhelm-health-source.md` status if needed; add a CHANGELOG entry if the repo keeps one.
- [ ] Commit `docs: SP3 walhelm-source close-out`.

---

## Notes for the implementer
- **Reuse `Client.Upload` + `TarSubtree` directly** for the walhelm path; the `Orchestrator.Deliver` is Takeout/manifest-specific (`manifest.SubtreeRecognized`) and is NOT the right seam here.
- **The delivery extension is additive** — existing Takeout/Meta deliveries must stay byte-identical on the wire (Task 1's non-walhelm test is the guardrail).
- **`WalhelmClient` is the recognizer's own seam**, intentionally taking `since time.Time`; only the production wrapper touches walhelm-go's variadic-option API. Keep walhelm-go imports confined to `internal/walhelm/{client,session}.go`.
- **No live KP**: every test uses the fake client + the fake glovebox server. The real session + live run are operator steps.
- **Deferred, keep stub-free:** no `DownloadRecord`/zip wiring, no proxy/multi-patient — don't half-build them (spec §2.2, §11 #6).

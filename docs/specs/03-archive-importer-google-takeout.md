# Archive Importer for Google Takeout -- Design Specification

**Version 1.0 -- May 2026**
**Beads:** archiver-ubc

*This document specifies the first concrete implementation of the archive importer pattern from spec 01. It introduces a workload called `archive-importer` that takes a finished Google Takeout zip, unpacks it, identifies which Google service each subtree belongs to, and emits per-subtree notification events that downstream consumers (glovebox, future Immich integration, etc.) subscribe to. It also defines the **consumer-facing output format** -- the directory layout, sidecar manifest, and event taxonomy that any subscriber needs to read in order to write a working integration.*

---

## 1. Problem Statement

Spec 01 established the architectural pattern (container unpacker -> archive layout -> format handler -> destination) and listed concrete archive layout matchers as V2+ deferred. This spec implements one of them: Google Takeout.

The motivation is direct: Steve has Takeouts. Glovebox's mbox importer (the destination for mail content) is partially implemented. The missing link is the recognizer-side workload that turns a Takeout zip into per-service events glovebox (and future consumers) can react to.

Three properties from spec 01 § 1 carry over and constrain this design:

1. **Composite archives.** A Takeout contains dozens of service subtrees. The importer must enumerate them all, not just the one we have a consumer for today.
2. **Diverging destinations.** Mail goes to glovebox; Photos eventually goes to Immich; many subtrees have no subscriber yet. The importer emits events; raw stays on storage; consumers attach themselves to media types they care about.
3. **Bursty scale.** A 12 GB Takeout with 500,000 messages arrives in one batch. The importer must avoid loading archive contents into memory (per-entry streaming decompression) and must not stall the rest of the recognizer cluster. Note: this is *seek-and-stream*, not a true byte-stream consumer -- Go's `archive/zip` requires `io.ReaderAt` and seeks to the central directory at the end of the file before reading entries. Works fine on a local file regardless of size; cannot operate on a stdin pipe or partially-downloaded zip.

## 2. Scope and Non-Goals

### 2.1 In scope (V1)

- One new binary, `archive-importer`, runnable both as a Kubernetes Job and on a workstation.
- Google Takeout provider detection plus ~15 subtree matchers (see § 5.2).
- Container unpacker for **zip only**. Takeout ships as zip; tar.gz / 7z paths are reserved in the design but not implemented in V1.
- Schema bump: `notification-event.v1.1.schema.json` adding the archive-related extensions from spec 01 § 4.
- New schema: `archive-layout-manifest.v1.schema.json` for the sidecar manifest the importer writes.
- Recognizer chart additions: a suspended CronJob template, ConfigMap, ServiceAccount; chart version bumps to 0.2.0.
- GitLab CI additions: one new `build:archive-importer` job following the existing kaniko pattern.
- Consumer-facing contract section (§ 7 of this spec) -- the authoritative description glovebox and future subscribers read.

### 2.2 Deferred (V2 and beyond)

- **Meta export recognizer.** Same structural shape; different subtree names. Lands as a follow-up spec/plan/MR. The Provider abstraction in § 5.1 leaves room for it.
- **Inbox watcher.** V1 is manually triggered (`kubectl create job ...` or `docker run ...`). A long-running watcher that auto-triggers on new archives in `incoming/archives/raw/` is V2.
- **tar.gz and 7z unpackers.** Takeout ships zip; other providers may ship differently.
- **Per-format handlers** (mbox parser, ics parser, etc.). These live in destination repos per spec 01 § 3.2. Glovebox's spec 09 is the first such handler.
- **Resume after partial failure.** V1 is single-shot. If unpack fails partway, the operator deletes `unpacked/<id>/` and retries. The sidecar manifest reserves a `resume_state` field for V2.

## 3. Layer Naming Reconciliation

Spec 01 called the second layer "layout recognizer". The recognizer **project** is also called recognizer (`Code/archiver` directory; renamed from "archiver"). This spec uses the term **archive layout** for that layer to avoid the name collision. Spec 01 has a known follow-up to land the same rename there; see archiver-ubc notes for the cross-reference.

## 4. Architecture

One binary. Two runtime modes. Single workflow inside the binary:

```
[ Archive file at  <data-root>/raw/<filename>.zip ]
                          |
                          v
       archive-importer ingest <path>
                          |
       1. Hash file (SHA256), derive <id> = <hash-prefix>-<filename-stem>
       2. Create <data-root>/unpacked/<id>/
       3. Unpack archive into unpacked/<id>/ (zip: seek to central
          directory, then per-entry streaming decompression -- not a
          true byte-stream consumer; needs random access on the input)
       4. Move original archive into unpacked/<id>/ (preservation;
          source + tree + manifest live as one unit)
       5. Detect provider (Google Takeout in V1) -- walk root, look for
          a top-level "Takeout/" dir
       6. For each child of "Takeout/", try subtree matchers in order:
            - matched: emit notification-event v1.1 of type
              archive-subtree-recognized with the right media_type
            - unmatched: log warning; if --include-unrecognized, emit
              archive/google-takeout/unrecognized-subtree
       7. Write archive-layout-manifest.v1.json sidecar in unpacked/<id>/
       8. Emit final archive-import-complete event
       9. Exit 0
```

### 4.1 Runtime modes

| Mode | Invocation | When to use |
|---|---|---|
| **Kubernetes Job** | `kubectl create job --from=cronjob/recognizer-archive-importer ...` (see § 8.1) | Production. Pod has the right PVC mounts, service account, env vars. |
| **Workstation** | `docker run --rm -v <local-dir>:/data ... archive-importer:<tag> ingest /data/...` | Local development, testing matchers against archives you haven't moved to NFS, debugging. |

Same image, same binary, same entry point. The chart injects defaults via env vars for the in-cluster case; CLI flags override them in either mode.

### 4.2 Idempotency

The `<id>` is content-addressed (SHA256-prefixed). Same archive byte-for-byte produces the same `<id>`. Three possible startup states for `unpacked/<id>/`:

| State | Detection | Default behavior |
|---|---|---|
| **Absent** | `unpacked/<id>/` does not exist | Full run (all 9 steps). |
| **Present, manifest valid** | `archive-layout-manifest.v1.json` exists and parses against the schema | Skip steps 2-4 (no re-extract, no re-move). Re-run steps 5-9: re-emit events with the **same `event_id`s** loaded from the prior manifest's `events_emitted` (downstream consumers see stable identity). Manifest overwritten with fresh timestamps but identical event IDs. |
| **Present, manifest absent or invalid** | `unpacked/<id>/` exists but `archive-layout-manifest.v1.json` is missing or fails schema validation | Treat as failed prior run. Exit code 5 with a clear error ("partial state at unpacked/<id>/; re-run with --force to overwrite, or remove the directory manually"). Use `--force` to re-extract anyway. |

`--force` overrides the third state: re-extract from scratch, regenerate manifest, mint new event IDs (the prior manifest is unreadable so old IDs can't be reused). `--force` is also valid in state 2 but unnecessary -- the binary skips unpack by default there.

**Concurrent-run protection.** Two `kubectl create job` invocations (or one in-cluster Job + one workstation run) against the same archive could race past the "absent" check and both unpack into the same directory. The binary acquires an exclusive `flock(2)` on `unpacked/<id>/.lock` immediately after step 2 (directory creation) and holds it until step 9. A second process that finds the lock taken exits 5 with "another import is in progress for this archive id" without disturbing the in-flight unpack. The lockfile is removed on clean exit; a stale lock left over from a SIGKILL'd predecessor manifests as state-3 (manifest missing) on the next run and is handled by the existing error path.

This is a deliberate split: "the data is already on disk, but please re-tell the relay about it" is a common operator need (relay was down, or a new subscriber wants old events). `--force` is for the rare "I think the unpack itself was wrong" case.

### 4.3 CLI surface

One verb (`ingest`) with these flags (env-var equivalents in parentheses):

| Flag | Env | Default | Notes |
|---|---|---|---|
| `--data-root` | `ARCHIVE_DATA_ROOT` | `/data/incoming/archives` | Base under which `raw/` and `unpacked/` live. |
| `--relay-url` | `NOTIFICATION_RELAY_URL` | computed in-cluster Service URL (§ 8.2) | Where to POST notification events. |
| `--include-unrecognized` | `INCLUDE_UNRECOGNIZED=true` | `false` | Emit events for unrecognized subtrees (§ 5.3). |
| `--force` | `--` | `false` | Re-extract even if `unpacked/<id>/` exists (§ 4.2). |
| `--id` | `--` | derived | Override the SHA-prefix+stem id (debugging). |
| `--dry-run` | `--` | `false` | Log everything, emit no events, write no files. |
| `--log-level` | `LOG_LEVEL` | `info` | `debug` / `info` / `warn`. |

**Exit codes:** `0` success; `1` archive unreadable / unpack failed; `2` matcher panic; `3` relay unreachable after retries; `4` manifest write failed; `5` partial-state directory (§ 4.2). Non-zero exit always implies "did not complete cleanly, do not trust partial events."

### 4.4 Language and reuse

Go. Matches `images/document-scanner/scanner-session-manager` (Go 1.26.x). The notification-relay client is a small new package; it does **not** import any code from `images/notification-relay` (which is Python). The relay's HTTP contract is documented in `schemas/notification-event.v1.1.schema.json`, and that's the only thing both sides share.

## 5. Archive Layout Matcher Design

### 5.1 Two-level interface

```go
package matcher

// SubtreeMatcher identifies whether a directory is a specific archive
// subtree and reports the media_type to emit when it matches.
type SubtreeMatcher interface {
    MediaType() string                              // "archive/google-takeout/mail"
    Description() string                            // one-line human description
    Matches(dirPath, dirName string) (bool, error)  // recognition test
}

// Provider groups a top-level provider detector with its subtree matchers.
type Provider struct {
    Name     string                                 // "google-takeout"
    Detect   func(rootPath string) (matched bool, subtreeBase string, err error)
                                                    // subtreeBase is where to start walking,
                                                    // e.g., "<root>/Takeout" for Takeout
    Subtrees []SubtreeMatcher
}
```

The binary holds a static slice of `Provider`s (just one in V1) and tries each `Detect` against the unpacked root. The first match wins; remaining providers are not tried. If multiple providers ever match, a warning is logged.

### 5.2 Google Takeout provider + subtree matchers

**Provider detection:** `Detect` returns `(true, "<root>/Takeout", nil)` if a `Takeout/` directory exists immediately under the unpacked root. Google's archive zips always wrap content in this top-level dir.

**Subtree matchers** (V1 set; each is small: directory name + content fingerprint):

| Media type | Dir name | Fingerprint |
|---|---|---|
| `archive/google-takeout/mail` | `Mail/` | At least one `*.mbox` file |
| `archive/google-takeout/calendar` | `Calendar/` | At least one `*.ics` file |
| `archive/google-takeout/chat` | `Chat/` or `Google Chat/` | `Groups/` or `Conversations/` subdir |
| `archive/google-takeout/keep` | `Keep/` | One or more `*.json` files |
| `archive/google-takeout/notebooklm` | `NotebookLM/` | `*.html` notebooks |
| `archive/google-takeout/voice` | `Voice/` | `Calls/`, `Texts/`, or `Voicemails/` subdir |
| `archive/google-takeout/my-activity` | `My Activity/` | `*.html` or `*.json` activity files |
| `archive/google-takeout/photos` | `Google Photos/` | `*.json` metadata next to media files |
| `archive/google-takeout/timeline` | `Location History/` or `Timeline/` | `*.json` location records |
| `archive/google-takeout/youtube` | `YouTube and YouTube Music/` | `videos/` subdir |
| `archive/google-takeout/fit` | `Fit/` | `Activity/` + `*.tcx` or `*.gpx` |
| `archive/google-takeout/drive` | `Drive/` | mixed content (`*.docx`, `*.pdf`, others) |
| `archive/google-takeout/tasks` | `Tasks/` | `*.json` task lists |
| `archive/google-takeout/contacts` | `Contacts/` | `*.vcf` files |

15 subtrees in the initial set. The exact list is finalized once we inspect an actual recent Takeout; the matchers may grow or shrink by one or two. Each is implemented as a separate function in `images/archive-importer/internal/matcher/google_takeout.go` with a corresponding unit test against a fixture directory.

### 5.3 Unrecognized subtrees

For each child of the provider base that no matcher claims, the importer logs a warning and records the subtree in the sidecar manifest under `subtrees_unrecognized`. By default, no event is emitted -- the data sits on disk for human inspection later.

The `--include-unrecognized` flag (env: `INCLUDE_UNRECOGNIZED=true`) flips this on: an event with `media_type: archive/google-takeout/unrecognized-subtree` is emitted for each unrecognized subtree, with `output_path` pointing at the actual directory. This is intended for forensic mode (running against a new-to-us Takeout to see what's there) and is off by default.

### 5.4 Provider abstraction headroom

The `Provider` struct is designed to accommodate Meta export and other providers in follow-up specs without refactoring V1 code:

```go
var Providers = []Provider{
    googleTakeout,
    // metaExport,        // spec 04 (deferred)
    // slackExport,       // spec 05 (deferred)
    // whatsappExport,    // spec 06 (deferred)
}
```

A future Meta export spec is a new `Provider` plus a slice of `SubtreeMatcher`s; no changes to the binary's main flow.

**Known gap for Meta:** Meta's combined Facebook + Instagram export unpacks with parallel roots (`your_facebook_activity/` AND `your_instagram_activity/`), unlike Google Takeout's single `Takeout/` wrapper. The current `Provider.Detect` signature returns one `subtreeBase`. When the Meta spec lands, either (a) the signature changes to return `[]string` (small refactor), or (b) Meta ships as two registered providers (`meta-facebook`, `meta-instagram`) that each detect their own root in the same unpacked tree. Flagging now so it isn't a surprise at spec-04 time.

## 6. Schema Changes

### 6.1 `notification-event.v1.1.schema.json` (additive over v1.0)

Lives in `schemas/`. Same envelope as v1.0 with these additions (per spec 01 § 4.1):

- `source` enum gains: `archive-recognizer`, `archive-unpacker`, `archive-format-handler`.
- `event_type` enum gains: `archive-unpacked`, `archive-subtree-recognized`, `archive-import-complete`.
- `media_type` is loosened: the v1.0 closed enum is replaced with a `oneOf` of (the v1.0 enum values) or (the pattern `^archive/.+$`). Examples enumerated in spec 01 § 4.1 plus the Google Takeout taxonomy from § 5.2 of this spec.
- `metadata` gains optional fields: `archive_format`, `item_count`, `byte_size`, `origin`, `parent_event_id`.

Backward-compatibility note from spec 01 § 4.1.1 applies: producers stamp `schema_version: "1.1"`. The notification-relay's existing prefix-match logic continues to work without code changes; strict consumers must update.

### 6.2 `archive-layout-manifest.v1.schema.json` (new)

The sidecar manifest the importer writes to `unpacked/<id>/`. Authoritative record of what happened during the import; lets re-runs reuse event IDs and gives consumers a complete picture.

Top-level structure:

```json
{
  "schema_version": "1.0",
  "archive_id": "<id>",
  "source": {
    "original_filename": "...",
    "moved_to": "unpacked/<id>/<original-filename>",
    "sha256": "...",
    "size_bytes": 12345,
    "mtime": "ISO-8601 timestamp",
    "archive_format": "zip"
  },
  "provider": "google-takeout",
  "matcher_version": "1.0",
  "timestamps": { "start": "...", "end": "..." },
  "subtrees_recognized": [
    {
      "media_type": "archive/google-takeout/mail",
      "output_path": "unpacked/<id>/Takeout/Mail",
      "item_count": null,
      "byte_size": 1234567,
      "event_id": "..."
    }
  ],
  "subtrees_unrecognized": [
    {
      "path": "Takeout/SomeNewService",
      "first_seen": "...",
      "byte_size": 123,
      "emitted_event": false
    }
  ],
  "events_emitted": [
    { "event_id": "...", "event_type": "...", "media_type": "...", "timestamp": "..." }
  ]
}
```

Field-by-field semantics:

- `archive_id`: the SHA256-prefix + filename-stem identifier. Stable across re-runs of the same archive.
- `source.moved_to`: relative path from the importer's data root. The original file is moved (not copied) after unpack to keep source + tree + manifest as one self-contained directory.
- `provider`: which provider matched. `null` if no provider detected (in that case the manifest still exists, but `subtrees_recognized` and `events_emitted` are empty).
- `matcher_version`: a string the binary stamps so consumers (or future-us reading old manifests) know which matcher set produced this output.
- `subtrees_recognized[].item_count`: `null` from the importer. The layout matcher does not count per-item content (that's the format handler's job). Filled in later by downstream format handlers in their own per-format manifests.
- `subtrees_unrecognized[].emitted_event`: `true` only if the importer ran with `--include-unrecognized` AND emitted the event successfully.
- `events_emitted`: complete record of POSTs to the relay. On re-run, the binary loads this and re-uses event IDs.

No `resume_state` field in V1. Reserved for V2 if unpack-resumption ever ships.

## 7. Consumer-Facing Output Contract

This section is what glovebox (and future subscribers) reads to write a working integration. Stable promise; only changes when this spec's version changes.

### 7.1 Where to find an archive's output

Given an `archive_id` (from a notification event or out-of-band), the importer's output lives at:

```
<recognizer-data-root>/incoming/archives/unpacked/<id>/
  <original-archive>.zip                  # the moved-in source archive
  archive-layout-manifest.v1.json         # sidecar manifest (see § 6.2)
  Takeout/                                # unpacked provider tree
    Mail/
    Calendar/
    ...
```

`<recognizer-data-root>` is the shared NFS / Longhorn-backed PVC mounted across recognizer workloads. For consumers running in the same K8s namespace, mount the same PVC and read from this path.

### 7.2 What's stable, what isn't

| Stable across importer versions (consumer can rely on) | Not stable (consumer must not hard-code) |
|---|---|
| The `unpacked/<id>/` directory containing the manifest, source archive, and unpacked tree | The provider's internal directory shape (Google may rename `Mail/` to `Gmail/`) |
| The presence of `archive-layout-manifest.v1.json` sidecar | The specific filenames inside subtrees (Google may rename `All mail Including Spam and Trash.mbox`) |
| The `archive/<provider>/<service>` media type taxonomy as a prefix | Whether a specific subtree exists in any given archive (depends on user's service usage) |
| The notification event envelope (`schema_version: "1.1"` and field set) | Item counts at recognition time (importer reports `null`) |
| The schema_version field on the manifest | The exact set of subtrees recognized (matcher set grows over time) |

**Rule of thumb:** consumers consume via the event's `output_path` (which points at the subtree directory), never by reconstructing inner paths from a hardcoded layout assumption.

### 7.3 Event flow consumers subscribe to

Three event types are emitted during a single ingest:

| Event type | When | Useful for |
|---|---|---|
| `archive-unpacked` | After step 3 (unpack complete) | Consumers that want to be told "raw extracted content is now at this path" before any subtree matching happens. V1 may emit this as a no-op for cleanliness; consumers usually skip it. |
| `archive-subtree-recognized` | Per recognized subtree, after step 5 matches one | The primary signal. Subscribe by `media_type` prefix. `output_path` points at the subtree dir. |
| `archive-import-complete` | After step 8 (end of import) | Audit / dashboard. Summarizes the whole ingest. |

Most consumers care only about `archive-subtree-recognized` with a specific `media_type`.

### 7.4 Subscribing by media type

**Current relay behavior (V1):** the notification-relay fans out **every event** to **every configured destination** -- there is no server-side filtering by `media_type` or `event_type`. Subscribers receive the full event stream and filter **client-side** by inspecting `media_type` on each incoming event.

A practical consumer (e.g., glovebox's mbox importer) implements a prefix-match in its own handler:

```python
def on_event(event):
    if not event["media_type"].startswith("archive/"):
        return                                   # not an archive event
    if event["media_type"] != "archive/google-takeout/mail":
        return                                   # not my event
    if event["event_type"] != "archive-subtree-recognized":
        return                                   # only the subtree-recognized step
    # ... handle the mail subtree at event["output_path"]
```

Consumers that want broader coverage (e.g., "any provider's mail") use a less-specific prefix in the same client-side check: `event["media_type"].endswith("/mail")` or `re.match(r"archive/[^/]+/mail$", event["media_type"])`.

**Future evolution (deferred):** spec 01 § 6 lists "NATS subjects per `plan.md`, Section 3.3.2" as a possible replacement for the webhook relay. NATS subjects would let subscribers register server-side prefix subscriptions, removing the all-events-to-all-destinations fan-out cost. The event envelope and `media_type` taxonomy stay identical when that swap happens; consumer code changes from "filter on receive" to "subscribe to a subject". Not in V1.

The wildcard expressions in earlier drafts of this spec (`archive/*/mail`) describe the *logical* intent and would be subjects under a future NATS-based bus; today they are client-side regex predicates.

### 7.5 Worked example

Operator runs the importer against `takeout-2026-04-11.zip` (SHA256 prefix `4f2a8b3c`). Example notification event posted for Mail:

```json
{
  "schema_version": "1.1",
  "source": "archive-recognizer",
  "event_type": "archive-subtree-recognized",
  "event_id": "evt_01HFXXX...",
  "timestamp": "2026-05-19T11:30:08Z",
  "media_type": "archive/google-takeout/mail",
  "output_path": "/data/incoming/archives/unpacked/4f2a8b3c-takeout-2026-04-11/Takeout/Mail",
  "metadata": {
    "archive_format": "zip",
    "byte_size": 8451220992,
    "origin": "Google Takeout 2026-04-11",
    "parent_event_id": "evt_01HFWYY..."
  }
}
```

Glovebox's subscriber reads `output_path`, walks the directory, finds the `*.mbox` file, runs its mbox importer against it. Glovebox's own per-format manifest records what messages were imported; that manifest lives in a glovebox-controlled location, not next to the importer's manifest.

## 8. Chart and CI Integration

### 8.1 Chart additions

New workload directory under `charts/recognizer/templates/archive-importer/`:

```
templates/archive-importer/
  configmap.yaml         # dataRoot, relayUrl, includeUnrecognized, logLevel
  serviceaccount.yaml    # workload identity (no special RBAC needed)
  cronjob.yaml           # suspended template; users create Jobs from it
```

The CronJob ships with `spec.suspend: true` and a placeholder schedule (`"0 0 1 1 0"`, Jan 1 -- effectively never). It exists as a template only. The pod spec inside has all volumeMounts (the chart's `<release>-data` PVC), env vars (data root, relay URL), service account, image, and resources -- everything except the per-archive arg.

**Invocation recipe** (stock `kubectl` + `yq`; no extra wrapper required):

```bash
ARCHIVE=takeout-2026-04-11.zip
kubectl -n recognizer get cronjob recognizer-archive-importer -o yaml \
| yq '
    .spec.jobTemplate as $jt
    | $jt
    | .apiVersion = "batch/v1"
    | .kind = "Job"
    | .metadata.name = "archive-import-'${ARCHIVE%.zip}'-'$(date +%s)$(openssl rand -hex 2)'"
    | .metadata.namespace = "recognizer"
    | .spec.template.spec.containers[0].args = ["ingest", "/data/incoming/archives/raw/'$ARCHIVE'"]
  ' \
| kubectl apply -f -
```

The yq filter promotes the CronJob's `jobTemplate` into a freestanding Job, renames it with a deterministic-ish suffix, and overrides the container args. All other pod-spec details (image, volumeMounts, env, serviceAccount, resources) come from the chart's CronJob template unchanged. The chart also ships this recipe as a small wrapper script at `images/archive-importer/scripts/run-job.sh` that takes one argument (the archive filename) for operators who prefer it; the script is just the yq pipeline above.

*Note on alternatives considered:* `kubectl create job --from=cronjob/...` copies the pod spec but does not accept arg overrides; `kubectl set args` does not exist as a subcommand. The yq pipeline is the simplest pattern that actually works with stock tools.

### 8.2 `values.yaml` additions

```yaml
archiveImporter:
  enabled: true              # no hardware deps; safe to enable by default
  image:
    name: archive-importer
    tag: ""                  # defaults to Chart.AppVersion
  resources:
    requests:
      cpu: 200m
      memory: 256Mi
    limits:
      cpu: "1"
      memory: 2Gi            # conservative ceiling. Steady-state demand is
                             # in the low hundreds of MiB (zip central
                             # directory ~50 MiB for a 500k-entry archive,
                             # decompression buffers <1 MiB per stream,
                             # relay-retry queue <100 MiB). The 2 GiB ceiling
                             # is headroom for fragmented or highly compressed
                             # archives; tune downward after a real-Takeout
                             # baseline run.
  config:
    dataRoot: /data/incoming/archives
    relayUrl: ""             # empty = use computed in-cluster default
                             # (see _helpers.tpl "recognizer.relayUrl")
    includeUnrecognized: false
    logLevel: info
```

Helm does not render `{{ }}` markers inside `values.yaml` string literals -- only inside template files. The default `relayUrl` is therefore computed in `_helpers.tpl` as a new helper:

```
{{- define "recognizer.relayUrl" -}}
{{- printf "http://%s-notification-relay.%s.svc.cluster.local:8080/notify"
      (include "recognizer.fullname" .)
      .Release.Namespace -}}
{{- end -}}
```

The archive-importer ConfigMap template invokes it with:

```
relayUrl: {{ .Values.archiveImporter.config.relayUrl | default (include "recognizer.relayUrl" .) | quote }}
```

Empty string in `values.yaml` means "use the computed in-cluster default"; operators override by setting a real URL.

### 8.3 Image tag handling

The existing `recognizer.image` helper already supports per-workload tag overrides (the chart accepts `.Values.<workload>.image.tag` and falls back to `Chart.AppVersion` when empty). Archive-importer follows the same pattern -- no helper change required. Per-workload tag overrides are how the binary's release cadence diverges from the chart's once the workloads ship at different velocities.

### 8.4 CI additions

One new kaniko build job in `.gitlab-ci.yml`, mirroring the existing two:

```yaml
build:archive-importer:
  extends: .build
  variables:
    DOCKERFILE: images/archive-importer/Dockerfile
    COMPONENT: archive-importer
```

The new Go module under `images/archive-importer/` is added to the `test:go` and `vuln:go` matrices the same way the existing scanner module is. No changes to `helm:lint` or `package:chart`.

### 8.5 Versioning

This work ships:

- `archive-importer` image at `v0.1.0` (initial release of the binary)
- Recognizer chart at `0.2.0` (additive new workload; minor bump per chart's semver-ish posture)
- Schemas at their own versions: `notification-event.v1.1`, `archive-layout-manifest.v1`

Spec 03 (this document) is `v1.0`. A future v1.1 of this spec adds Meta export and/or new Takeout subtrees discovered in the wild.

## 9. Testing and Acceptance

### 9.1 Fixtures

`images/archive-importer/testdata/fixtures/` holds tiny, hand-built directory trees that look like real Takeouts but contain trivial content. Each fixture is committed; no CI downloads or external dependencies.

- `google-takeout-minimal/` -- one file in every known subtree. Full coverage.
- `google-takeout-mail-only/` -- just `Takeout/Mail/foo.mbox`. Absent-subtree handling.
- `google-takeout-with-unknown/` -- has `Takeout/SomeNewService/`. Validates the unrecognized path.
- `not-an-archive/` -- random directory with no `Takeout/` root. Provider detection failure.
- `takeout-zip/takeout-2026-04-11.zip` -- zipped version of `google-takeout-minimal/`. Validates the unpacker.

### 9.2 Test pyramid

| Layer | Goal |
|---|---|
| **Unit (per-matcher)** | Each subtree matcher's `Matches()` against each fixture. One assertion per matcher per fixture. |
| **Unit (unpacker)** | Zip unpack against the fixture zip; asserts the tree shape, file count, byte-size totals. |
| **Unit (manifest)** | Manifest writer produces JSON that validates against `archive-layout-manifest.v1.schema.json`. |
| **Unit (relay client)** | `httptest.Server` mock; retries, exit codes, schema-version field. |
| **Integration** | End-to-end `ingest` against the fixture zip + a mock relay. Asserts: (a) all expected events emitted with correct media types and output paths; (b) manifest exists and validates; (c) emitted events validate against `notification-event.v1.1.schema.json`. |
| **Integration (idempotency)** | Two consecutive `ingest` runs of the same archive. Same `<id>`, same manifest, same event IDs re-emitted. |
| **Integration (failure modes)** | Corrupt zip -> exit 1. Missing data root -> exit 1. Relay 500s -> exit 3 after retries. `--include-unrecognized=false` and unknown subtree -> exit 0, warning logged, no event, manifest records the unrecognized. |
| **Manual acceptance** | A real Steve-supplied Google Takeout lands events in notification-relay end-to-end. Documented in this spec's § 9.4. |

### 9.3 CI coverage

- `test:go` runs `go test ./...` from the new module.
- `vuln:go` runs govulncheck against it.
- `helm:lint` and `package:chart` cover the chart-side additions.

### 9.4 Acceptance criteria (what "done" means)

1. Schema files committed: `schemas/notification-event.v1.1.schema.json`, `schemas/archive-layout-manifest.v1.schema.json`.
2. This spec committed as `docs/specs/03-archive-importer-google-takeout.md` v1.0.
3. Binary builds + tests green in CI; chart bumped to 0.2.0; `archive-importer:v0.1.0` published to the registry.
4. Idempotency verified: same fixture archive yields a manifest that is byte-identical across two consecutive runs after applying `jq 'del(.timestamps, .events_emitted[].timestamp, .subtrees_unrecognized[].first_seen)'`. Event IDs in `events_emitted` are identical across runs.
5. A real user-supplied Google Takeout lands `archive-subtree-recognized` events in notification-relay end-to-end, with the sidecar manifest accurately enumerating the recognized + unrecognized subtrees.
6. § 7.5's example event in this spec is regenerated from the manual acceptance run (real values, not fabricated).

Six criteria. All landed = V1 done. Meta export, inbox watcher, and the other deferred items become their own spec / plan / MR cycles.

## 10. Risks and Open Questions

- **Path traversal in zip entries.** Maliciously-crafted zips can include entries with names like `../../etc/passwd`. Go 1.20+ surfaces this as `zip.ErrInsecurePath` when `GODEBUG=zipinsecurepath=0` (the default). The importer must treat that error as fatal -- never opt into the insecure-path mode. Documented in the implementation plan as a hard requirement.
- **Disk-space pre-flight.** A 12 GB zip expanding 1.5-2x plus the source-preservation move means single-archive footprint approaches 30 GB. The chart's default 50 GB PVC fits one in-flight import; multiple concurrent imports could exhaust it. V1 ships single-threaded, but the binary should `df`-check the data root before step 3 and exit 1 if free space < (archive size * 3) with a clear message. A `--skip-space-check` escape hatch is reserved.
- **CI runner arch.** Per spec 02 § 11, the GitLab Runner is amd64-only (helper image is `x86_64-...`; CI YAML pins build pods via `KUBERNETES_NODE_SELECTOR_arch: "kubernetes.io/arch=amd64"`). The `build:archive-importer` job inherits this; the resulting image is amd64-only until `gitops-vney` unblocks privileged dind. The chart's CronJob should also carry an `amd64` nodeAffinity so the importer doesn't get scheduled to an arm64 node and `ImagePullBackOff`. Documented in the implementation plan.
- **Relay dedup posture.** § 4.2's idempotent re-run reuses `event_id`s, so a relay that dedups on `event_id` (or `(source, event_id)`) gracefully absorbs duplicates. A relay that does NOT dedup will see double-emits and forward them to subscribers twice on re-run. Verify the current relay's behavior in the implementation plan before claiming idempotency end-to-end; if it doesn't dedup, file a follow-up to add it (small change; the field is already in the schema).
- **Acceptance examples and PII.** § 9.4 #6 asks for spec examples regenerated from a real Takeout. A real archive's filename can contain the user's email or name (`takeout-bob.smith@example.com-2026-04-11.zip`). PR review should redact filenames in the example JSON; alternatively, the manual acceptance step uses a deliberately-anonymized filename copy.
- **Real-world Takeout drift.** Google renames subtree directories without notice (Hangouts -> Chat -> Google Chat in living memory). The matcher's directory-name list will need maintenance. Each matcher checks a fingerprint (file presence) in addition to the directory name, which softens the blow somewhat. A new directory name + same fingerprint usually means "add an alias to one matcher", a small PR.
- **Very large Takeouts.** Steve's largest tested archive is ~12 GB. Streaming unpack means peak memory stays low, but **disk pressure** is real -- the unpacked tree can exceed the source by 1.5-2x for highly compressed inputs. The recognizer chart's default 50 GB Longhorn PVC will fit any plausible Takeout, but **multiple in-flight imports could exhaust storage**. V1 ships single-threaded; a `--max-concurrent` flag is reserved.
- **Schema v1.1 strict-consumer breakage.** Per spec 01 § 4.1.1, strict consumers using `additionalProperties: false` and closed-enum validation will reject v1.1 events. The current consumer (notification-relay) does prefix-matching and is unaffected. Future consumers must check the `schema_version` field.
- **Event ID reuse on re-run.** Re-emitting a previously-recorded `event_id` is "right" from a consumer-idempotency standpoint but can surprise log-scrapers that assume IDs are append-only. Documented in spec 03 § 7 and in the manifest schema's `events_emitted` field description.
- **Relay durability vs. importer durability.** If the relay is down when the importer runs, the importer retries (with backoff) and eventually exits 3. The unpacked tree + manifest still land successfully. The operator re-runs the importer (idempotent) once the relay is back; events fire then. The manifest is the durability layer; the relay is the eventing layer.
- **Unrecognized-subtree warning noise.** When run in default mode against a new-to-us Takeout shape, the importer may log many warnings. Operators should run with `--include-unrecognized=true` for the first import of any user's Takeout, then file a follow-up to add the missing matchers.

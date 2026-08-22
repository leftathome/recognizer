# Action plan — closing the gaps — 2026-08-22

Derived from [glovebox-integration-review.md](glovebox-integration-review.md)
and [architecture-implementation-review.md](architecture-implementation-review.md).
Written to be executed by a fleet of subagents on smaller models: every packet
is self-contained (context, files, exact change, acceptance checks,
validation commands) so an agent needs no conversation history to do it well.

## 0. Fleet operating rules

These apply to **every** packet:

- **Branch:** work lands on `claude/glovebox-integration-review-heqi5p`
  (this PR's branch) unless the orchestrator says otherwise. One commit per
  packet, message prefixed with the packet ID, e.g.
  `fix(chart): CNP selects real pods + hardware-ns policy [B1]`.
- **Validation gates (all must pass before a packet is "done"):**
  - `helm template charts/recognizer --kube-version 1.34.0` renders clean
    (also with `--set hardware.enabled=true`, `--set archiveImporter.enabled=true,archiveImporter.gloveboxIngest.enabled=true`, `--set walhelmSource.enabled=true,walhelmSource.subjectPrincipal=walhelm:test`)
  - `helm lint --strict charts/recognizer`
  - `python -m pytest tests/` (repo root; needs `pip install -r requirements.txt`)
  - Go packages touched: `go vet ./... && go test ./...` from that module dir
    (`images/archive-importer`, `images/document-scanner/scanner-session-manager`)
- **Don't widen scope.** A packet fixes what it names. New discoveries become
  notes for the tracker (packet H2), not drive-by edits.
- **Match surrounding style.** Chart comments in this repo explain operator
  intent; keep that voice. No model names in commits/code.
- **Tracker:** this repo uses beads (`bd`). The `bd` CLI isn't available in
  the current execution environment, so packets reference existing bead IDs
  and packet H2 batches the tracker updates for when it is.
- **Model tier guide:** `haiku` = mechanical, well-specified edits;
  `sonnet` = code with judgment (Go wiring, tests, security-sensitive YAML).
  Anything marked *orchestrator* stays with the coordinating agent.

## 1. Packet index

Status column updated 2026-08-22 after the fleet run on this branch; every
DONE packet landed as its own commit (grep the log for `[<ID>]`).

| ID | Title | Prio | Tier | Depends on | Status |
|---|---|---|---|---|---|
| B1 | Make the CNP real: selector, egress set, hardware-ns policy | P0 | sonnet | — | ✅ DONE (also fixed the never-matching relay egress selector) |
| B2 | securityContexts for scanner + relay; relay Dockerfile USER | P0 | haiku | — | ✅ DONE |
| B3 | Relay hardening: threaded server, bounded+unique dead-letters | P1 | sonnet | — | ✅ DONE |
| B4 | Glovebox token: env var → mounted file, re-read per delivery | P1 | sonnet | — | ✅ DONE |
| B5 | Rotate + purge `.beads-credential-key`; .gitignore secrets patterns | P0/P3 | operator + haiku | — | ⚠️ gitignore DONE; **rotation/purge still an operator action** |
| B6 | Kill `capture`-namespace fossils (scanner cfg, alerts, post_rip default) | P1 | haiku | — | ✅ DONE |
| B7 | `automountServiceAccountToken: false` everywhere | P2 | haiku | — | ✅ DONE |
| A1 | Values/docs: glovebox floor 0.6.4, bearerPort migration note, required-mode gate | P1 | haiku | — | ✅ DONE |
| A2 | Delivery client: `https://` + optional CA bundle support | P1 | sonnet | — | ✅ DONE |
| A3 | Upstream issues to glovebox (handoff staleness, producer cert, mount-path doc bug) | P2 | orchestrator | user sign-off | ✅ DONE — filed with sign-off 2026-08-22: glovebox#65, #69, #70 |
| C1 | Deploy `post_rip.py`: mount + wire into ARM | P1 | sonnet | B6 | ✅ DONE (via ARM's verified `BASH_SCRIPT`; NOTIFY_WEBHOOK arm.yaml key was invented and got removed) |
| C2 | Scanner: wire manifest+notify into `main.go`; real device name; read config | P1 | sonnet | B6 | ✅ DONE |
| C3 | Dead-letter drain CronJob | P2 | sonnet | B3 | ✅ DONE |
| C4 | Metrics: relay `/metrics` + fixed ServiceMonitor/alerts | P2 | sonnet | — | ✅ DONE |
| C5 | Grafana dashboard JSON | P3 | haiku | C4 | ✅ DONE |
| C6 | Chart render tests (pytest + `helm template` assertions) | P1 | sonnet | B1,B2,B6 | ✅ DONE (suite caught + we fixed: dashboard JSON rendered as object; duplicate part-of labels on 4 ExternalSecrets) |
| C7 | Scanner driver/processor split — Slice 1 | P2 | opus/sonnet, phased | C2 | ❌ large; own PR |
| C8 | Schema: `disc-detected`/`disc-ejected` events (`archiver-9xw`) | P3 | sonnet | — | ✅ DONE |
| H1 | Unblock CI (`archiver-850`): go1.26.4 bump + pypi→apt jsonschema | P1 | sonnet | — | ⚠️ changes landed; **pipeline verification pending on GitLab** |
| H4 | GitHub Actions CI (glovebox's pattern) while developing against GitHub | P1 | orchestrator | — | ✅ DONE — `.github/workflows/{ci,codeql,release}.yml`: tests (python+chart render, both Go modules), govulncheck+trivy, helm lint/kubeconform/OCI chart push, multi-arch images to `ghcr.io/leftathome/recognizer/*`, tag-driven release. Walhelm packages/image excluded (private dep only reachable from gitlab.orac.local) — GitLab CI remains their gate |
| H2 | Tracker reconciliation: re-open/annotate misclosed beads, file new ones | P2 | operator/orchestrator | bd available | ❌ needs `bd` (unavailable in the fleet's environment) |
| H3 | Verify gitops-side artifacts (NFD rules incl. USB class 06, SDM, Flux HelmRelease, ns label) | P1 | operator | gitops repo access | ❌ cross-repo |

❌/⚠️ packets are documented hand-offs, not silent drops.

### Follow-ups discovered during execution (for H2's bead filing)
- `.gitlab-ci.yml` needs `GITLAB_TOKEN` on the new `test:chart` job? No —
  but the walhelm packages (`internal/walhelm`, `cmd/walhelm-fetch`) could
  not be compiled in the fleet environment (private walhelm-go); their B4/A2
  edits are minimal and mirrored from tested code, and CI's
  `test:go:archive-importer` is the authoritative check.
- Scanner: `session.StartIdleTimer()` and an ADF-complete trigger are still
  not wired into the HTTP surface (pre-existing; belongs to C7/Slice 1).
- The chart's inlined `post_rip.py` copy (hook ConfigMap) must track
  `images/optical-ripper/hooks/post_rip.py`; the C6 render test fails on
  drift, so a divergence cannot land silently.
- Relay drain + queue knobs are values-driven; gitops HelmRelease values may
  want overrides once real destination volume is known.

## 2. Packet specifications

### B1 — Make the CiliumNetworkPolicy real (P0, sonnet)
**Context:** `charts/recognizer/templates/networkpolicies.yaml:10-12` selects
`app.kubernetes.io/part-of: recognizer`, but no pod template carries that
label (`_helpers.tpl:27-36`). The privileged `recognizer-hardware` namespace
has no policy at all. No egress rule covers glovebox:9091, so fixing the
selector alone would break delivery.
**Change:**
1. Add `app.kubernetes.io/part-of: recognizer` to `recognizer.labels` in
   `_helpers.tpl` (flows to all pod templates via existing includes — verify
   each template's pod `metadata.labels` includes it after rendering).
2. In `networkpolicies.yaml`: keep NFS/relay/OMDb/MusicBrainz/DNS egress; add
   an egress rule to the glovebox namespace on TCP 9091 gated on
   `archiveImporter.gloveboxIngest.enabled` or `walhelmSource.enabled`, with
   the port taken from a new helper parsing `gloveboxIngest.url` (or a
   documented `gloveboxIngest.port` value — simpler; add it to values with a
   comment tying it to the bearerPort migration in A1).
3. New `networkpolicies-hardware.yaml` rendered into
   `recognizer.hardwareNamespace` when `hardware.enabled`: egress limited to
   relay:8080, OMDb/MusicBrainz:443, DNS:53; ingress limited to release
   namespace on 8080 (ARM UI) — mirror the values structure of the existing
   policy.
**Accept:** rendered CNPs' `endpointSelector` labels appear verbatim in every
pod template's labels; `helm template` diff shows hardware policy only with
`hardware.enabled=true`; C6 tests assert the selector↔pod-label match.

### B2 — securityContexts for scanner + relay (P0, haiku)
**Context:** `templates/document-scanner/daemonset.yaml` and
`templates/notification-relay/deployment.yaml` set no pod/container
securityContext; relay Dockerfile has no `USER`. Both violate the PSS
`restricted` posture claimed at `values.yaml:33-35`.
**Change:** copy the archive-importer pattern
(`archive-importer/cronjob.yaml:42-61`): pod `runAsUser/Group: 1000`,
`runAsNonRoot: true`, `fsGroup: 1000`, `seccompProfile: RuntimeDefault`;
container `allowPrivilegeEscalation: false`, `capabilities.drop: [ALL]`.
Add `USER 1000:1000` to `images/notification-relay/Dockerfile` (confirm the
Python entrypoint needs no privileged port — it binds 8080, fine). The
scanner DaemonSet will move namespaces in C7; hardening it now is still
correct.
**Accept:** `helm template` output for both workloads contains the contexts;
relay container starts as non-root (Dockerfile builds; `docker run --rm <img> id`
if docker available, else rely on review).

### B3 — Relay hardening (P1, sonnet)
**Context:** `images/notification-relay/relay/main.py` runs a single-threaded
`HTTPServer` and calls `with_retry` (up to 155s of sleep) inside the request
handler; dead-letter filenames are second-granularity
(`deadletter.py:21-22`) so bursts overwrite.
**Change:** (1) switch to `ThreadingHTTPServer`; (2) respond 202 immediately
after validation and run fan-out+retry on a worker thread (bounded queue;
on-full → dead-letter immediately with reason); (3) add a UUID suffix to
dead-letter filenames; (4) cap dead-letter dir growth (e.g. refuse+log above
N files, N configurable via env, default 10000). Keep stdlib-only. Update
`tests/relay/` accordingly (existing tests assert synchronous 202 — adapt to
poll the mock destination; keep `test_default_delays_are_5_30_120`).
**Accept:** `pytest tests/relay` green; a test proves two events with the
same timestamp produce two dead-letter files; a test proves a slow
destination doesn't block a second request.

### B4 — Glovebox token as mounted file (P1, sonnet)
**Context:** both CronJobs inject `GLOVEBOX_INGEST_TOKEN` via `secretKeyRef`
env (`archive-importer/cronjob.yaml:82-86`, `walhelm-fetch/cronjob.yaml:64-68`).
Glovebox's handoff doc says re-read the token per send; env vars also leak
via `/proc/<pid>/environ`.
**Change:** support `GLOVEBOX_INGEST_TOKEN_FILE` in
`images/archive-importer` (flags/env plumbing + `delivery.Client` reading the
file per request, trimming whitespace; keep `GLOVEBOX_INGEST_TOKEN` as
fallback for compatibility). Mount the existing Secret as a read-only volume
in both CronJobs (mirror the walhelm-session mount pattern) and set
`GLOVEBOX_INGEST_TOKEN_FILE` instead of the env secret.
**Accept:** `go test ./...` green with new unit tests (file present, file
rotated between two deliveries → new value used, file missing+env fallback);
`helm template` shows the mount and no `secretKeyRef` env for the token.

### B5 — Credential rotation + ignore patterns (P0 operator / P3 haiku)
**Operator (documented, not automatable here):** rotate the beads Dolt
credential (`.beads/.beads-credential-key`, retrievable at `1ab0a0d^`); if
the GitLab/GitHub remotes are shared, purge history (`git filter-repo`) and
force-push per host policy.
**Agent part:** add `*.key`, `*.pem`, `.env`, `session.json`,
`.beads/.beads-credential-key` to `.gitignore`.
**Accept:** `.gitignore` updated; rotation recorded in tracker (H2).

### B6 — Kill `capture` fossils (P1, haiku)
**Files/lines:** `templates/document-scanner/configmap.yaml:13` (use
`recognizer.relayUrl` helper from `_helpers.tpl:83-87`);
`templates/monitoring/prometheusrule.yaml:22` (namespace filter →
`{{ .Release.Namespace }}` + hardware namespace);
`images/optical-ripper/hooks/post_rip.py:78` (default →
`http://recognizer-notification-relay:8080/event`-shaped placeholder or
require env, matching how C1 wires it; update its tests).
**Accept:** `grep -rn "capture" charts/ images/` shows no live-namespace
fossils (docs/history references fine); pytest green.

### B7 — `automountServiceAccountToken: false` (P2, haiku)
On both ServiceAccounts and every pod spec (ripper, scanner, relay, both
CronJobs). None of the binaries touch the K8s API.
**Accept:** rendered manifests show it on every pod; helm lint green.

### A1 — Integration values + docs refresh (P1, haiku)
**Change:** in `values.yaml` comments for both `gloveboxIngest` blocks: state
the supported glovebox floor (app ≥ 0.6.4), the coming `bearerPort`
migration (URL port must change in the operator's window), and the
`required`-mode precondition (glovebox must carry the bearer-listener fix).
Add the same to `charts/recognizer/README.md` integration section, replacing
any "v0.7.0" framing. Cross-link `docs/analysis/glovebox-integration-review.md`.
**Accept:** grep shows no stale claims; helm lint green (comments only).

### A2 — TLS-capable delivery client (P1, sonnet)
**Context:** `internal/delivery/client.go` builds a bare `http.Client`; the
transport to glovebox is plaintext. Glovebox's bearer listener is plaintext
today, but the client must be ready the day it isn't — and must be able to
pin a private CA.
**Change:** honor `https://` URLs; add optional `GLOVEBOX_INGEST_CA_FILE`
(PEM bundle → `tls.Config.RootCAs`; TLS 1.2+ min, prefer 1.3); add optional
`GLOVEBOX_INGEST_REQUIRE_TLS=true` that refuses to start with an `http://`
URL (for the walhelm/PHI path once the server side exists). Plumb through
walhelm-fetch too (shared client). Chart: optional values +
mounted-ConfigMap CA, rendered only when set.
**Accept:** unit tests with `httptest.NewTLSServer` (custom CA accepted,
wrong CA refused, require-TLS refuses http); `go vet`/`go test` green;
`helm template` unchanged when values unset.

### A3 — Upstream glovebox issues (P2, orchestrator) — ✅ DONE 2026-08-22
Filed on `leftathome/glovebox` per integration review §6, with the user's
explicit go-ahead (outward-facing, so it waited for sign-off):

- **glovebox#65** — `docs/handoffs/recognizer-archive-delivery.md` drift:
  4 of 6 media types listed, no `bearer_port` section, no `required`-mode
  caveat, no `archive/recognizer-scan` section, chart 0.4.2 references, and
  the missing "app 0.6.4 is the floor for multi-GB uploads" note.
- **glovebox#69** — no `producer`-kind `Certificate` template in the chart,
  though `spiffe://glovebox/producer/<name>` is documented and accepted by
  the SAN parser. Not blocking (our path is bearer-token) — filed so the gap
  is on the books before mTLS reaches the bearer surface.
- **glovebox#70** — `docs/ingest-mtls.md` gives client keypair paths as
  `/etc/glovebox/tls/` while the chart mounts `/etc/ingest-tls/`; in-chart
  connectors are unaffected, out-of-chart clients hit a hard startup error.

Nothing here is a recognizer code change; track the responses if we later
need the producer cert (§4.3 of the integration review).

### C1 — Deploy the post-rip hook (P1, sonnet, after B6)
**Context:** `images/optical-ripper/hooks/post_rip.py` is tested but unwired;
ARM 2.23 supports invoking a script on job completion (verify the exact
mechanism against ARM 2.23 docs/config keys in
`templates/optical-ripper/configmap.yaml` — the chart comment there warns
against inventing config keys, so find the real one, e.g. ARM's
`ARM_POST_PROCESSING`/wrapper approach; if ARM 2.23 has no hook key, mount
the script and invoke via ARM's `abcde`/job-complete wrapper documented
route — the packet includes confirming which).
**Change:** ship the hook via ConfigMap (or bake into an init-copied dir),
mount into the ARM container, set its env (`RELAY_URL` from
`recognizer.relayUrl` — note cross-namespace: hardware→release namespace
FQDN), wire ARM config to call it, and update `charts/recognizer/README.md`
so its claim becomes true. Extend the tender/eject flow only if required.
**Accept:** `helm template --set hardware.enabled=true` shows the mount +
config reference; pytest for the hook still green; README claim now accurate;
B1's hardware-ns policy allows egress relay:8080 (dependency noted).

### C2 — Wire the scanner binary end-to-end (P1, sonnet, after B6)
**Context:** `cmd/scanner/main.go` never calls `manifest` or `notify`;
`web.go` scan handler is a stub; device name is a literal.
**Change:** in `OnClose`, build+write `manifest.json` (schema
`schemas/scan-session-manifest.v1.schema.json`) and send the
`scan-session-complete` event via `notify` (relay URL from env, populated by
the chart via `recognizer.relayUrl`); implement `handleScan` to actually
invoke `scan.Scanner` with the session's settings; resolve the SANE device
via `scanimage -f`/env override instead of the literal; read
`/etc/scanner/config.yaml` (already mounted) for idle timeout + relay URL
with env taking precedence. Keep the JSON API shape stable.
**Accept:** `go test ./...` green including new tests: OnClose writes a
schema-valid manifest (validate against the JSON schema fixture) and POSTs a
schema-valid event to an httptest relay; stub behaviors gone.

### C3 — Dead-letter drain CronJob (P2, sonnet, after B3)
Small stdlib-Python (reuse relay image) command `drain.py`: re-POST each
dead-letter file to the relay (or fan out directly via `fanout.py`), delete
on success, age out after N days (env). Chart CronJob (hourly, suspendable,
same hardening as archive-importer) mounting the shared PVC.
**Accept:** unit tests (success deletes, failure retains, age-out); rendered
CronJob passes kubeconform; plan.md §3.3.3 finally true.

### C4 — Real metrics (P2, sonnet)
Relay: stdlib-only `/metrics` text endpoint (or vendored client if allowed —
prefer stdlib given the image) exposing
`capture_notification_events_total{status}`,
`capture_notification_dead_letter_total`, fan-out latency histogram buckets
optional. Fix `templates/monitoring/servicemonitor.yaml` (port names must
match Services; add `namespaceSelector` covering the hardware namespace) and
`prometheusrule.yaml` (alerts reference emitted metrics + correct
namespaces; keep alert names).
**Accept:** pytest asserts `/metrics` output contains the counters after an
event; rendered ServiceMonitor/Rule reference only ports/metrics that exist.

### C5 — Grafana dashboard (P3, haiku, after C4)
`charts/recognizer/dashboards/recognizer.json` (or ConfigMap with
`grafana_dashboard: "1"` label — match how the cluster's Grafana sidecar
discovers dashboards; default to the ConfigMap+label pattern): rip counts by
media type (from ARM if scraped later), relay events/dead-letters, CronJob
success age. Cite panels to real metric names from C4 only.
**Accept:** JSON parses; kubeconform green; bead `archiver-emj.22` truthful.

### C6 — Chart render tests (P1, sonnet, after B1/B2/B6)
New `tests/chart/test_render.py`: run `helm template` (subprocess, skip if
helm absent) across the value permutations in §0 and assert: every CNP
`endpointSelector` label appears in every pod template; no `namespace:
capture` anywhere; every pod has `securityContext.runAsNonRoot` or is the
documented privileged ARM pod; ServiceMonitor port names ⊆ Service port
names; PrometheusRule namespaces ⊆ rendered namespaces; token env absent
when B4's file mount is active. Wire into `.gitlab-ci.yml` `test:python`
(helm image already fetched in `helm:lint` — reuse pattern).
**Accept:** pytest green locally; each assertion fails if its target
regresses (verify by temporary mutation during development, then revert).

### C7 — Scanner driver/processor split, Slice 1 (P2, phased — own branch/PR)
Execute `docs/superpowers/plans/2026-06-12-document-scanner-driver-slice1.md`
as written (it is already TDD-granular): privileged stateless
`scanner-driver` in the hardware namespace (hostPath `/dev`, no
`smarter-devices/bus-usb`), real `POST /scan` semantics (discrete-DPI 400/422
mapping), `ScanBatch`, session manager removal. Too large for this PR;
depends on C2 landing first so the current binary's behavior is pinned by
tests. Recommend a dedicated follow-up with opus/sonnet.

### C8 — `disc-detected`/`disc-ejected` schema events (P3, sonnet)
Per bead `archiver-9xw`: add to `notification-event.v1.1.schema.json` (or cut
v1.2 if additive rules require — the v1→v1.1 precedent is additive-in-place;
follow it), fixtures + tests, emit from the tender script (optional curl,
gated on relay reachability, non-fatal).
**Accept:** pytest green; schema version decision recorded in the file
header comment.

### H1 — Unblock CI (P1, sonnet, best-effort from here)
Per `archiver-850`: bump Go toolchain to ≥1.26.4 in `.gitlab-ci.yml` and both
`go.mod` files (`toolchain` directive) for GO-2026-5037; for the pypi SSL
failure, pin the index/CA per the job's error (reproduce what's reproducible:
`go vet`/`go test` locally). Cannot run the GitLab pipeline from this
environment — mark the packet done only up to "changes pushed, pipeline
validation pending operator".

### H2 — Tracker reconciliation (P2, needs `bd`)
Re-open or annotate: `archiver-emj.13/.17/.18/.19/.22/.6` (see architecture
review §4). File new beads for: B1–B7, A1–A3, C1–C6, C8, H1/H3, the NFS
`mountOptions` restoration, and the glovebox `bearerPort` migration
(blocking-on-operator). Link `discovered-from` this review.

### H3 — GitOps-side verification (P1, operator/cross-repo)
In `gitlab.orac.local/steve/gitops`: confirm NFD worker config exists and add
USB class 06 to `deviceClassWhitelist` (scanner rule can't fire without it);
confirm smarter-device-manager state matches what the chart still requests;
confirm the `name=openclaw-recognizer` namespace label is codified; confirm
Flux HelmRelease values pin chart ≥ the version carrying B1/B2 once
published; confirm Grafana dashboard delivery mechanism for C5.

## 3. Sequencing

```
Wave 1 (parallel): B2, B6, B7, A1, B5(gitignore)      — mechanical, unblock others
Wave 2 (parallel): B1, B3, B4, A2, C8                 — core security + client
Wave 3 (parallel): C1, C2, C3, C4, H1                 — wiring + observability
Wave 4:            C6 (locks everything in), C5
Later/elsewhere:   C7 (own PR), H2+H3 (operator)   [A3 filed 2026-08-22]
```

## 4. Explicitly out of scope here (so nothing is silently dropped)

- Anything requiring the physical hardware (cold-plug e2e `archiver-e1a`,
  flatbed acceptance `archiver-hee`).
- The gitops repo (H3) and GitLab pipeline execution (H1 validation).
- mTLS client certificates for glovebox — not required for `/v1/archives`
  today; revisit when glovebox extends mTLS to the bearer surface (tracked in
  the integration review §4.3).
- Phase 2/3 of plan.md (classification, OCR pipelines, book reconstruction,
  reMarkable delivery) — unstarted by design, awaiting Phase 1 closure.

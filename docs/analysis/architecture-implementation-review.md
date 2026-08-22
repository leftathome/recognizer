# Architecture implementation review — designed vs. built — 2026-08-22

**Scope:** compare what we said we'd build — `plan.md` (v1.0 draft,
2026-04-04) plus `docs/specs/01–04`, the superpowers design docs, and
`docs/optical-ingest-e2e-plan.md` — against what exists in this repo @
`68b6883` (chart 0.6.2), with a security assessment of how it was built.

**Framing:** `plan.md` describes an "openclaw-capture" repo with raw
`manifests/` + ArgoCD. The project was renamed archiver→recognizer and
re-platformed onto a Helm chart + GitLab CI + **Flux** (spec 02 §5 — "The
orac cluster is driven by Flux, not ArgoCD"). Several plan.md §9 deliverables
were deliberately relocated to the sibling gitops repo
(`gitlab.orac.local/steve/gitops`); those are marked *moved out of repo* —
correct by decision, but unverifiable from here and worth a one-time audit
against gitops.

**Companion documents:**
- [glovebox-integration-review.md](glovebox-integration-review.md)
- [action-plan.md](action-plan.md) — remediation work packets

---

## 1. Scorecard

| # | Feature area (design ref) | Status | One-line verdict |
|---|---|---|---|
| 1 | USB detection: NFD rules + device plugin (plan §3.1) | ⚠️ diverged / moved out of repo | Labels renamed to `recognizer.io/*`; NFD config lives in gitops; device plugin abandoned for optical (privileged hostPath instead); NFD whitelist missing USB class 06 so the scanner rule can't fire |
| 2 | Optical ripper (plan §4) | ✅ done (one gap) | Most mature area; ARM 2.23 config, tender/eject-guard beyond spec; **post-rip notification hook written+tested but never deployed** |
| 3 | Document scanner (plan §5; superpowers driver design) | ❌ skeleton only | Binary runs but writes no manifest, sends no notification, `/scan` is a stub; the designed driver/processor split (Slice 1) entirely unbuilt |
| 4 | Notification relay + dead-letter (plan §3.3) | ⚠️ partial | Relay solid and well-tested; **no dead-letter drain**, no metrics, ships with zero destinations, unauthenticated |
| 5 | Storage: NFS PV/PVC (plan §3.2) | ✅ done (extended) | Three-backend selector better than design; default `scratch` mode means ripper output is on a throwaway volume the importer can't read (known, `values.yaml:50-53`) |
| 6 | Cilium network policies (plan §3.4) | ❌ built but non-functional | **Selector matches zero pods**; no policy in the privileged hardware namespace; no egress rule for glovebox/Vault even if fixed |
| 7 | Secrets via ESO (plan §3.5) | ⚠️ diverged, sound | Vault, not 1Password (fine); no plaintext secrets in tree; **one credential retrievable from git history**; glovebox token via env var not file |
| 8 | Archive importer / Google Takeout (specs 01, 03) | ✅ done | Best code + best pod security posture in the repo; delivery is plaintext HTTP (see integration review) |
| 9 | walhelm health source (spec 04) | ✅ done, gated off | Same client, PHI payload, same plaintext transport; session correctly file-mounted |
| 10 | Monitoring (plan §8 Phase 1) | ❌ cosmetic | ServiceMonitor scrapes endpoints that don't exist; both alerts reference a metric/namespace that don't exist; no Grafana dashboard anywhere |
| 11 | CI/CD + GitOps (spec 02) | ✅ done / 🔴 currently red | Pipeline matches spec 02; **blocked since v0.5.0 tag** (`archiver-850`): chart 0.5.0 never published, source now at 0.6.2 |
| 12 | Web UIs (plan §5.7) | ⚠️ partial, all unauthenticated | ARM UI fronts a privileged pod; scanner "UI" is a JSON API; no auth on any HTTP surface in the chart |
| 13 | Tests | ⚠️ good units, zero chart tests | Every Go/Python package tested; **nothing renders the chart in tests**, which is exactly where the worst bugs live |

Plan.md §10 acceptance criteria: optical ~met (modulo notification), scanner
0/9 met, framework 1/5 met (secrets; not notifications-retry-in-anger, not
metrics, not network policy; ArgoCD superseded).

## 2. Detail by area

### 2.1 USB detection + device plugin — diverged
- Designed: NFD `deviceClassWhitelist` 08/ff, labels `openclaw.io/device-*`,
  smarter-device-manager for `sr*`/`sg*`/`bus/usb` (plan §3.1, repo layout
  `manifests/nfd/`, `manifests/device-plugin/`).
- Built: label vocabulary is now `recognizer.io/device-*`
  (`values.yaml:124,172`); NFD worker config lives in gitops. The optical
  path abandoned the device plugin for `privileged: true` + hostPath `/dev`
  (`optical-ripper/daemonset.yaml:138-143,200-207`) — a documented decision
  (`archiver-6ix`), contained by a dedicated `recognizer-hardware` namespace
  at PSS `privileged`. The scanner still requests the dead
  `smarter-devices/bus-usb: 1` resource (`values.yaml:119`), which the driver
  design doc explicitly declared unusable.
- Known gap: NFD whitelist lacks USB class 06 (Imaging) so the Epson rule
  cannot fire (`docs/optical-ingest-e2e-plan.md:86-88`). Beads:
  `archiver-5uh`, `archiver-9aj`, `archiver-6zu` (open/in-progress).

### 2.2 Optical ripper — done, one wiring gap
Chart and tender scripts exceed the plan (disc-present/readable probes,
eject-guard, ARM DB PVC, Renovate-pinned image `2.23.2`). Deliberate,
documented config divergences from plan §4.6 (ARM 2.23 config keys).

**Gap:** `images/optical-ripper/hooks/post_rip.py` (unit-tested, ~19 tests)
is referenced by **no template, ConfigMap, or Dockerfile** — the deployed pod
relies on ARM's own `NOTIFY_WEBHOOK`, which does not emit our
`notification-event` schema. `charts/recognizer/README.md:12` claims
otherwise. Also `post_rip.py:78` defaults to the dead
`capture`-namespace relay URL.

### 2.3 Document scanner — skeleton; design unmet
The Go session-manager packages (scan/session/manifest/notify/web) all exist
*with tests*, but `cmd/scanner/main.go` wires almost none of it:
- `OnClose` only logs — **no `manifest.json` is written, no notification is
  sent** (plan §5.5–5.6 unmet; beads `archiver-emj.17/.18` closed
  prematurely).
- `web.go` `handleScan` returns `{"status":"scan triggered"}` without
  scanning; `/settings` is a no-op; the literal `"epson-ds-1630"` is passed
  as the SANE device name (the exact bug the driver design doc opens with).
- No HTML UI exists (plan §5.7's status page/thumbnails/toggles) — JSON only.
- The mounted `/etc/scanner/config.yaml` is never read, and contains a
  stale+wrong relay URL (`capture` namespace, wrong service name), ignoring
  the `recognizer.relayUrl` helper.
- Slice 1 of the driver/processor split
  (`docs/superpowers/plans/2026-06-12-document-scanner-driver-slice1.md`):
  none of its acceptance criteria are met — DaemonSet still renders into the
  release namespace, no privileged/hostPath, still requests
  `smarter-devices/bus-usb`, `session.Manager` still present.

### 2.4 Notification relay — good core, missing the loop-closers
Retry ladder is exactly spec (5/30/120s), dead-letter writes are atomic,
schema validation against v1.1. But: **no dead-letter drain** (plan §3.3.3's
"CronJob or the relay itself periodically retries" — dead letters accumulate
forever); `destinations: []` as shipped (fan-out to nothing);
second-granularity dead-letter filenames silently overwrite in bursts; no
`/metrics`.

### 2.5 Storage — done
Three-backend selector (longhorn default / nfs / existing) with `keep`
policies and cross-namespace hardware volume. Divergences from plan §3.2 are
sensible (50Gi vs 10Ti; `/mnt/tank/recognizer`; no NFS `mountOptions` —
worth restoring `nfsvers=4.1,hard` when the NAS lands). The default
`hardware.data.mode: scratch` hand-off gap to the importer is known and
documented.

### 2.6 Network policy — the most important structural finding
`templates/networkpolicies.yaml` transliterates plan §3.4 but:
1. `endpointSelector` matches `app.kubernetes.io/part-of: recognizer`, a
   label `recognizer.labels` **never applies to any pod template** → the CNP
   selects zero endpoints. Under default-deny it strands the workloads; under
   default-allow it restricts nothing. Either way it is not what the chart
   claims.
2. Rendered only in the release namespace — the **privileged hardware
   namespace has no NetworkPolicy at all**.
3. No egress rules for glovebox:9091, Vault/ESO, or DNS-to-the-right-place
   even if the selector matched; no ingress section; no default-deny.
Also: glovebox's own policy needs the out-of-band
`kubectl label ns recognizer name=openclaw-recognizer` (documented in
values comments; failure mode is TCP timeouts).

### 2.7 Secrets — diverged (Vault, not 1Password), one historical leak
Six ExternalSecrets, all `vault-backend` ClusterSecretStore. No plaintext
secrets in the working tree. Findings:
- **`.beads/.beads-credential-key` is retrievable from git history**
  (untracked in `1ab0a0d` via `git rm --cached`, never purged). Treat as
  compromised: rotate, and purge history if the remote is shared.
- Glovebox tokens injected as env vars (readable in `/proc/<pid>/environ`,
  visible to `kubectl exec`); the walhelm *session* already demonstrates the
  correct read-only file-mount pattern.
- Ripper secrets (MakeMKV/OMDb) render into the privileged namespace;
  MakeMKV key additionally transits a root init-container shell and is
  duplicated as env `KEY` on the main container.
- `refreshInterval: 1m` on glovebox/walhelm secrets (1440 Vault reads/day
  each) vs 1h elsewhere.
- `.gitignore` has no `*.key` / `*.pem` / `.env` / `session.json` entries.

### 2.8–2.9 Archive importer + walhelm-fetch — done; transport is the issue
Both are genuinely complete (fixtures, e2e test faking both KP and the
glovebox tus server) with the best pod hardening in the chart (nonroot,
seccomp, drop-ALL, distroless). The transport findings — plaintext HTTP
carrying a bearer token and, for walhelm, PHI — are covered in the
[integration review](glovebox-integration-review.md) §5 and §7.

### 2.10 Monitoring — missing in substance
No application in this repo exposes `/metrics` (zero prometheus client
imports anywhere). The ServiceMonitor scrapes 404s; the
`NotificationDeadLetterBacklog` alert watches a metric nothing emits
(`capture_notification_dead_letter_total`); the crash-loop alert filters on
the dead `capture` namespace; the ServiceMonitor's `namespaceSelector` never
covers `recognizer-hardware`, so the one pod with a real web port (ARM) is
unscraped; no Grafana dashboard JSON exists (bead `archiver-emj.22` closed
against a file that isn't there).

### 2.11 CI/CD — matches spec, currently red
`.gitlab-ci.yml` implements spec 02 faithfully (kaniko, trivy, govulncheck,
kubeconform, OCI chart push on tags). Open problems: `archiver-850` —
pipeline blocked (stdlib CVE needs go1.26.4 + pypi SSL), **no chart published
since 0.5.0** while source is at 0.6.2; three TLS-verification bypasses
(`--skip-tls-verify-registry`, `helm --insecure-skip-tls-verify`,
`curl -sk` carrying `CI_JOB_TOKEN`) pending the cluster-CA fix
(`gitops-gr30`); amd64-only images (runner not privileged, no buildx —
`gitops-vney`).

### 2.12 Web surfaces + RBAC
All three Services are ClusterIP-only (no Ingress/Gateway anywhere — good),
but every HTTP surface is unauthenticated in-cluster, including ARM's UI in
the privileged namespace and the relay's writable `POST /event`. No
Role/RoleBinding (none needed), but no `automountServiceAccountToken: false`
either, so every pod carries a mountable SA token it doesn't use.

### 2.13 Tests — the missing layer is chart rendering
Unit coverage is genuinely good (relay, schemas, post_rip, every Go package,
one real e2e). **Zero tests render the chart**, which is precisely where the
highest-impact defects sit (CNP selector, `capture` namespace fossils,
missing securityContexts, ServiceMonitor namespaces). `helm lint` +
kubeconform cannot catch semantic mismatches.

## 3. Security findings, ranked

1. **PHI + bearer token over plaintext HTTP to glovebox** —
   `values.yaml:238,306`; `internal/delivery/client.go:149,173,226`; zero TLS
   code in the repo. Highest stakes on the walhelm path (health records).
2. **CiliumNetworkPolicy selects zero pods; privileged namespace has no
   policy at all** — the chart's containment story is currently fictional.
3. **Credential retrievable from git history** (`.beads/.beads-credential-key`
   at `1ab0a0d^`) — rotate + purge.
4. **Privileged hostPath-`/dev` DaemonSet** (accepted risk `archiver-6ix`,
   but compounded by: secrets co-located in that namespace, no NetworkPolicy,
   unauthenticated ARM UI fronting it).
5. **Relay: unauthenticated `POST /event` + synchronous 155s retry on a
   single-threaded server + attacker-influenced unbounded dead-letter writes
   to the shared PVC + burst filename collisions.**
6. **Scanner and relay pods have no securityContext** (would be rejected
   under the PSS `restricted` posture the values claim; relay image runs as
   root — no `USER` in Dockerfile).
7. **Glovebox token as env var** rather than mounted file.
8. **CI TLS bypasses** (three; `CI_JOB_TOKEN` over `curl -sk`).
9. **Stale `capture` namespace fossils in live config** — scanner ConfigMap
   relay URL, PrometheusRule namespace filter, `post_rip.py` default.
10. **No `automountServiceAccountToken: false`**; missing NFS mount options;
    `.gitignore` gaps — hygiene tier.

## 4. Bead-ledger reconciliation (tracker says done; repo says otherwise)

| Bead (closed) | Claim | Reality |
|---|---|---|
| `archiver-emj.13` | post-rip hook | Written + tested, never deployed |
| `archiver-emj.17/.18` | scanner manifest + notify | Written + tested, never called by `main.go` |
| `archiver-emj.19` | scanner web UI | JSON API only; no UI |
| `archiver-emj.22` | Grafana dashboard | File does not exist (any repo we can see) |
| `archiver-emj.6` | Cilium policies | Rendered, selects zero pods |
| `archiver-emj.4/.5` | NFD + device plugin | Moved to gitops (verify there); optical path abandoned the device plugin |

These should be re-opened or annotated via `bd` so the tracker reflects
reality (see action plan packet H2).

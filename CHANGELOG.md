# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.4.0] - 2026-05-22

### Added

- **Glovebox archive-delivery client** in `archive-importer`
  (archiver-5od). After the matcher pass, recognized subtrees are
  pushed to glovebox's tus.io `/v1/archives` endpoint per the team
  handoff doc. Bearer-token auth from a Vault-projected Secret
  (`secret/glovebox/ingest-tokens/<source-id>`). Per-subtree shape:
  raw bytes for `archive/mbox`, gzipped tar for
  `archive/google-takeout-subtree` and `archive/generic-tarball`
  (Meta exports). 32 MiB PATCH chunks with bounded retry on 5xx/429.
  Idempotent on re-run: manifest's new `deliveries[]` section tracks
  what succeeded, and `Orchestrator.DeliverAll` short-circuits any
  subtree whose `source_path` already has a `completed` record.
  Schema bumps to v1.1 when `deliveries` is non-empty.
- **Meta/Facebook export support in `archive-importer`** (archiver-bp9).
  Detection looks for `personal_information/profile_information/profile_information.json`
  with a `profile_v2` or `profile_information_v2` key at the archive
  root (no wrapper dir, unlike Google Takeout). Depth-1 matchers cover
  the 8 top-level dirs:
  `ads_information`, `apps_and_websites_off_of_facebook`, `connections`,
  `logged_information`, `personal_information`, `preferences`,
  `security_and_login_information`, `your_facebook_activity`.
  Verified against a real 332 MB Facebook export -- 8/8 recognized,
  0 unrecognized.

### Changed

- **`archive-importer` is now multi-provider.** `runZipImport`
  enumerates `GoogleTakeoutProvider` then `MetaProvider` and uses the
  first that detects. Hardcoded `archive/google-takeout` /
  `archive/google-takeout/unrecognized-subtree` media_types and the
  literal `Takeout/` path prefix in the manifest's unrecognized subtree
  records are replaced with `provider.UmbrellaMediaType`,
  `provider.UnrecognizedSubtreeMediaType()`, and the
  detect-result-relative path so Meta exports record bare
  category-dir names instead of `Takeout/<category>`.

### Chart

- New `archiveImporter.gloveboxIngest.{enabled,url,sourceID,vault.*}`
  Values. When `enabled: true`, the chart renders an ExternalSecret
  targeting `glovebox/ingest-tokens/<sourceID>` plus the
  `GLOVEBOX_INGEST_{URL,TOKEN,SOURCE_ID}` env on the archive-importer
  CronJob. Default `false` keeps current behavior.

### Operator note

- The recognizer namespace must carry the label `name: openclaw-recognizer`
  for glovebox's NetworkPolicy to admit traffic on port 9091. This is a
  namespace-level label (separate from `kubernetes.io/metadata.name`)
  and is set out-of-band: `kubectl label ns recognizer name=openclaw-recognizer`.

## [0.3.4] - 2026-05-21

### Changed

- **`archive-importer` pod and container security contexts** now satisfy
  the PSS `restricted` baseline. Pod sets `runAsNonRoot: true` and
  `seccompProfile.type: RuntimeDefault`; container sets
  `allowPrivilegeEscalation: false` and `capabilities.drop: ["ALL"]`.
  Eliminates four admission warnings per Job pod.
- **`optical-ripper` `chown-home` init container** now seeds
  `/home/arm/.MakeMKV/settings.conf` with the registration key from the
  `makemkv-license` Secret before ARM starts. The `arm-home` volume is
  an `emptyDir`, so without this the key was wiped on every pod restart
  and MakeMKV reverted to the "version too old" failure path.

### Fixed

- `images/archive-importer/scripts/run-job.sh` strips `.mbox` /
  `.tar.gz` / `.7z` (not just `.zip`) before normalizing the stem,
  drops `.` from the allowed character set entirely, and truncates the
  normalized stem so the resulting Job name fits inside k8s's 63-char
  label limit. The 12GB Google Mail mbox would otherwise fail Job
  creation with a label-length validation error.

## [0.3.3] - 2026-05-21

### Changed

- `archive-importer` relay client wraps 4xx responses with both the
  response body and the event JSON payload to make schema validation
  failures diagnosable from pod logs.

## [0.3.2] - 2026-05-21

### Fixed

- `notification-relay/relay/validate.py` now loads
  `notification-event.v1.1.schema.json`. Without this update the v1.1
  events the importer emits (with the looser `media_type` pattern and
  new `source` / `event_type` enums) were rejected with HTTP 400.
- `archive-importer` event payloads include `node_name`, populated from
  the Downward API via a `NODE_NAME` env on the CronJob template.
  Required by both v1.0 and v1.1 schemas; earlier builds omitted it.

## [0.3.1] - 2026-05-21

### Fixed

- `recognizer.relayUrl` helper renders `/event` (the only path the
  notification-relay accepts) instead of `/notify`. Every importer
  event was hitting 404 in-cluster before this fix.

## [0.3.0] - 2026-05-21

### Added

- **Standalone `.mbox` support in `archive-importer`**. Google Takeout
  splits Mail into a separate `.mbox` file alongside the zip volumes
  when the export is too large to fit inside a zip; the importer now
  dispatches on file extension at `ingest` time. `.zip` keeps the
  existing unpack-and-walk flow; `.mbox` hashes + moves the file into
  `unpacked/<id>/`, emits a single `archive/google-takeout/mail` event
  pointing at the moved file, and writes a one-entry manifest. Same
  archive_id / idempotency semantics as zip imports. Glovebox's
  mbox-importer (spec 09) is the downstream consumer for per-message
  parsing.
- **`archive_format: "none"`** in
  `archive-layout-manifest.v1.schema.json` for raw-file deliveries
  (additive enum extension; v1.0 documents written before this still
  validate).
- **33 new Takeout subtree matchers** covering Alerts, Android Device
  Configuration Service, Assignments (Google Classroom), Blogger,
  Chrome, Discover, Flow, Gemini, Google Account, Google Ads, Google
  Business Profile, Google Feedback, Google Finance, Google Meet,
  Google Pay, Google Play Books, Google Play Movies & TV, Google Play
  Store, Google Product Surveys, Google Shopping, Google Store, Google
  Wallet, Google Workspace Marketplace, Groups, Home App, Maps, Nest,
  News, Profile, Saved, Search Contributions, Search Notifications,
  Workspace Studio. Brings total recognized Takeout subtrees from 14
  to 47.

### Changed

- **Matcher fingerprints now default to "any non-hidden entry"**.
  Canonical Takeout subtree names are unambiguous once the provider
  matched on `Takeout/`; the per-service `anyFileMatching(*.mbox)` /
  `anySubdirOf(...)` checks were over-narrow and falsely dropping real
  exports into `unrecognized`. Particular fix: YouTube no longer
  requires `videos/` or `playlists/` -- exports with just
  `subscriptions/` and `history/` now match.
- **Single-pass SHA-256 for fresh imports.** Previously the importer
  hashed each source twice (once for `archive_id` derivation, once
  for `manifest.source.sha256`). `ident.HashFile` + `ident.DeriveID`
  split the work cleanly and main.go reuses the single result.
  Confirmed: a 12 GB Takeout mbox now ingests in ~10s of recognizer
  work (plus whatever filesystem copy time the operator pays before
  invoking the binary).

### Fixed

- **Unpacker rejected legitimate filenames with `..` mid-name**. The
  redundant `strings.Contains(name, "..")` check refused entries like
  `RackStation RS2423+ _ Synology Inc..html`. Dropped; `filepath.IsLocal`
  already handles every real path-traversal case.
- **`scripts/run-job.sh` accepted RFC 1123-invalid Job names** when
  the source archive's filename had uppercase characters (Takeout
  filenames carry uppercase `T` and `Z` from their ISO timestamp
  format). The script now lowercases + sanitizes the stem.

## [0.2.5] - 2026-05-21

### Changed

- Bumped `opticalRipper.image.tag` to `2.23.2` (was `2.21.0`). 2.21.0
  shipped MakeMKV 1.18.2 which is now past its 60-day "this version is
  too old" rolling grace window; current ARM releases bundle a newer
  MakeMKV that the rotating free beta key from forum.makemkv.com
  re-activates.

## [0.2.4] - 2026-05-21

### Fixed

- **archive-importer pod permission denied** writing to the data PVC.
  Distroless base ships as `nonroot` (uid 65532), but the shared data
  PVC is owned by uid 1000 (the optical-ripper init container is the
  first writer). Pods failed with
  `mkdir /data/incoming/archives/unpacked: permission denied`. Pod
  now runs as uid:gid 1000:1000 with `fsGroup: 1000` so writes
  succeed and any files created stay group-readable by ARM and the
  scanner/relay containers.
- **`scripts/run-job.sh` rejected Google Takeout filenames**. Google's
  filenames carry uppercase `T`/`Z` from their timestamp format (e.g.
  `takeout-20260411T180338Z-11-001.zip`), but Kubernetes object names
  must be lowercase RFC 1123 subdomains. The script now normalizes the
  stem (lowercase + non-alphanumeric → `-`).

## [0.2.3] - 2026-05-21

### Fixed

- **optical-ripper's `NOTIFY_WEBHOOK` pointed at the wrong service**.
  Was hardcoded to
  `http://notification-relay.capture.svc.cluster.local:8080/event`
  (wrong namespace, missing release prefix, wrong path). Switched to
  the `recognizer.relayUrl` helper (D1 of spec 03 plan) so it renders
  the actual in-cluster Service URL.

## [0.2.2] - 2026-05-21

### Fixed

- **ARM bailed at startup on the permission check for `/out/video`**.
  ARM's wrapper checks (without creating) that
  `COMPLETED_PATH` / `AUDIO_COMPLETED_PATH` / `DATA_COMPLETED_PATH`
  already exist and are writable; the data PVC was empty on first
  use. The `chown-home` init container now `mkdir -p`s
  `/out/{video,audio,data}` and chowns them to 1000:1000.

## [0.2.1] - 2026-05-21

### Fixed

- **notification-relay crashed on arm64 nodes** with
  `exec /usr/local/bin/python: exec format error`. Our kaniko CI is
  amd64-only, so the Deployment now pins `nodeSelector:
  kubernetes.io/arch=amd64`. Lift this once images ship multi-arch
  manifests.

## [0.2.0] - 2026-05-20

The big feature release: the **archive-importer** workload arrives,
implementing spec 03 end-to-end against Google Takeout archives.

### Added

- **`archive-importer` workload** (suspended `CronJob` template +
  `ConfigMap` + `ServiceAccount`). Consumers promote the CronJob to a
  one-off Job per archive via `scripts/run-job.sh <filename>.zip`.
  Honors `archiveImporter.config.{dataRoot,relayUrl,includeUnrecognized,logLevel}`.
- **Go binary** at `images/archive-importer/cmd/archive-importer` with
  seven internal packages (ident, unpacker, matcher, manifest, relay,
  lock, plus the matcher's Google Takeout provider). 60 unit + 5
  end-to-end integration tests; deterministic event IDs (sha256 of
  archive_id|media_type|output_path) so re-runs reuse the same IDs.
- **JSON Schemas**: `notification-event.v1.1.schema.json` (additive
  over v1.0; adds `archive-*` source/event_type values + `archive/*`
  media-type pattern) and `archive-layout-manifest.v1.schema.json`
  (the sidecar the importer writes next to the unpacked tree).
- **CI jobs**: `test:go:archive-importer`, `vuln:go:archive-importer`,
  `build:archive-importer` (kaniko). `vuln:go` is now parameterized
  on `GO_MODULE_DIR` so future modules cost one extends-block.
- **`recognizer.relayUrl` chart helper** for in-cluster default URLs.

### Changed

- `vuln:go` no longer hardcodes the document-scanner module path.

## [0.1.5] - 2026-05-20

### Fixed

- **ARM bailed on the ownership check for `/etc/arm/config`**.
  ConfigMap volume mounts are root-owned and read-only, so fsGroup
  alone can't satisfy ARM's strict uid-1000 check. `arm-config` is
  now a writable `emptyDir`; the `chown-home` init container copies
  the ConfigMap contents into it and chowns to 1000:1000.

## [0.1.4] - 2026-05-20

### Fixed

- **ARM container CrashLoopBackOff on `/home/arm` ownership**.
  `emptyDir` mounts default to root:root; ARM's entrypoint refused to
  start with
  `[ERROR]: ARM does not have permissions to /home/arm using
   1000:1000 ... Folder permissions--> 0:0`. Added
  `pod.securityContext.fsGroup: 1000` and a `chown-home` init
  container that chowns `/home/arm` + `/out` to 1000:1000.

## [0.1.3] - 2026-05-20

### Fixed

- **optical-ripper image tag was non-existent**. `2.6.0` was never
  published on Docker Hub (the 2.6.x line starts at 2.6.42). Bumped
  to `2.21.0` (recent stable, multi-arch).
- **CI `package:chart` job rejected all-digit short-SHA branch builds**
  with `Error: Version segment starts with 0`. SemVer treats pure-digit
  pre-release identifiers as numeric and forbids leading zeros, so a
  short SHA like `04863033` blew up. Branch builds now use
  `0.0.0-sha<short-sha>` (alphanumeric).

## [0.1.2] - 2026-05-20

### Fixed

- **chart's ExternalSecrets referenced a `onepassword-connect`
  ClusterSecretStore that doesn't exist in this homelab**. Switched
  all three to `vault-backend` with paths under `eso/recognizer/*`.
  Vault setup: `vault kv put secret/eso/recognizer/{makemkv,omdb,
  notification-relay} <key>=<value>`.

## [0.1.1] - 2026-05-19

### Added

- Chart README, `NOTES.txt`, and prerequisites header in `values.yaml`
  documenting required cluster components (NFD, smarter-device-manager,
  External Secrets Operator, StorageClass, image pull credentials).

## [0.1.0] - 2026-05-19

The first release published to `registry.orac.local`. Supersedes the
2026-04-05 dev-only milestone (never published anywhere; entries below
cover both the original feature work and the GitLab/Helm migration).

### Added

- **Helm chart** `charts/recognizer/` packaging all three workloads
  (document-scanner, notification-relay, optical-ripper) plus shared
  resources (namespace, NetworkPolicies, monitoring, capture-data
  PV/PVC).
- **GitLab CI pipeline** (`.gitlab-ci.yml`): test, build, package, release
  stages. Multi-stage with kaniko-based image builds (amd64 only for now)
  and OCI Helm chart push to `registry.orac.local`.
- **Storage backend selector**: `values.storage.backend` chooses between
  `longhorn` (default; dynamic RWX provisioning), `nfs` (static PV against
  an external NFS export), and `existing` (bring-your-own PVC).

### Changed

- Migrated build + release pipeline from GitHub Actions + GHCR to GitLab
  CI on `gitlab.orac.local`. Images now published to
  `registry.orac.local/steve/recognizer/{document-scanner,notification-relay}`.
  Chart published to `oci://registry.orac.local/steve/recognizer/charts/recognizer`.
- Renamed `openclaw.io/device-*` NFD labels to `recognizer.io/device-*`
  and `app.kubernetes.io/part-of: capture-framework` to `recognizer`
  (vestigial labels from before the project split off).
- `values.nfs.*` → `values.storage.*` with the new backend selector.
  Workload templates now mount `<release>-data` (formerly `-nfs`) via a
  `recognizer.dataClaimName` helper.

### Removed

- `manifests/` directory (replaced by the Helm chart).
- `.github/workflows/` (replaced by `.gitlab-ci.yml`).
- `tests/test_manifests.py`, `tests/test_layer1_manifests.py`,
  `tests/test_document_scanner.py`, `tests/test_optical_ripper.py` --
  these tested the static `manifests/` files; that role is now served
  by `helm:lint` + `kubeconform` in CI against the chart's rendered
  output.

### Original feature set (from the 2026-04-05 dev milestone)

### Added

- **USB hotplug capture framework** for Kubernetes: automatic workload scheduling when USB devices are plugged into cluster nodes
- **Notification relay service** (Python): validates events against JSON Schema, fans out to configured webhook destinations with retry and dead-letter handling
- **Document scanner session manager** (Go): SANE wrapper, session lifecycle state machine (ADF + flatbed modes with idle timeout), manifest.json generation, notification dispatch, web UI
- **Optical disc ripper integration**: ARM (Automatic Ripping Machine) ConfigMap, DaemonSet, post-rip notification hook with disc type mapping
- **Kubernetes manifests**: namespace, NFS PV/PVC, NFD custom USB rules, Smarter Device Manager config, Cilium network policies, ExternalSecrets for 1Password, DaemonSets + Services for all workloads
- **JSON Schemas**: notification-event.v1 and scan-session-manifest.v1 with strict validation (additionalProperties: false)
- **Monitoring**: Prometheus ServiceMonitor, alert rules (dead-letter backlog, crash-loop), Grafana dashboard
- **CI pipeline**: GitHub Actions with Python + Go test jobs, multi-arch Docker builds (amd64/arm64), security scanning (govulncheck, Trivy)
- **205 tests**: 156 pytest (schema validation, manifest content, kubeconform, relay integration, post-rip hook) + 49 go test (SANE wrapper, session lifecycle, manifest generation, notification dispatch, web UI)

# Document Scanner — Driver/Processor Split & Slice 1 (Real Device Access)

- **Status:** Draft (design approved 2026-06-12, pre-implementation)
- **Date:** 2026-06-12
- **Hardware:** Epson DS-1630 flatbed + ADF, USB `04b8:015c`, attached to node `johnny`
- **Related issues:** `archiver-9aj` (chart wiring for host device events), `archiver-hee` (e2e flatbed/scanner acceptance test)
- **Related memory:** `bd memories ds1630` (`ds1630-sane-capabilities`, `ds1630-button-trigger`)

---

## 1. Background

The `images/document-scanner/scanner-session-manager` (Go) is a working **skeleton**: a
`scanimage` wrapper with fixed params, a session lifecycle manager (ADF vs flatbed,
idle timer, duplex flag), and an HTTP API. But:

- The `/scan` endpoint is a **stub** — it returns `"scan triggered"` without scanning.
- The web handler's device identifier is the literal string `"epson-ds-1630"`, which is
  **not** a valid SANE device name.
- The Helm chart deploys document-scanner into `.Release.Namespace` with **no privileged
  securityContext and no `/dev` passthrough**, so the pod cannot reach the USB scanner.
- None of the "intelligent" capabilities (auto-crop, color/photo classification,
  auto-size, preview-analyze) exist.

This spec was preceded by a **live hardware probe** (a throwaway privileged debug pod on
`johnny`). All capability claims below are empirically confirmed, not assumed.

## 2. Empirical hardware findings (ground truth)

Driven entirely by the **open-source `epsonds` SANE backend** (ESC/I-2) in stock Debian
`sane-utils` 1.1.1 — **no proprietary `epsonscan2` driver required** (the Dockerfile's
`epsonscan2` lines are correctly commented out).

| Capability | Finding |
|---|---|
| Device | `epsonds:libusb:NNN:NNN`; `scanimage -L` enumerates it (alive/ready check). |
| Sources | `Flatbed`, `ADF Front`, `ADF Duplex`. |
| Duplex | Single-pass confirmed: 1 sheet → 2 images via `--batch`. **Back side emerges rotated 180°.** |
| Modes | `Lineart`, `Gray`, `Color`; depth 1/8-bit. |
| Resolution | Discrete: 50, 75, 100, 150, 200, 240, 300, 360, 400, 600, 1200 dpi. |
| Geometry | mm; flatbed max 215.9×297.18, ADF max 215.9×393.7. |
| Empty feeder | Instant `rc=7` "Document feeder out of documents" (0.13s, no motor) — cheap, non-destructive empty-detect; also the clean end-of-batch signal. |
| HW auto-crop/deskew | `--adf-crp` / `--adf-skew` are **exposed but inactive/unsupported** on this model → must be done in **software**. |
| Size / color / photo-vs-doc detection | **Not** hardware features → require a scan + pixel analysis. Flatbed allows preview→analyze→commit; ADF is single-pass (scan-once-then-postprocess). |
| Scan button | Not exposed via SANE. Emitted on vendor USB **Interrupt-IN endpoint EP `0x83`** (8-byte packets): `SCAN → 01 01 00 00 00 00 00 00` (decoded). Red/Stop button is silent at idle (mid-scan abort, firmware). |

## 3. Target architecture: driver / processor split

Two pods, split on the privilege boundary:

```
scanner-driver  (PRIVILEGED)                 scanner-processor (UNPRIVILEGED)
ns: recognizer-hardware                       ns: recognizer (PSS=restricted)
node-pinned to johnny           <--- HTTP --- replicable, user-facing (ingress)
privileged + hostPath /dev          API
STATELESS thin SANE wrapper:                  owns sessions, document grouping,
  - detect device                             image analysis (crop/deskew/rotate,
  - POST /scan (params -> raw files)          color/photo/size classify), OCR,
  - GET /status (device present)              web UI; orchestrates smart scans
  deps: sane-utils only                       by calling the driver's /scan
        |  writes raw scans                           | reads raw, writes derived
        +------------->  shared data PVC  <------------+
```

**Rationale.** Least privilege: the privileged blast radius shrinks to a thin,
rarely-changing SANE wrapper. All code that grows and churns (OpenCV, OCR,
classification, the web UI — the bulk of the attack surface) runs unprivileged,
replicable, restartable, node-agnostic. The handoff PVC already exists (both mount
`/out`). The driver API is internal ClusterIP; users never touch the privileged pod.
This mirrors the existing optical-ripper (privileged hardware) → importer (unprivileged
processing) precedent.

**Session placement:** the driver is stateless. All session/document-grouping, idle
timers, and "new document" boundaries live in the processor (decided 2026-06-12).

## 4. Slice 1 scope — the deployed driver scans the DS-1630

The first buildable slice. There is no analysis code yet, so this is "stand up the thin
driver properly." It unblocks everything else.

### 4.1 Chart (mirror optical-ripper)

All hardware-specific edits below MUST be gated on `{{- if .Values.hardware.enabled }}`,
exactly as `templates/optical-ripper/daemonset.yaml` does. Applying `privileged: true`
while the pod renders into the PSS=restricted release namespace gets it **rejected by
PodSecurity admission**; the conditional is what keeps the legacy single-namespace path
(`hardware.enabled=false`) valid. `hardware.enabled` defaults true, so the happy path
works, but the conditional is mandatory, not cosmetic.

- `templates/document-scanner/{daemonset,configmap,service}.yaml`: namespace
  `.Release.Namespace` → `{{ include "recognizer.hardwareNamespace" . }}` (the helper
  already falls back to the release namespace when `hardware.enabled=false`).
- `daemonset.yaml`, **inside `if .Values.hardware.enabled`**: add container
  `securityContext.privileged: true`; add a hostPath `/dev`→`/dev` volume + mount; and
  **omit** the `smarter-devices/bus-usb` limit. The dead per-device `smarter-devices`
  resource names are dynamically numbered and never match; privileged + hostPath `/dev`
  is what works. Mirror optical-ripper's structure (which conditionally *re-adds*
  `smarter-devices/sr0|sg0` only in legacy mode) — i.e. the smarter-devices limit is
  conditional, not unconditionally deleted.
- Unchanged: nodeSelector `recognizer.io/device-scanner=epson-ds-1630`, `/out` PVC mount,
  `/etc/scanner` configmap.

**Coupling (N2):** placing document-scanner in the hardware namespace requires
`hardware.enabled=true`, which simultaneously drives the namespace helper and the
existence of the hardware-namespace data PVC (`templates/hardware-data.yaml`).

### 4.2 Driver app (Go, `scanner-session-manager` → driver role)

- **Device resolution:** call `scan.DetectDevice` **per request** in both `handleScan`
  and `handleStatus` so the real `epsonds:libusb:…` string is resolved freshly (absorbs
  USB path churn on replug, §6). This removes the `"epson-ds-1630"` literal-as-device
  bug; the `device string` param to `NewHandler` and the `SCANNER_DEVICE_NAME` env
  default in `cmd/scanner/main.go` become vestigial and are dropped (N3).
  (`DetectDevice`'s in-code comment says `epsonscan2`, but its `strings.Contains(line,
  "epson")` match correctly catches the `epsonds:` device string — no change needed
  there.)
- **`scan` package:** keep `ScanPage` (flatbed single). Add `ScanBatch` for ADF with a
  **distinct arg builder** — `BuildArgs` always emits `--output-file`, which is wrong for
  batch; `ScanBatch` must emit `--batch=<pattern>` instead, where `<pattern>` is a
  `printf`-style template with a `%d` page counter (e.g. `page_%02d.tiff`). End-of-feed
  detection: see §4.4 (stderr substring + produced-file count, not a raw exit code).
  Returns the ordered list of produced files.
- **`web.handleScan`:** replace the stub with a **synchronous** handler.
  - Request: `POST /scan` `{ "source": "...", "mode": "...", "resolution": N }`.
  - Validate against the real enums: `source ∈ {Flatbed, ADF Front, ADF Duplex}`,
    `mode ∈ {Lineart, Gray, Color}`, and `resolution ∈ {50, 75, 100, 150, 200, 240,
    300, 360, 400, 600, 1200}` — a **discrete set membership test**, not a range
    (e.g. 250 is in-range but invalid → 400). Any unknown value → `400`.
  - Route: flatbed → single page (`ScanPage`); ADF → batch (`ScanBatch`). Write raw
    **TIFF** to a per-scan dir under the PVC (naming in §4.6).
  - Duplex side labelling: ADF Duplex emits front,back,front,back…; label odd=front,
    even=back. Other sources → `single`. (Validated for a single sheet, §2; multi-sheet
    ordering is a Slice 2 assumption to verify — N5.)
  - Response (200): `{ "device": "...", "output_dir": "...", "pages": [ {"filename":"...","side":"..."} ] }`.
- **`web.handleStatus`:** report `device_present` (bool, from per-request `DetectDevice`)
  and the resolved device string. Drop the `current_session`/`recent_sessions` fields
  from the status response struct (they reference the removed session manager). (Feeder-
  loaded sensing deferred — confirming "loaded" means *pulling* the sheet.)
- **Remove the persistent session manager from the driver (C1).** The `session` package
  (grouping/idle/document-boundary) is a processor concern. Concretely:
  - Delete the `/session/close` and `/session/new-document` routes and their handlers
    (`handleCloseSession`, `handleNewDocument`).
  - Decide `/settings`: it is currently a no-op stub → **drop it** for Slice 1 (re-add in
    the processor if needed).
  - Change `NewHandler` to drop the `sessions *session.Manager` parameter; remove
    `session.NewManager(...)` construction and its `OnClose` wiring from
    `cmd/scanner/main.go`; update `web/web_test.go` accordingly.
  - The `session` package may be deleted or left dormant (unreferenced); deleting is
    cleaner. "Stateless driver" means **no persistent session.Manager** — only
    per-request, function-local bookkeeping (the produced-file list used to build the
    response) remains.
- **Keep `/healthz`** unchanged.
- **Forward-compat note:** keep the binary structured so a later background button-watcher
  goroutine (libusb EP `0x83`) and USB-interface coordination can be added without
  reshaping the driver.

### 4.3 Dockerfile

- Remove `imagemagick` from the driver image (belongs to the processor). Keep
  `sane-utils`, `curl`, `ca-certificates`. `epsonscan2` lines stay commented.

### 4.4 Error handling

The empty-feeder vs. real-failure discriminator must be **testable through the existing
`scan.Commander` mock**, which returns `(stdout, stderr, err)` and exposes **no exit
code** (synthesizing an `*exec.ExitError` with `ExitCode()==7` is impractical). Therefore
do **not** branch on a raw `rc=7`. Instead:

- **Empty feeder** = the command errored AND `stderr` contains the substring
  `"out of documents"` AND **zero** output files were produced → `422`.
- **Clean end-of-batch** (ADF) = same `"out of documents"` stderr but **≥1** file already
  produced → success (200), not an error.
- **Any other non-nil error** (stderr without that substring) → `500`.

This keys behavior on stderr text + produced-file count, both drivable by the mock.

| Condition | Response |
|---|---|
| No device found (`DetectDevice` fails) | `503 {"error":"scanner not found"}` |
| ADF source, empty feeder ("out of documents" + 0 files) | `422 {"error":"feeder empty"}` |
| Invalid source/mode/resolution | `400 {"error":"..."}` |
| `scanimage` failure (other stderr) | `500 {"error": "...", "stderr": "..."}` |
| Long ADF batch | generous, **configurable** scan timeout (env, minutes-scale) |

### 4.5 Testing

- **Unit** (existing mockable `Commander`): `ScanBatch` arg-building (emits `--batch=`,
  not `--output-file`) + end-of-batch parsing (stderr `"out of documents"` + file count,
  per §4.4); `handleScan` validation (discrete dpi set membership), source routing,
  duplex side labelling, error mapping (400/422/500/503).
- `go vet` + `staticcheck` (per repo Go standards).
- **In-cluster acceptance** (per "test it in a container"): rebuild the image via CI and
  deploy the chart. The scan step is a **manual gate** — it requires a human at `johnny`
  to physically load (and separately empty) the ADF, so it cannot be fully CI-automated.
  Talos nodes have **no SSH**; drive the test via `kubectl exec <pod> -n
  recognizer-hardware -- curl -s localhost:8080/scan ...` (curl is deliberately retained
  in the driver image, §4.3) or `kubectl port-forward`. Loaded → `ADF Duplex` writes 2
  TIFFs to the data PVC; empty feeder → `422`. Satisfies `archiver-hee`; the chart wiring
  satisfies `archiver-9aj`.

### 4.6 Output path / naming

- Per-scan directory under the PVC. Default output root: replace the vestigial
  `SCANNER_OUTPUT_DIR=/out/scans/sessions` (the "sessions" name is dead once sessions are
  stripped) with `/out/scans` as the base.
- Each `/scan` request creates a unique subdir, e.g. `/out/scans/<UTC-timestamp>-<rand>`
  (reuse the existing ID scheme: `20060102-150405-<3 random bytes hex>`), avoiding
  collisions on concurrent/rapid scans.
- Files within: the `ScanBatch` pattern `page_%02d.tiff` (ADF) or `page_01.tiff`
  (flatbed single). `output_dir` in the 200 response is the absolute subdir path; each
  `pages[].filename` is the basename, so the full path is `output_dir + "/" + filename`.

## 5. Out of scope (later slices)

- **Slice 2 — Processor/brain (unprivileged):** software auto-crop (cream-vs-gray bbox),
  deskew, duplex 180°-rotate, color/photo-vs-document classification, auto-size, flatbed
  preview→analyze→commit loop, session/document UX, web UI, OCR, format selection.
- **Slice 3 — Trigger sources:** plug events (NFD/udev) + Scan-button trigger (libusb
  watcher on EP `0x83` decoding `01 01…`, releasing the interface during scans) +
  feeder/readiness-on-connect. Stop-button mid-scan abort optional.

  **Cross-boundary trigger flow (key design point).** Button events are received in the
  **driver** (only it can read EP `0x83`), but the driver is stateless and has **no scan
  policy** — it does not start scans on its own. On a press it only **publishes a
  "scan-requested" event**; the **processor** (which owns sessions + scan policy)
  consumes it, decides params (feeder loaded? `ADF`:`flatbed`; mode/dpi/current UI
  settings), and **calls back into the driver's existing `POST /scan`**, then reads the
  produced files from the shared PVC and folds them into the active session. This means
  **one scan path** — button press, API call, and future web-UI click are all just
  triggers converging on `POST /scan` → files on PVC → response paths.
  - **Event transport (DECIDED — option A, 2026-06-12):** the driver's button-watcher
    **HTTP POSTs a `scan-requested` event directly to the processor** (e.g.
    `POST <processor>/triggers/scan`). This reuses recognizer's existing house idiom —
    publish an event JSON via outbound POST — and even the scanner's existing `notify`
    package plumbing (`notify.Send`), but aims at the processor directly rather than the
    notification-relay. Rationale: a button press is a **point-to-point command to one
    consumer** (the processor), whereas the notification-relay is a **blind fan-out to
    all configured destinations** (`fanout.py` has no per-`event_type` routing), so
    routing a control event through it would spam user-notification sinks
    (Discord/Pushover) with `scan-requested`. The relay stays for what it's good at:
    broadcasting user-facing `scan-started`/`scan-complete` notifications. Command and
    notification planes stay separate.
    - *Rejected alternative B:* add `event_type` subscription/routing to the relay and
      route `scan-requested` only to the processor (one unified event plane). More work;
      makes the relay a control-plane dependency on the scan path. Revisit only if a
      unified event plane becomes desirable.
    - *Rejected (off-idiom):* SSE/stream subscription — recognizer's pattern is
      push-POST outbound, not subscriber-pull.
  - **Interface coordination:** EP `0x83` shares the USB interface SANE claims during a
    scan, so the driver internally pauses the watcher / releases the interface for the
    duration of a `/scan` and resumes after — invisible to the processor.

## 6. Risks / open questions

- **Async vs sync at scale:** Slice 1 is synchronous for simplicity/verifiability. Long
  ADF runs hold the HTTP connection; revisit with async/job model when the processor +
  UI arrive (Slice 2).
- **Device path churn on replug:** USB `libusb:NNN:NNN` can change. Detecting per request
  (not caching at startup) absorbs this.
- **Driver/watcher interface contention (future):** EP `0x83` shares the interface SANE
  claims during a scan; the Slice 3 watcher must release during scans. Noted now so
  Slice 1 structure doesn't preclude it.
- **PVC mode `scratch` blocks the Slice 2 handoff (N1):** `hardware.data.mode` defaults
  to `scratch`, in which the release-namespace processor **cannot read** the driver's
  `/out` (per `values.yaml` / `hardware-data.yaml`). Not a Slice 1 blocker — criterion 2
  only needs TIFFs on the hardware-namespace PVC — but the Slice 2 cross-namespace
  processor handoff requires switching to `nfs` mode. Flagged so it isn't a surprise.
- **RBAC / NetworkPolicy (N4):** the driver needs no Kubernetes API access and there are
  no NetworkPolicy templates in the chart, so nothing is required for Slice 1. A future
  default-deny NetworkPolicy would affect the Slice 2 cross-namespace processor→driver
  path.

## 7. Acceptance criteria (Slice 1)

Automated (CI / `helm template` / `go test`):

1. With `hardware.enabled=true`, `helm template` renders document-scanner into
   `recognizer-hardware` with `privileged: true` + hostPath `/dev` and **no**
   `smarter-devices/bus-usb` limit; with `hardware.enabled=false` it renders into the
   release namespace **without** `privileged`/hostPath (PSS=restricted-safe).
3. `GET /status` (unit + live) returns `device_present` and, when present, the
   `epsonds:libusb:…` string. Invalid `/scan` params → `400`; missing device → `503`;
   empty-feeder simulation (stderr `"out of documents"` + 0 files) → `422`.
5. Unit tests + `go vet` + `staticcheck` pass; the code no longer references a
   `session.Manager`; the driver image contains `sane-utils` and **not** `imagemagick`.

Manual gate (human at `johnny`, via `kubectl exec ... curl` / port-forward — no SSH):

2. With a sheet loaded in the ADF, the deployed (non-debug) pod resolves the device and
   `POST /scan {source:"ADF Duplex",mode:"Color",resolution:300}` writes 2 TIFFs to the
   data PVC and returns their paths + `front`/`back` labels.
4. With the feeder empty, the same request returns `422`.

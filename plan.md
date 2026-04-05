# USB hotplug workload framework

## Product specification

**Version:** 1.0 draft
**Date:** 2026-04-04
**Author:** Steve (with Claude)

---

## 1. Overview

### 1.1 Problem statement

Physical media capture — ripping optical discs, scanning paper documents — requires dedicated hardware that is used intermittently. Rather than running always-on services for devices that may only be connected for a few hours at a time, we want a framework where plugging a USB device into any node in a Kubernetes cluster automatically schedules the appropriate workload on that node, and unplugging the device tears it down.

### 1.2 Core pattern

**USB hotplug → node label → pod scheduling → capture → write to NAS → notify downstream**

This pattern is shared across all device types. The framework provides the detection and scheduling layer; each device type provides a purpose-built workload container. The initial implementations are:

1. **Optical disc ripper** — Pioneer BDR-XS07UHD external Blu-ray drive
2. **Document scanner** — Epson DS-1630 flatbed/ADF scanner

The architecture is designed to be extensible to future USB-attached capture devices.

### 1.3 Design principles

- **Ephemeral workloads**: Pods exist only while their device is connected. No resource waste when devices are unplugged.
- **Unattended operation**: Once configured, the system requires no interaction beyond inserting a disc or placing a document on the scanner. The human workflow is purely physical.
- **Faithful capture**: Always capture at the highest fidelity the hardware supports. All lossy transformations happen downstream, never at the point of capture.
- **Separation of capture and processing**: Capture workloads write raw output to the NAS and fire a notification. Classification, transcoding, OCR, and other processing happen in separate downstream pipelines.
- **GitOps-managed**: All manifests, configurations, and workload definitions live in the homelab GitOps repo and are deployed via ArgoCD.

---

## 2. Infrastructure

### 2.1 Cluster environment

| Component | Detail |
|---|---|
| Cluster OS | Talos Linux |
| Orchestration | Kubernetes (managed by Talos) |
| GitOps | ArgoCD |
| CNI | Cilium (default-deny egress) |
| Secrets | 1Password Connect + External Secrets Operator |
| Monitoring | Prometheus + Grafana |
| Storage target | Synology NAS, NFS export |

### 2.2 Hardware — optical drive

| Property | Value |
|---|---|
| Model | Pioneer BDR-XS07UHD |
| Interface | USB 3.0 (bus-powered) |
| Capabilities | UHD Blu-ray, Blu-ray, DVD, CD (read/rip); LibreDrive compatible |
| Linux device nodes | `/dev/sr0` (block device), `/dev/sg0` (SCSI generic) |
| USB identifiers | Vendor `07e8` (Pioneer) — confirm exact product ID on first plug-in |
| Notes | LibreDrive support is critical for UHD ripping; MakeMKV handles this natively |

### 2.3 Hardware — document scanner

| Property | Value |
|---|---|
| Model | Epson DS-1630 (also sold as WorkForce DS-1630) |
| Interface | USB 3.0 |
| Capabilities | Flatbed + 50-sheet ADF, duplex via ADF, up to 600 DPI optical |
| Linux driver | `epsonscan2` SANE backend (Epson's official Linux driver); requires `epsonscan2-non-free-plugin` for firmware |
| USB identifiers | Vendor `04b8` (Epson) — confirm exact product ID via `lsusb` on first plug-in |
| Scan speeds | ADF: 25 ppm mono, 25 ppm color at 200/300 DPI; flatbed: 10 ipm |
| Output formats via SANE | TIFF, JPEG, PNG, PDF (via `scanimage` CLI) |

### 2.4 NAS directory structure

All capture output lands on the Synology NAS under a shared NFS export. The directory structure provides coarse routing; fine-grained organization is a downstream concern.

```
/volume1/incoming/
├── video/                  # Video disc rips (UHD, BD, DVD)
│   └── {Title} ({Year})/  # Named by ARM via OMDb lookup
│       ├── *.mkv
│       └── metadata.json   # OMDb metadata, disc info
├── audio/                  # Audio disc rips (CD, DVD-Audio)
│   └── {Artist} - {Album}/
│       ├── *.flac
│       └── metadata.json   # MusicBrainz metadata, album art
├── data/                   # Data disc ISOs
│   └── {label}_{date}.iso
├── scans/
│   ├── sessions/           # Raw scan sessions (groups of pages)
│   │   └── {timestamp}_{session-id}/
│   │       ├── page_001.tiff
│   │       ├── page_002.tiff
│   │       ├── ...
│   │       └── manifest.json
│   ├── documents/          # (Downstream) Classified single documents
│   └── books/              # (Downstream) Multi-page book reconstructions
└── notifications/          # (Optional) Dead-letter / audit log for failed notifications
```

---

## 3. Shared framework: USB detection and pod scheduling

### 3.1 Detection chain

The detection chain bridges the gap between a physical USB plug event and a Kubernetes scheduling decision. On Talos Linux, direct udev rule customization is limited, so we use Kubernetes-native primitives.

#### 3.1.1 Node Feature Discovery (NFD)

NFD runs as a DaemonSet across all nodes. It periodically scans for connected USB devices and applies node labels based on custom rules.

**Custom NFD rules:**

```yaml
# nfd-worker-config.yaml (excerpt)
sources:
  usb:
    deviceClassWhitelist:
      - "08"    # Mass storage (optical drives)
      - "ff"    # Vendor-specific (some scanners)
    deviceLabelFields:
      - "vendor"
      - "device"

# Custom rules for our specific devices
rules:
  - name: "pioneer-bdr-xs07uhd"
    labels:
      "openclaw.io/device-optical-drive": "pioneer-bdr-xs07uhd"
    matchFeatures:
      - feature: usb.device
        matchExpressions:
          vendor: {op: In, value: ["07e8"]}
          # device: {op: In, value: ["XXXX"]}  # Fill in after first plug-in

  - name: "epson-ds-1630"
    labels:
      "openclaw.io/device-scanner": "epson-ds-1630"
    matchFeatures:
      - feature: usb.device
        matchExpressions:
          vendor: {op: In, value: ["04b8"]}
          # device: {op: In, value: ["XXXX"]}  # Fill in after first plug-in
```

When a device is plugged in, NFD detects the USB device and applies the appropriate label to the node. When unplugged, NFD removes the label on its next scan cycle.

#### 3.1.2 Device plugin (Smarter Device Manager or custom)

The device plugin exposes physical device nodes as allocatable Kubernetes resources. This is preferable to running privileged pods because:

- Only the pod that requests the device gets access
- Kubernetes tracks device allocation (no two pods fight over `/dev/sr0`)
- Works with standard `resources.limits` in pod specs

**Configuration:**

```yaml
# smarter-device-manager config
- devicematch: ^sr[0-9]+$         # Optical drive block devices
  nummaxdevices: 1
- devicematch: ^sg[0-9]+$         # SCSI generic (needed for MakeMKV)
  nummaxdevices: 1
- devicematch: ^bus/usb/           # USB bus devices (for scanner)
  nummaxdevices: 4
```

#### 3.1.3 Workload scheduling

Each capture workload is defined as a **DaemonSet with a `nodeSelector`** matching the NFD-applied label. This is the simplest model: when the label appears, the DaemonSet controller schedules the pod on that node; when the label disappears, the pod is removed.

Alternative considered: a custom Kubernetes operator that watches for label changes and creates/destroys Deployments. This is more complex and unnecessary for our single-instance-per-device use case.

### 3.2 Synology NFS mount

The NAS is exposed to all capture pods via a PersistentVolume + PersistentVolumeClaim pair.

```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: incoming-nfs-pv
spec:
  capacity:
    storage: 10Ti
  accessModes:
    - ReadWriteMany
  nfs:
    server: synology.local
    path: /volume1/incoming
  mountOptions:
    - nfsvers=4.1
    - hard
    - intr
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: incoming-nfs-pvc
  namespace: capture
spec:
  accessModes:
    - ReadWriteMany
  resources:
    requests:
      storage: 10Ti
  volumeName: incoming-nfs-pv
```

### 3.3 Notification events

When a capture workload completes a unit of work (one disc ripped, one scan session closed), it emits a notification event. This decouples capture from downstream processing.

#### 3.3.1 Event schema

```json
{
  "schema_version": "1.0",
  "source": "optical-ripper | document-scanner",
  "event_type": "disc-extraction-complete | scan-session-complete",
  "timestamp": "2026-04-04T18:30:00Z",
  "output_path": "/volume1/incoming/video/Movie Title (2024)/",
  "media_type": "video/uhd-bluray | video/bluray | video/dvd | audio/cd | audio/dvd-audio | data/iso | scan/adf-session | scan/flatbed-session",
  "metadata": {
    "title": "Movie Title",
    "year": 2024,
    "source_device": "pioneer-bdr-xs07uhd",
    "disc_label": "MOVIE_TITLE",
    "page_count": null,
    "session_id": null
  },
  "node_name": "k8s-node-01"
}
```

#### 3.3.2 Transport

**Phase 1:** Lightweight webhook POST to a notification relay service running in the cluster. The relay fans out to configured destinations (NATS subject, Discord webhook, Pushover, etc.). The relay is a small Go or Python service with a ConfigMap-driven destination list.

**Phase 2:** If OpenClaw Home Agent is running, the notification relay publishes to a NATS subject that the appropriate OpenClaw agent subscribes to. The media agent handles video/audio ingest notifications; a new documents agent (or extension of the mail agent) handles scan notifications.

#### 3.3.3 Failure handling

If the notification POST fails:
- Retry with exponential backoff (3 attempts, 5s/30s/120s)
- On final failure, write the event JSON to `/incoming/notifications/dead-letter/{timestamp}.json`
- A CronJob or the relay service itself periodically retries dead-lettered events

### 3.4 Cilium network policy

Capture pods need limited egress:

```yaml
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: capture-pods-egress
  namespace: capture
spec:
  endpointSelector:
    matchLabels:
      app.kubernetes.io/part-of: capture-framework
  egress:
    # NFS to Synology
    - toEndpoints: []
      toCIDR:
        - "192.168.1.X/32"   # Synology IP
      toPorts:
        - ports:
            - port: "2049"
              protocol: TCP
    # Notification relay (cluster-internal)
    - toEndpoints:
        - matchLabels:
            app: notification-relay
      toPorts:
        - ports:
            - port: "8080"
              protocol: TCP
    # Metadata lookups (OMDb API, MusicBrainz) — optical ripper only
    - toFQDNs:
        - matchName: "www.omdbapi.com"
        - matchName: "musicbrainz.org"
      toPorts:
        - ports:
            - port: "443"
              protocol: TCP
    # DNS resolution
    - toEndpoints:
        - matchLabels:
            k8s:io.kubernetes.pod.namespace: kube-system
            k8s-app: kube-dns
      toPorts:
        - ports:
            - port: "53"
              protocol: UDP
```

### 3.5 Secrets

| Secret | Source | Consumer |
|---|---|---|
| MakeMKV license key | 1Password vault | Optical ripper pod (env var `KEY`) |
| OMDb API key | 1Password vault | Optical ripper pod (env var `OMDB_API_KEY`) |
| Notification relay destinations | ConfigMap (non-secret) + 1Password for webhook tokens | Notification relay |

All secrets are injected via External Secrets Operator pulling from 1Password Connect.

---

## 4. Workload 1: Optical disc ripper

### 4.1 Base software

**Automatic Ripping Machine (ARM)** — [github.com/automatic-ripping-machine/automatic-ripping-machine](https://github.com/automatic-ripping-machine/automatic-ripping-machine)

ARM provides the disc detection, type identification, ripping orchestration, metadata lookup, and web UI. It uses MakeMKV for video disc decryption/extraction and abcde for audio CD ripping.

### 4.2 Container image

Based on the official ARM Docker image (`automaticrippingmachine/automatic-ripping-machine`), with configuration overrides applied via environment variables and mounted config files.

### 4.3 Disc type routing

ARM handles disc type detection internally. The routing to output directories is configured as follows:

| Disc type | Detection method | Ripping tool | Output format | Output path |
|---|---|---|---|---|
| UHD Blu-ray | MakeMKV disc info | MakeMKV (LibreDrive) | MKV | `/incoming/video/{Title} ({Year})/` |
| Blu-ray | MakeMKV disc info | MakeMKV | MKV | `/incoming/video/{Title} ({Year})/` |
| DVD (video) | MakeMKV disc info | MakeMKV or HandBrake | MKV | `/incoming/video/{Title} ({Year})/` |
| Audio CD | abcde / cdparanoia | abcde | FLAC | `/incoming/audio/{Artist} - {Album}/` |
| DVD-Audio | disc structure analysis | MakeMKV (ISO) or dvdaudio tools | FLAC or ISO | `/incoming/audio/{Artist} - {Album}/` |
| Data disc (any) | Fallback | dd / mkisofs | ISO | `/incoming/data/{label}_{date}.iso` |

### 4.4 Metadata lookup

- **Video discs**: ARM queries the OMDb API using the disc title to retrieve movie/show metadata, year, and poster art. This drives the output folder naming convention that Plex/Jellyfin/Emby expects.
- **Audio CDs**: abcde queries MusicBrainz for disc metadata, track listings, and album art.
- **Data discs**: The ISO volume label is used as the folder name. No external metadata lookup.

### 4.5 Post-extraction flow

1. ARM completes ripping and writes output to the NAS
2. ARM ejects the disc (`EJECTENABLED=true`)
3. A post-rip hook script (configured in ARM) POSTs a notification event to the relay service
4. The user removes the disc and optionally inserts the next one
5. ARM detects the new disc insertion and begins the next rip

### 4.6 ARM configuration overrides

Key ARM configuration values (`arm.yaml` overrides):

```yaml
# Ripping behavior
EJECTENABLED: true
JUSTMAKEISO: false           # Default: full rip. Override per disc type as needed.
RIPMETHOD: "mkv"             # Use MakeMKV for video

# Output paths (mapped to NAS mount inside container)
COMPLETED_PATH: "/out/video"
AUDIO_COMPLETED_PATH: "/out/audio"
DATA_COMPLETED_PATH: "/out/data"

# Metadata
OMDB_API_KEY: "${OMDB_API_KEY}"   # Injected via secret

# Quality
HANDBRAKE_PRESET: "disabled"      # Phase 1: no transcoding at capture time
                                   # Raw MKV output; transcode downstream if desired.

# Notifications
NOTIFY_WEBHOOK: "http://notification-relay.capture.svc.cluster.local:8080/event"
```

### 4.7 Pioneer BDR-XS07UHD specific notes

- **LibreDrive**: The Pioneer BDR-XS07UHD supports LibreDrive mode, which MakeMKV uses to bypass firmware restrictions on UHD Blu-ray reading. This is the primary reason for choosing this drive.
- **Bus power**: The drive is bus-powered via USB. Ensure the node's USB port provides sufficient power (USB 3.0 spec: 900mA). If the drive draws too much current during spin-up, a powered USB hub may be needed.
- **Spin-up time**: External USB drives have a noticeable spin-up delay. ARM's disc detection polling interval should be generous enough (5-10 seconds) to avoid false negatives.

### 4.8 DaemonSet manifest

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: optical-ripper
  namespace: capture
  labels:
    app.kubernetes.io/name: optical-ripper
    app.kubernetes.io/part-of: capture-framework
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: optical-ripper
  template:
    metadata:
      labels:
        app.kubernetes.io/name: optical-ripper
        app.kubernetes.io/part-of: capture-framework
    spec:
      nodeSelector:
        openclaw.io/device-optical-drive: "pioneer-bdr-xs07uhd"
      containers:
        - name: arm
          image: automaticrippingmachine/automatic-ripping-machine:latest
          env:
            - name: ARM_UID
              value: "1000"
            - name: ARM_GID
              value: "1000"
            - name: KEY
              valueFrom:
                secretKeyRef:
                  name: makemkv-license
                  key: license-key
            - name: OMDB_API_KEY
              valueFrom:
                secretKeyRef:
                  name: omdb-api
                  key: api-key
          resources:
            limits:
              smarter-devices/sr0: 1
              smarter-devices/sg0: 1
          volumeMounts:
            - name: incoming
              mountPath: /out
            - name: arm-config
              mountPath: /etc/arm/config
            - name: arm-home
              mountPath: /home/arm
          ports:
            - name: web-ui
              containerPort: 8080
      volumes:
        - name: incoming
          persistentVolumeClaim:
            claimName: incoming-nfs-pvc
        - name: arm-config
          configMap:
            name: arm-config
        - name: arm-home
          emptyDir: {}
```

### 4.9 User journey: ripping a disc

1. Steve plugs the Pioneer BDR-XS07UHD into a USB port on node `k8s-node-02`.
2. NFD detects the USB device (vendor `07e8`) within its scan interval (~15-60 seconds).
3. NFD applies label `openclaw.io/device-optical-drive: pioneer-bdr-xs07uhd` to `k8s-node-02`.
4. The DaemonSet controller sees the label match and schedules the `optical-ripper` pod on `k8s-node-02`.
5. ARM starts, initializes MakeMKV, and begins polling `/dev/sr0` for disc insertion.
6. Steve inserts a UHD Blu-ray disc.
7. ARM detects the disc, identifies it as UHD Blu-ray via MakeMKV, queries OMDb for metadata.
8. MakeMKV rips the disc to MKV files in `/incoming/video/Movie Title (2024)/`.
9. ARM ejects the disc.
10. The post-rip hook POSTs a notification event: `{"source": "optical-ripper", "event_type": "disc-extraction-complete", "media_type": "video/uhd-bluray", "output_path": "/incoming/video/Movie Title (2024)/"}`.
11. Downstream: Plex/Jellyfin scans the incoming directory and adds the movie to its library. An OpenClaw agent optionally processes the notification for the daily briefing.
12. Steve inserts the next disc, or unplugs the drive.
13. If unplugged: NFD removes the label, the DaemonSet controller removes the pod.

---

## 5. Workload 2: Document scanner

### 5.1 Base software

**Custom container** built on:

- **SANE** (Scanner Access Now Easy) — hardware abstraction layer for scanners
- **`epsonscan2`** — Epson's official SANE backend, with non-free firmware plugin for the DS-1630
- **`scanimage`** — SANE CLI tool for initiating scans
- **Session manager** — custom service (Python or Go) that manages scan batching, session lifecycle, and notification dispatch
- **Web UI** — lightweight interface for monitoring scan status, reviewing sessions, and manual overrides

### 5.2 Container image

Purpose-built Dockerfile:

```dockerfile
FROM ubuntu:24.04

# SANE and dependencies
RUN apt-get update && apt-get install -y \
    sane-utils \
    libsane-dev \
    imagemagick \
    tiffcp \
    curl \
    jq \
    python3 \
    python3-pip

# Epson epsonscan2 + non-free firmware plugin
# (Install from Epson's Linux driver download page or build from source)
COPY epsonscan2/ /opt/epsonscan2/
RUN dpkg -i /opt/epsonscan2/*.deb || apt-get -f install -y

# Session manager application
COPY scanner-session-manager/ /opt/scanner/
WORKDIR /opt/scanner

CMD ["python3", "main.py"]
```

### 5.3 Scanning modes

The DS-1630 has two physical input methods. The session manager detects which one is active based on the SANE source parameter.

#### 5.3.1 ADF (Automatic Document Feeder) mode

- **Use case**: Multi-page documents, mail, receipts, correspondence
- **Behavior**: Load a stack of pages into the ADF tray. The session manager initiates a scan run that pulls all pages sequentially until the feeder is empty.
- **Session semantics**: One ADF run = one session. All pages pulled in a single feeder run are grouped together.
- **Duplex**: The DS-1630 supports duplex scanning via the ADF (scan both sides). Enabled by default.

#### 5.3.2 Flatbed mode

- **Use case**: Books, photos, fragile documents, thick originals, anything that won't feed through rollers
- **Behavior**: Place a single page/item on the glass. The session manager scans one page at a time. An idle timeout groups consecutive flatbed scans into a session.
- **Session semantics**: All flatbed scans within a configurable idle window (default: 90 seconds) are grouped into one session. If no new scan is initiated within the window, the session closes.
- **Explicit boundary**: The web UI (or a future physical button) allows the user to tap "new document" to force a session boundary between scans.

### 5.4 Scan parameters

**Capture everything at maximum fidelity. Post-processing derives what it needs.**

| Parameter | Value | Rationale |
|---|---|---|
| Resolution | 600 DPI | Maximum optical resolution of the DS-1630. Downstream OCR and ebook generation benefit from high resolution. |
| Color mode | 24-bit color | Always. Even for text documents — preserves letterheads, colored annotations, blue-ink signatures, stamps. |
| Output format | TIFF (uncompressed or LZW) | Lossless archival format. JPEG artifacts degrade OCR accuracy. |
| Duplex (ADF) | Enabled | Default on. Can be disabled per-session via web UI. |
| Blank page skip | Disabled at capture | Capture everything. Blank page detection is a downstream classification concern (may want blank pages for book structure). |

### 5.5 Session lifecycle

```
[Pages scanned] → [Session open, accumulating pages]
                         │
                         ├── ADF: feeder empties → session closes automatically
                         │
                         └── Flatbed: idle timeout (90s) expires → session closes
                                │
                                └── OR: user taps "new document" → session closes, new one opens
                         
[Session closed] → [Write manifest.json] → [POST notification] → [Ready for next session]
```

### 5.6 Session manifest

Each closed session produces a `manifest.json` alongside its TIFF files:

```json
{
  "schema_version": "1.0",
  "session_id": "20260404-183045-a1b2c3",
  "timestamp_start": "2026-04-04T18:30:45Z",
  "timestamp_end": "2026-04-04T18:32:12Z",
  "source_device": "epson-ds-1630",
  "input_method": "adf | flatbed",
  "duplex": true,
  "resolution_dpi": 600,
  "color_mode": "color",
  "page_count": 12,
  "pages": [
    {
      "filename": "page_001.tiff",
      "side": "front | back",
      "size_bytes": 25431876,
      "dimensions": {"width_px": 5100, "height_px": 6600}
    }
  ],
  "output_path": "/volume1/incoming/scans/sessions/20260404-183045-a1b2c3/",
  "user_tags": [],
  "notes": ""
}
```

### 5.7 Session manager web UI

A lightweight web interface (Flask or similar) served from the scanner pod:

- **Status page**: Shows current session state (idle / scanning / session open, page count), scanner connection status, recent sessions
- **Manual controls**:
  - "Scan now" button (trigger a flatbed scan without waiting for hardware button)
  - "New document" button (force session boundary)
  - "Close session" button (force-close the current session)
  - Duplex toggle (for current/next ADF run)
- **Session review**: List of recent sessions with page count, thumbnail of first page, timestamp
- **Configuration**: Idle timeout adjustment, notification endpoint override

### 5.8 DaemonSet manifest

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: document-scanner
  namespace: capture
  labels:
    app.kubernetes.io/name: document-scanner
    app.kubernetes.io/part-of: capture-framework
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: document-scanner
  template:
    metadata:
      labels:
        app.kubernetes.io/name: document-scanner
        app.kubernetes.io/part-of: capture-framework
    spec:
      nodeSelector:
        openclaw.io/device-scanner: "epson-ds-1630"
      containers:
        - name: scanner
          image: ghcr.io/openclaw/document-scanner:latest  # Custom image
          resources:
            limits:
              smarter-devices/bus-usb: 1
          volumeMounts:
            - name: incoming
              mountPath: /out
            - name: scanner-config
              mountPath: /etc/scanner
          ports:
            - name: web-ui
              containerPort: 8080
      volumes:
        - name: incoming
          persistentVolumeClaim:
            claimName: incoming-nfs-pvc
        - name: scanner-config
          configMap:
            name: scanner-config
```

### 5.9 User journey: scanning mail

1. Steve plugs the Epson DS-1630 into a USB port on node `k8s-node-01`.
2. NFD detects the USB device (vendor `04b8`), applies label `openclaw.io/device-scanner: epson-ds-1630` to `k8s-node-01`.
3. The DaemonSet controller schedules the `document-scanner` pod on `k8s-node-01`.
4. The session manager starts, initializes SANE, confirms the DS-1630 is detected.
5. Steve collects a stack of mail — utility bills, a letter from a friend, a credit card statement, some junk mail.
6. Steve loads the stack into the ADF tray.
7. The session manager detects pages in the ADF and initiates scanning: 600 DPI, color, duplex.
8. The ADF pulls all pages through. Each page is written as a numbered TIFF to `/incoming/scans/sessions/{timestamp}_{session-id}/`.
9. The ADF empties. The session manager closes the session, writes `manifest.json`.
10. The session manager POSTs a notification: `{"source": "document-scanner", "event_type": "scan-session-complete", "media_type": "scan/adf-session", "page_count": 14}`.
11. Downstream: A classification pipeline (Phase 2) receives the notification, examines the scanned pages, and determines that this session contains 5 distinct documents (2 bills, 1 personal letter, 1 credit card statement, 1 junk). It splits them into `/incoming/scans/documents/` with appropriate metadata.

### 5.10 User journey: scanning a book

1. Steve opens a book to the first spread and places it face-down on the flatbed glass.
2. He triggers a scan (via the web UI "scan now" button, or by pressing the scanner's hardware button if supported).
3. The session manager captures one 600 DPI color TIFF.
4. Steve turns the page, places the next spread on the glass, triggers another scan.
5. This repeats for each page/spread. Each scan resets the 90-second idle timer.
6. When Steve is done, he waits for the idle timeout (or taps "close session" in the web UI).
7. The session closes. Manifest records `input_method: "flatbed"` and all pages.
8. Notification fires. Downstream: The book reconstruction pipeline (Phase 2) processes the session — each scan may contain two book pages (left and right of the spread) that need to be split, deskewed, and sequenced.

### 5.11 User journey: scanning photos or art

1. Steve places a photograph on the flatbed glass.
2. Triggers a scan. Full color, 600 DPI TIFF captured.
3. Places the next photo, triggers another scan.
4. Session closes on idle timeout.
5. Downstream: The classifier identifies the session as photos/art (high visual complexity, low text content) and routes them to a photo archive rather than OCR.

---

## 6. Downstream processing (Phase 2)

The capture workloads deliberately avoid any classification or transformation beyond faithful extraction. All intelligence lives in downstream pipelines. This section describes the target architecture for downstream processing — to be built in Phase 2 when the Mac Studio M5 is available for local LLM inference.

### 6.1 Document classification pipeline

**Trigger**: Receives `scan-session-complete` notification events.

**Processing**:

1. Load all page TIFFs from the session.
2. For each page, generate a reduced-resolution JPEG for LLM vision analysis.
3. Submit the page set to a multimodal LLM (Claude vision API in Phase 1; local model on Mac Studio in Phase 2) with a classification prompt.
4. The LLM identifies document boundaries and classifies each document.

**Classification taxonomy**:

| Category | Description | Downstream routing |
|---|---|---|
| `correspondence` | Personal letters, cards, handwritten notes | Archive, optional OCR for searchability |
| `mail/bills` | Utility bills, statements, official notices | OCR → structured data extraction → financial tracking |
| `mail/junk` | Advertisements, flyers, unsolicited mail | Low-priority archive or discard |
| `receipts` | Receipts, invoices | OCR → expense tracking |
| `tax-documents` | W-2s, 1099s, tax returns | OCR → secure archive |
| `book-pages` | Consistent formatting, page numbers, sequential content | Book reconstruction pipeline |
| `photos` | Photographs, prints | Photo archive (no OCR) |
| `art` | Drawings, paintings, artwork | Art archive (no OCR) |
| `technical` | Manuals, spec sheets, datasheets | OCR → searchable PDF archive |
| `mixed` | Multiple types in one page, or uncategorizable | Manual review queue |

**LLM classification prompt** (simplified):

```
You are examining {N} scanned pages from a single scanning session. 

For each logical document you identify:
1. Which page numbers belong to it
2. Its category (from the taxonomy)
3. Key metadata: date, sender/author, subject, any reference numbers
4. Confidence level (high/medium/low)

If a single page contains content from two different documents (e.g., 
front and back of different items from an ADF scan), note this.
```

### 6.2 OCR pipeline

**Input**: Classified document pages (from the classification pipeline).

**Processing**:

1. Generate a high-contrast grayscale derivative of each color TIFF (ImageMagick or similar).
2. Run OCR (Tesseract, or a cloud OCR API for higher accuracy).
3. Produce structured output: full text, per-page text, bounding boxes for key fields.
4. Store OCR results alongside the original TIFFs.

**Output**: Per-document JSON with full text, plus a searchable PDF combining the original color scan with an invisible OCR text layer.

### 6.3 Book reconstruction pipeline

**Input**: A scan session classified as `book-pages`.

**Processing**:

1. **Page splitting**: If scanned as spreads (two pages per flatbed scan), split each scan into left and right pages. Use image analysis to find the gutter (center fold line).
2. **Deskewing**: Straighten each page image.
3. **Page ordering**: Sequence pages by detected page numbers, or by scan order if no numbers are found.
4. **OCR**: Full-text OCR of each page.
5. **Structure detection**: An LLM pass identifies chapter breaks, headings, front/back matter, table of contents, footnotes, illustrations.
6. **EPUB generation**: Assemble the structured text into an EPUB file:
   - Proper chapter navigation (NCX/nav)
   - Embedded fonts
   - Table of contents
   - Reflowable text (for reMarkable and open e-readers)
7. **Alternative: PDF output**: For layout-dependent books (art books, cookbooks, graphic novels), produce a high-quality PDF with OCR text layer instead of reflowable EPUB. The classifier determines which output format is appropriate based on text-to-image ratio.

**Output targets**:

| Format | Target device/app | When to use |
|---|---|---|
| EPUB (reflowable) | reMarkable, Kobo, Calibre, any EPUB reader | Text-heavy books: novels, non-fiction, reference |
| PDF (with OCR layer) | reMarkable (PDF mode), any PDF reader | Layout-dependent: art books, cookbooks, textbooks with complex figures |

**reMarkable delivery**: The reMarkable supports EPUB and PDF natively. Delivery options:
- Drop file into the reMarkable cloud sync folder
- Use the reMarkable USB web interface for local transfer
- Use the reMarkable cloud API (if available and documented)

### 6.4 Video/audio ingest

**Trigger**: Receives `disc-extraction-complete` notification events for video and audio.

**Processing**:

- **Video**: Output lands in a directory structure Plex/Jellyfin/Emby expects. Downstream may optionally transcode (e.g., 4K HEVC → 1080p for bandwidth-constrained clients), but this is not part of the capture framework.
- **Audio**: Output lands as FLAC files with MusicBrainz metadata. Downstream may integrate with a music library manager (Lidarr, Beets) for further organization.

---

## 7. Open questions and decisions

### 7.1 Resolved

| Question | Decision | Rationale |
|---|---|---|
| How do we group scanned pages into documents? | Session-based batching (ADF run or flatbed idle timeout), with downstream LLM classification for splitting sessions that contain multiple documents | Keeps the scanner workload simple; classification is a downstream concern that benefits from LLM vision |
| How do we classify document types? | Multimodal LLM (Claude vision → local model) with defined taxonomy | Most accurate approach; taxonomy is extensible |
| B&W vs color scanning? | Always full color, 600 DPI, TIFF | Storage is cheap, re-scanning is not. Downstream derives grayscale for OCR. |
| Should we scan mail? | Yes. ADF mode makes this a "load and go" operation | The 50-sheet ADF on the DS-1630 is perfect for mail batches |
| Book scanning output format? | Both EPUB (reflowable) and PDF, chosen by classifier based on content type | Covers both text-heavy and layout-dependent books |
| Scanner Linux support? | Confirmed: `epsonscan2` SANE backend officially supports the DS-1630 | Epson lists SANE as a supported driver interface; `epsonscan2` non-free plugin provides required firmware |

### 7.2 Still open

| Question | Options | Notes |
|---|---|---|
| NFD scan interval tuning | Default (~60s) vs aggressive (~15s) | Faster detection means quicker pod scheduling after plug-in, but more CPU overhead. Start with default and tune based on experience. |
| Scanner hardware button support | SANE button API vs. polling vs. web UI only | The DS-1630 may expose a hardware "scan" button via SANE's button API (`scanimage --button-wait`). Needs testing. If supported, this is the most natural UX — no web UI needed for basic scanning. |
| Smarter Device Manager vs. custom device plugin | Use existing SDM vs. write a thin plugin for tighter control | SDM may be overkill or insufficiently flexible. Evaluate during implementation. |
| Book spread splitting algorithm | OpenCV-based gutter detection vs. LLM vision vs. fixed center split | Fixed center split works for most books but fails for thick spines. OpenCV gutter detection is more robust. LLM vision is most flexible but slowest. |
| reMarkable delivery mechanism | Cloud sync folder vs. USB web interface vs. API | Depends on which reMarkable model Steve has and whether cloud sync is enabled. |
| Phase 1 notification transport | Simple webhook relay vs. NATS from the start | Webhook relay is simpler for Phase 1. NATS adds operational complexity but is the target for OpenClaw integration in Phase 2. |
| Flatbed idle timeout value | 90 seconds (proposed) | May need tuning based on actual usage. Too short = sessions split unexpectedly while turning book pages. Too long = waiting around after finishing. Configurable via web UI. |
| Physical "new document" button | GPIO wired button vs. web UI only vs. NFC tap | A physical button is the nicest UX but requires hardware tinkering. Web UI is good enough for Phase 1. |

---

## 8. Phasing

### Phase 1: Core capture (build first)

- [ ] NFD custom rules for Pioneer and Epson USB IDs
- [ ] Device plugin configuration (Smarter Device Manager)
- [ ] NFS PV/PVC for Synology incoming share
- [ ] Cilium network policies for capture namespace
- [ ] Optical ripper DaemonSet (ARM container + config)
- [ ] MakeMKV license and OMDb API key via 1Password/ESO
- [ ] Post-rip notification hook in ARM
- [ ] Notification relay service (webhook fan-out)
- [ ] Document scanner container (SANE + epsonscan2 + session manager)
- [ ] Scanner web UI (status, manual controls, session review)
- [ ] Session manifest generation
- [ ] Dead-letter handling for failed notifications
- [ ] Prometheus metrics: rip duration, disc type counts, scan session counts, pages per session, notification success/failure
- [ ] Grafana dashboard for capture workloads
- [ ] ArgoCD Application manifests for the entire capture namespace

### Phase 2: Intelligent processing (after Mac Studio M5 is online)

- [ ] Document classification pipeline (LLM vision)
- [ ] OCR pipeline (Tesseract or cloud API)
- [ ] Searchable PDF generation (color scan + invisible OCR text layer)
- [ ] Book reconstruction pipeline (split → deskew → order → OCR → structure → EPUB/PDF)
- [ ] reMarkable delivery integration
- [ ] OpenClaw agent integration (media agent for video/audio, documents agent for scans)
- [ ] NATS-based notification transport (replacing or augmenting webhook relay)
- [ ] Local embedding + search index for scanned document text (sqlite-vec)
- [ ] Structured data extraction from classified documents (bills → amounts/dates/accounts)

### Phase 3: Quality of life (stretch goals)

- [ ] Physical "scan" button support via SANE button API
- [ ] Hardware "new document" button (GPIO or NFC)
- [ ] Auto-detection of ADF vs flatbed (switch modes based on where pages are loaded)
- [ ] Scanner calibration profiles (per document type)
- [ ] Multi-scanner support (extend framework to handle multiple scanners simultaneously)
- [ ] Barcode/QR code detection in scanned documents for automatic routing
- [ ] Integration with external document management systems

---

## 9. Repository structure

```
openclaw-capture/
├── README.md
├── docs/
│   └── spec.md                          # This document
├── manifests/
│   ├── base/
│   │   ├── namespace.yaml
│   │   ├── nfs-pv.yaml
│   │   ├── nfs-pvc.yaml
│   │   └── network-policies.yaml
│   ├── nfd/
│   │   └── nfd-worker-config.yaml       # Custom USB detection rules
│   ├── device-plugin/
│   │   └── smarter-device-manager.yaml
│   ├── optical-ripper/
│   │   ├── daemonset.yaml
│   │   ├── configmap.yaml               # ARM configuration
│   │   ├── external-secret.yaml         # MakeMKV key, OMDb key
│   │   └── service.yaml                 # Web UI access
│   ├── document-scanner/
│   │   ├── daemonset.yaml
│   │   ├── configmap.yaml               # Scanner config, session manager config
│   │   └── service.yaml                 # Web UI access
│   ├── notification-relay/
│   │   ├── deployment.yaml
│   │   ├── configmap.yaml               # Destination list
│   │   ├── external-secret.yaml         # Webhook tokens
│   │   └── service.yaml
│   ├── monitoring/
│   │   ├── servicemonitor.yaml
│   │   ├── prometheusrule.yaml          # Alert rules
│   │   └── grafana-dashboard.json
│   └── argocd/
│       └── application.yaml             # ArgoCD Application for this repo
├── images/
│   ├── document-scanner/
│   │   ├── Dockerfile
│   │   ├── scanner-session-manager/
│   │   │   ├── main.py
│   │   │   ├── session.py
│   │   │   ├── scan.py                  # SANE interface wrapper
│   │   │   ├── notify.py
│   │   │   ├── web/                     # Flask web UI
│   │   │   │   ├── app.py
│   │   │   │   ├── templates/
│   │   │   │   └── static/
│   │   │   └── requirements.txt
│   │   └── epsonscan2/                  # Driver packages (or download script)
│   └── notification-relay/
│       ├── Dockerfile
│       └── relay/
│           ├── main.py                  # (or main.go)
│           └── requirements.txt
└── scripts/
    ├── bootstrap.sh                     # Initial cluster setup
    ├── detect-usb-ids.sh                # Helper to identify VID:PID of connected devices
    └── test-notification.sh             # Manual notification test
```

---

## 10. Acceptance criteria

### Optical disc ripper

- [ ] Plugging in the Pioneer drive causes the ARM pod to be scheduled within 2 minutes.
- [ ] Inserting a Blu-ray disc results in an MKV file appearing in `/incoming/video/` with correct title and year.
- [ ] Inserting an audio CD results in FLAC files in `/incoming/audio/` with artist, album, and track metadata.
- [ ] Inserting a data disc results in an ISO in `/incoming/data/`.
- [ ] The disc is ejected after ripping completes.
- [ ] A notification event is received by the relay within 30 seconds of rip completion.
- [ ] Unplugging the drive causes the ARM pod to be removed within 2 minutes.

### Document scanner

- [ ] Plugging in the Epson DS-1630 causes the scanner pod to be scheduled within 2 minutes.
- [ ] Loading pages into the ADF and triggering a scan produces numbered TIFFs in a session directory.
- [ ] The session closes automatically when the ADF empties.
- [ ] Flatbed scans within the idle timeout are grouped into a single session.
- [ ] Flatbed sessions close after the idle timeout expires.
- [ ] Each closed session has a valid `manifest.json` with correct page count and metadata.
- [ ] A notification event is received by the relay within 10 seconds of session close.
- [ ] The web UI displays current scanner status and recent sessions.
- [ ] Unplugging the scanner causes the pod to be removed within 2 minutes.

### Framework

- [ ] Notifications are retried on failure and dead-lettered after 3 attempts.
- [ ] Prometheus metrics are scraped and visible in Grafana.
- [ ] All manifests deploy cleanly via ArgoCD from a single Application.
- [ ] Cilium network policies restrict egress to only necessary destinations.
- [ ] Secrets are sourced from 1Password via ESO — no plaintext secrets in the repo.

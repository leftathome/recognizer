# Optical Disc Ingest — Real Cold-Plug End-to-End Plan

Status: IN PROGRESS (started 2026-06-12)
Goal: a genuine end-to-end test on johnny — plug the Pioneer BDR-XS07UHD in
from nothing, the optical-ripper pod auto-schedules, MakeMKV rips the disc to
the data PVC, ejects, and fires notifications — with ZERO manual intervention.

## Why this plan exists

A "juiced" demo on 2026-06-12 proved the *capability* end-to-end: with the
drive's real SCSI-generic node (`sg36`) hand-mounted and the current free
MakeMKV beta key hand-injected, MakeMKV opened the Blu-ray
("Ghost in the Shell 2 Innocence"), enumerated 6 titles, and ripped a 258 MB
title to the data PVC (`/out`). So drive read + decrypt + rip-to-PVC all work.

The demo required two manual overrides. A REAL end-to-end must supply both
autonomously:

1. The device — the drive's `/dev/sg<N>` node is dynamic (sg22 last session,
   sg36 now; enumeration-dependent). The chart hardcodes `smarter-devices/sg0`,
   which is never the drive. MakeMKV locates the node via
   `/sys/block/sr0/device/scsi_generic/` then opens `/dev/<that exact name>`,
   so a renamed udev symlink won't help — the container needs that exact node.
   (archiver-bms)
2. The key — the Vault key is an EXPIRED free beta key (MSG:5073). A purchased
   PERMANENT key never expires and bypasses version checks. (archiver-144)

## Architecture decision (archiver-6ix) — DECIDED: split hardware namespace

Create `recognizer-hardware` with PSS `enforce=privileged`. The optical-ripper
(and later document-scanner) live there with host-device access so MakeMKV/the
scanner self-discover hardware regardless of dynamic enumeration. The main
`recognizer` namespace stays PSS=restricted — elevated privilege is contained
to the hardware ns. This single decision also unblocks udev insert/eject events
(archiver-6zu) and the document scanner, which has identical needs.

## What already works (no change needed)

- NFD rule `recognizer-devices` labels the node on drive plug
  (08e4:017a class 08 -> `recognizer.io/device-optical-drive`); the ripper
  daemonset nodeSelects on it -> autonomous scheduling on cold-plug.
- am0 key-delivery mechanism: init container writes
  `/home/arm/.MakeMKV/settings.conf`; ARM rips as the `arm` user (HOME=
  /home/arm) and reads it. Correct. (The earlier "version too old" was a
  test artifact of exec-ing as root with HOME=/root.)
- Data volume is RWX NFS -> a second PVC in the hardware ns can bind the same
  export for cross-namespace access.

## Work breakdown

- [ ] CHART: add `recognizer-hardware` namespace template + `values.hardware.*`
      (namespace name, PSS labels enforce/audit/warn=privileged).
- [ ] CHART: relocate optical-ripper DaemonSet + its ExternalSecrets
      (makemkv-license, omdb-api) into the hardware ns. Render their
      `namespace:` from `values.hardware.namespace`.
- [ ] CHART (archiver-bms): replace the `smarter-devices/sr0`+`sg0` resource
      requests with `securityContext.privileged: true` + hostPath `/dev` mount
      so MakeMKV self-discovers sr0 + the dynamic sgN. Keep the NFD
      nodeSelector. (Add hostPath `/run/udev` later for archiver-6zu.)
- [ ] CHART: cross-namespace NFS data access — render a data PV/PVC in the
      hardware ns bound to the same NFS export (backend=nfs path), so the
      ripper's `/out` resolves there.
- [ ] CHART: NetworkPolicy for the hardware ns (ripper -> notification-relay
      in the main ns; DNS; registry).
- [ ] GITOPS: ensure Flux creates/labels the hardware ns (PSS labels), and the
      HelmRelease values enable `hardware.*` + keep storage.backend=nfs.
- [ ] KEY (archiver-144, USER): purchase a permanent MakeMKV key, store at
      Vault `eso/recognizer/makemkv` (property `license-key`). ESO syncs ->
      Secret -> init container. No more monthly expiry.
- [ ] VALIDATE: `helm template` + kubeconform/kubeconform-crds; confirm the
      hardware-ns manifests render and PSS labels are present.
- [ ] DEPLOY: merge to gitops, let Flux reconcile.
- [ ] COLD-PLUG E2E TEST on johnny: with drive unplugged, replug -> NFD labels
      node -> ripper schedules -> MakeMKV rips to PVC -> eject -> notifications.

## Notes / gotchas

- Container runs as root for ARM's supervisord init, then drops to UID 1000
  (arm) for ripping. fsGroup=1000 on emptyDirs is required by ARM's startup
  permission check. Preserve this.
- MakeMKV free beta keys rotate ~monthly; a permanent key removes that toil.
- Cross-namespace PVCs are impossible (PVCs are namespaced) — must mint a
  separate PVC in the hardware ns against the same RWX NFS export.
- Scanner caveat (later): NFD worker-conf deviceClassWhitelist lacks USB class
  06 (Imaging), so the Epson rule won't fire until that's added.

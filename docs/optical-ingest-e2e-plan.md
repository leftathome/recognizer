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

- [x] CHART: add `recognizer-hardware` namespace template + `values.hardware.*`
      (PSS labels enforce/audit/warn=privileged). [archiver-otp]
- [x] CHART: relocate optical-ripper DaemonSet + ConfigMap + Service +
      ExternalSecrets into the hardware ns via `recognizer.hardwareNamespace`.
- [x] CHART (archiver-bms): replaced `smarter-devices/sr0`+`sg0` with
      `securityContext.privileged: true` + hostPath `/dev` so MakeMKV
      self-discovers sr0 + the dynamic sgN. NFD nodeSelector kept. Legacy
      (hardware.enabled=false) path still renders smarter-devices.
- [x] CHART (archiver-ztw): `hardware.data.mode=scratch` -> throwaway RWO PVC
      on `longhorn-single-replica` for the ripper's `/out` (NAS unavailable).
      `mode=nfs` retained for cross-ns sharing once the NAS is cabled.
- [x] KEY: Vault `eso/recognizer/makemkv` refreshed to the working free beta
      key; autonomous delivery validated (no MSG:5021/5073). [archiver-144]
- [x] VALIDATE: `helm lint --strict` + `helm template` (hardware on/off) green.
- [ ] GITOPS (archiver-yvk): chart v0.5.0 (Chart.yaml bumped, CHANGELOG);
      HelmRelease bumped to 0.5.0 + explicit hardware values. STAGED locally;
      needs push + `v0.5.0` tag (CI publishes the OCI chart) + gitops push.
- [ ] CHART: NetworkPolicy for the hardware ns (ripper -> notification-relay).
      Deferred: chart netpols are disabled in the HelmRelease anyway, and the
      relay is in ImagePullBackOff (node CA issue), so notifications are a
      separate follow-up.
- [ ] COLD-PLUG E2E TEST on johnny (archiver-e1a): drive unplugged -> replug ->
      NFD labels node -> ripper schedules in recognizer-hardware -> MakeMKV
      rips to the scratch PVC -> eject. (Importer hand-off + notifications
      pending NAS + relay CA fix.)
- [ ] SCANNER (after optical): Epson plugged into johnny now. Note: NFD
      worker-conf deviceClassWhitelist lacks USB class 06 (Imaging), so the
      scanner NodeFeatureRule won't fire until that's added -- verify first.

## Notes / gotchas

- Container runs as root for ARM's supervisord init, then drops to UID 1000
  (arm) for ripping. fsGroup=1000 on emptyDirs is required by ARM's startup
  permission check. Preserve this.
- MakeMKV free beta keys rotate ~monthly; a permanent key removes that toil.
- Cross-namespace PVCs are impossible (PVCs are namespaced) — must mint a
  separate PVC in the hardware ns against the same RWX NFS export.
- Scanner caveat (later): NFD worker-conf deviceClassWhitelist lacks USB class
  06 (Imaging), so the Epson rule won't fire until that's added.

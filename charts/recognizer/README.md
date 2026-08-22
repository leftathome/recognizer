# recognizer

Homelab capture framework: document scanners, optical disc rippers, and a
notification relay that fans out completion events. The chart packages
three workloads behind a single namespace, with shared storage and
opt-in monitoring.

| Workload | Kind | What it does |
|---|---|---|
| `document-scanner` | DaemonSet (per node with USB scanner) | SANE wrapper + session manager; emits scan-session manifests |
| `notification-relay` | Deployment (2 replicas by default) | Validates capture events against JSON Schema, fans out to webhooks |
| `optical-ripper` | DaemonSet (per node with USB optical drive) | Wraps Automatic Ripping Machine; post-rip hook posts to the relay |
| `archive-importer` | Suspended CronJob template (promoted to a Job per spec 03 § 8.1) | Imports finished digital archives (Google Takeout in v0.2.0); emits per-subtree notification events |

The `incoming` PVC is shared across all four so finished captures and
imported archives land in one place for downstream consumers.

## Cluster prerequisites

**These are not installed by this chart. The chart will deploy but pods
will stay `Pending` until each piece is in place.**

| What | Required by | Why |
|---|---|---|
| **node-feature-discovery (NFD) operator** running in `node-feature-discovery` namespace, configured with the recognizer device-match rules (see `gitops/clusters/orac/apps/recognizer/configmap-nfd-worker.yaml` for the rule set this chart expects) | document-scanner, optical-ripper | Generates the `recognizer.io/device-scanner` and `recognizer.io/device-optical-drive` node labels that the workload `nodeSelector`s pin against. Without NFD, the DaemonSet pods have nowhere to schedule. |
| **smarter-device-manager** DaemonSet on every capture-equipped node, configured to expose `/dev/sr*`, `/dev/sg*`, `/dev/bus/usb/*` as Kubernetes resources | document-scanner (`smarter-devices/bus-usb`), optical-ripper (`smarter-devices/sr0`, `smarter-devices/sg0`) | Without it, pod admission fails with `Insufficient smarter-devices/sr0` or similar. |
| **External Secrets Operator** with a working `ClusterSecretStore` named `vault-backend` | notification-relay, optical-ripper | The chart's `ExternalSecret` resources fetch service tokens (Discord, Pushover, MakeMKV, OMDb) from Vault. ESO can be replaced by overriding the `*.externalSecret.*` value blocks. |
| **A working StorageClass** matching `storage.<backend>.storageClassName` | All workloads | Default is `longhorn` (the orac cluster default) with `ReadWriteMany` access. RWX is required because the DaemonSets pin to different nodes; a single shared volume across them. |
| **An image pull credential** named `recognizer-registry` (or whatever `image.pullSecrets[].name` points at) in the chart's release namespace | All workloads | Images are published to a private registry (`registry.orac.local/steve/recognizer/*` by default). The recognizer namespace needs a Secret to pull them. The orac gitops repo materializes this via ExternalSecret; see `clusters/orac/apps/recognizer/externalsecret-gitlab-registry-pull-creds.yaml`. |

If a prereq is missing, the workloads it gates can be disabled at deploy
time via the `*.enabled: false` toggles:

```yaml
documentScanner:
  enabled: false      # disable until NFD + smarter-device-manager land
opticalRipper:
  enabled: false
# notification-relay has no hardware deps; safe to leave on
```

## Quick start

The chart is published to the homelab GitLab's OCI registry:

```bash
helm pull oci://registry.orac.local/steve/recognizer/charts/recognizer \
  --version 0.1.0 \
  --insecure-skip-tls-verify
```

The supported install path is Flux in the [orac gitops repo](https://gitlab.orac.local/steve/gitops);
see `clusters/orac/apps/recognizer/helmrelease-recognizer.yaml` for the
canonical HelmRelease. The chart is not intended for cross-cluster
deployment without changes to image registry, pull-secret name, and
device-label vocabulary.

## Configuration

### Storage backend

`values.storage.backend` selects one of three modes:

| `backend` | What | When |
|---|---|---|
| `longhorn` (default) | Dynamic RWX provisioning from a Longhorn StorageClass. Longhorn auto-spawns an NFS-share-manager pod under the hood. | The orac cluster default. |
| `nfs` | Static `PersistentVolume` against an external NFS export. Requires `storage.nfs.server`. | When the NAS is the right backing store. |
| `existing` | The chart renders no PV/PVC; workloads mount whatever PVC is named in `storage.existing.claimName`. | Bring-your-own PVC; e.g. shared with another release. |

Defaults: `size: 50Gi`, `accessModes: [ReadWriteMany]`. The PV (for
backend=nfs) and the PVC carry `helm.sh/resource-policy: keep` so a
chart uninstall does not strand data.

### Image registry

`image.registry` + `image.repository` compose the image reference.
Defaults assume the orac homelab:

```yaml
image:
  registry: registry.orac.local
  repository: steve/recognizer
  pullSecrets:
    - name: recognizer-registry
```

The optical-ripper image is the upstream Docker Hub
`automaticrippingmachine/automatic-ripping-machine` and does NOT use
the chart's registry/repository.

### Node selectors

The two DaemonSets pin to nodes labeled by NFD when the matching
hardware is attached:

```yaml
documentScanner:
  nodeSelector:
    recognizer.io/device-scanner: epson-ds-1630
opticalRipper:
  nodeSelector:
    recognizer.io/device-optical-drive: pioneer-bdr-xs07uhd
```

Override these to support additional models. The NFD rule set in
`gitops/.../configmap-nfd-worker.yaml` is the source of truth for which
label values are generated for which USB vendor:device pairs.

### Glovebox compatibility

When `archiveImporter.gloveboxIngest.enabled: true` or
`walhelmSource.enabled: true` (both use glovebox archive delivery), ensure
the deployed glovebox meets the following compatibility requirements:

- **Minimum app version:** 0.6.4 or later. Older versions (≤0.6.3) enforce a
  60s `ReadTimeout` that kills multi-GB archive uploads with `curl (55) broken
  pipe`.

- **mTLS precondition:** Before the operator sets `ingest.tls.mode: required`,
  verify the glovebox deployment includes the bearer-listener fix
  (`planPlaintextListeners`). Without it, `/v1/archives` goes offline and
  uploads fail with connection-refused.

- **Port migration:** A coming `config.ingest.bearerPort` split will move
  `/v1/archives` off the shared port 9091. When the operator configures it,
  coordinate a maintenance window and update `gloveboxIngest.url` to reflect
  the new port in both `archiveImporter` and `walhelmSource` blocks.

- **Vault TLS:** If the glovebox operator upgrades to a version with
  `tlsSkipVerify: false` (flip from the current `true`), and their Vault uses
  a self-signed CA without a `caSecret` configured, token resolution fails
  (uploads will 401/503). Confirm `caSecret` is set before they upgrade.

See [../../docs/analysis/glovebox-integration-review.md](../../docs/analysis/glovebox-integration-review.md)
for detailed integration context and upstream issues.

## Design background

`docs/specs/02-recognizer-gitlab-deploy.md` in the source repo describes
the chart's architecture, the migration from raw Kustomize manifests,
and the relationship to the orac gitops repo.

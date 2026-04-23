# Recognizer: GitLab CI, Helm Chart, and GitOps Deployment -- Design Specification

**Version 1.0 -- April 2026**
**Beads:** archiver-8j9

*This document defines the build, package, and deployment pipeline that gets recognizer (formerly "archiver") building cleanly on the homelab's self-hosted GitLab (`gitlab.orac.local`) and deployed to the orac Kubernetes cluster via the sibling gitops repo. It replaces the current GitHub Actions CI pipeline and the raw Kustomize manifests under `manifests/`.*

---

## 1. Problem Statement

Today the recognizer repo builds and ships via GitHub:

- `.github/workflows/ci.yml` runs tests, builds two multi-arch images (`document-scanner`, `notification-relay`), and pushes them to `ghcr.io/leftathome/recognizer/*`.
- `.github/workflows/release.yml` creates GitHub releases on `v*` tags.
- Cluster deployment was going to happen via raw Kustomize under `manifests/`, but that never got wired into the cluster. No gitops entry exists. There is no Helm chart.

The homelab cluster is self-sufficient: its own GitLab, its own container registry, its own Flux-driven GitOps. GHCR pulls work but are an external dependency that this cluster should not need. The cluster also has registry infrastructure (Zot + Spegel) available on-node, and secrets flow via External Secrets backed by Vault.

Separately, the project name was renamed from `archiver` to `recognizer` in the source (`go.mod` is `github.com/leftathome/recognizer/...`, Python tests reference `recognizer`) but the renaming is incomplete: the Kustomize manifests still use `namespace: archiver` and resource prefixes like `archiver-notification-relay`.

The goal of this spec is:

1. Build and publish container images and a packaged Helm chart from GitLab CI on `gitlab.orac.local`.
2. Deploy the chart to the orac cluster via a Flux `HelmRelease` declared in the gitops repo (under `clusters/orac/apps/`), following the same pattern used by other cluster-installed charts (Harbor, GitLab, Glovebox, Traefik, etc.).
3. Complete the archiver -> recognizer rename so that the name is consistent across source, images, chart, and running resources.
4. Delete the obsolete GitHub Actions workflows and the raw `manifests/` directory once the chart is verified equivalent.

## 2. Naming and Rename Scope

The project is **`recognizer`** everywhere going forward.

| Place | Before | After |
|---|---|---|
| Go module | `github.com/leftathome/recognizer/...` | unchanged |
| GitLab project | `steve/archiver` | `steve/recognizer` (renamed by user, 2026-04-22) |
| GitHub remote | `leftathome/recognizer.git` | unchanged |
| Git remote `gitlab` URL | `https://gitlab.orac.local/steve/archiver.git` | `https://gitlab.orac.local/steve/recognizer.git` |
| Image registry path | `ghcr.io/leftathome/recognizer/*` | `gitlab.orac.local:5050/steve/recognizer/*` |
| Helm chart name | (none) | `recognizer` |
| K8s namespace | `archiver` (in manifests) | `recognizer` (in chart) |
| K8s resource name prefixes | `archiver-notification-relay`, etc. | `recognizer-notification-relay`, etc. |
| Local working directory | `/mnt/c/Users/steve/Code/archiver` | user's choice; not in scope for this spec |

The local directory on disk is not renamed by this work. The user may `mv` it whenever convenient; the `.claude/projects/...-archiver/` cache path would need to be reset, and editor sessions would need to be restarted.

## 3. Helm Chart Structure

A single umbrella chart named `recognizer` lives at `charts/recognizer/` in this repo. It covers all three workloads plus shared supporting resources.

### 3.1 Chart layout

```
charts/recognizer/
  Chart.yaml                        # name: recognizer, version+appVersion tied to git tag
  values.yaml                       # defaults; all workloads enabled
  values.schema.json                # (optional) structural validation
  README.md                         # generated from values.yaml via helm-docs
  templates/
    _helpers.tpl                    # fullname, labels, image reference helpers
    namespace.yaml                  # guarded by values.createNamespace
    networkpolicies.yaml
    nfs-pv.yaml                     # cluster-scoped
    nfs-pvc.yaml
    document-scanner/
      daemonset.yaml
      configmap.yaml
      service.yaml
    notification-relay/
      deployment.yaml
      configmap.yaml
      service.yaml
      externalsecret.yaml
    optical-ripper/
      daemonset.yaml
      configmap.yaml
      service.yaml
      externalsecret.yaml
    monitoring/
      servicemonitor.yaml
      prometheusrule.yaml
    nfd-worker-config.yaml          # ConfigMap consumed by cluster-level NFD
    device-plugin-config.yaml       # ConfigMap consumed by smarter-device-manager
```

### 3.2 Values surface

Top-level values:

```yaml
image:
  registry: gitlab.orac.local:5050
  repository: steve/recognizer
  pullPolicy: IfNotPresent
  pullSecrets:
    - name: recognizer-registry

createNamespace: false             # namespace created by the Flux apps Kustomization

nfs:
  enabled: true
  server: <NAS hostname>
  path: /mnt/tank/recognizer

networkPolicies:
  enabled: true

monitoring:
  enabled: true

nodeFeatureDiscovery:
  enabled: true

devicePlugin:
  enabled: true

documentScanner:
  enabled: true
  image:
    name: document-scanner
    tag: ""                        # defaults to Chart.AppVersion
  # ... resource-specific values ...

notificationRelay:
  enabled: true
  image:
    name: notification-relay
    tag: ""
  # ...

opticalRipper:
  enabled: true
  image:
    name: optical-ripper
    tag: ""
  # ...
```

Image reference helper (in `_helpers.tpl`):

```
{{- define "recognizer.image" -}}
{{- $tag := .tag | default .root.Chart.AppVersion -}}
{{- printf "%s/%s/%s:%s" .root.Values.image.registry .root.Values.image.repository .name $tag -}}
{{- end -}}
```

Template usage: `image: {{ include "recognizer.image" (dict "root" $ "name" "document-scanner" "tag" .Values.documentScanner.image.tag) }}`

### 3.3 Scope decisions

**In scope (chart templates):** all resources currently under `manifests/`, including the cluster-scoped `nfs-pv` and the ConfigMaps that configure NFD and smarter-device-manager. Those ConfigMaps live *in the recognizer namespace* but describe what this app needs from those cluster-infra pieces, so they travel with the chart.

**Out of scope (separate gitops apps):** the NFD operator itself and the smarter-device-manager DaemonSet. These are cluster-infra and should be deployed independently. If they are not yet installed on the cluster, that is a separate blocker.

**Deletion of `manifests/`:** The chart replaces `manifests/` in the same commit that introduces it. Before that commit, a one-shot verification runs locally: `helm template charts/recognizer` is diffed against `kustomize build manifests/` and differences must be explainable (the namespace and resource-name rename, values defaulting). Only then does `manifests/` disappear.

## 4. GitLab CI Pipeline

`.gitlab-ci.yml` at the repo root. Four stages: `test`, `build`, `package`, `release`.

### 4.1 Stages and jobs

| Stage | Jobs | Trigger |
|---|---|---|
| `test` | `test:python`, `test:go`, `vuln:go`, `scan:trivy-fs` | MR and main |
| `build` | `build:document-scanner`, `build:notification-relay`, `build:optical-ripper` (multi-arch) | main and tags |
| `package` | `package:chart` (helm lint, helm template + kubeconform, helm package, helm push) | main and tags |
| `release` | `release:gitlab` (GitLab Release with changelog excerpt) | tags only |

### 4.2 Job specifics

- **Runner assumptions.** The cluster has GitLab Runners registered with a docker or kubernetes executor. Runners must be pre-configured to trust the orac cluster root CA so they can `git clone` from GitLab over HTTPS and push to `gitlab.orac.local:5050`. This is a pre-req for the pipeline to work at all; see Section 8.
- **Auth.** `CI_REGISTRY_USER`, `CI_REGISTRY_PASSWORD`, and `CI_REGISTRY` are predefined by GitLab for the container registry. `CI_JOB_TOKEN` is used for the Helm package registry and for the Release API. No manual secrets required.
- **Multi-arch builds.** `docker buildx` with `linux/amd64,linux/arm64` (matching the GitHub workflow). Requires `docker:dind` as a service, or a buildkit pod if using the kubernetes executor.
- **Image tag policy.**
  - `main` branch push -> tags `:latest` and `:main-<short-sha>`; no chart push.
  - `v*` semver tag -> images tagged `:<version>` (also `:latest` if this is a non-prerelease); chart packaged with matching version and pushed.
- **Chart registry.** OCI via the project's container registry: `oci://gitlab.orac.local:5050/steve/recognizer/charts`. `helm push recognizer-<version>.tgz oci://gitlab.orac.local:5050/steve/recognizer/charts`. Flux consumes this via an `OCIRepository` source (Section 5.1). The project-level HTTPS Helm package registry is the documented fallback if OCI auth proves difficult (Section 11).
- **Validation in `package:chart`.**
  - `helm lint charts/recognizer --strict`
  - `helm template charts/recognizer | kubeconform -strict -summary -schema-location default -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'`
  - `helm package charts/recognizer --version $VERSION --app-version $VERSION`
  - `helm push ...`

### 4.3 Release job

On `v*` tag:

- Extract the matching section from `CHANGELOG.md` (same awk logic as current `.github/workflows/release.yml`).
- Call GitLab Releases API with `CI_JOB_TOKEN`, tag name, release name, and extracted notes.

### 4.4 Security scans

The existing GitHub workflow runs `govulncheck` and `trivy fs`. Both port directly:

- `vuln:go` -- `go install golang.org/x/vuln/cmd/govulncheck@latest && govulncheck ./...` in the scanner module.
- `scan:trivy-fs` -- `aquasecurity/trivy` image, `trivy fs --exit-code 1 --severity HIGH,CRITICAL .`.

GitLab's built-in `Container-Scanning.gitlab-ci.yml` and `SAST.gitlab-ci.yml` templates are *not* adopted in v1; they duplicate what we already have, and keeping the existing tooling makes the migration a straight port rather than a rewrite. Adopting GitLab-native scans can happen later as a separate task.

## 5. GitOps Wiring (Flux)

The orac cluster is driven by Flux, not ArgoCD. The canonical layout in the gitops repo is:

- `clusters/orac/sources/` -- `HelmRepository` / `OCIRepository` resources that Flux uses to pull charts.
- `clusters/orac/apps/<name>.yaml` -- one `HelmRelease` per application.
- `clusters/orac/apps/kustomization.yaml` -- index of every file that the `apps` Kustomization reconciles.
- `clusters/orac/apps/namespace-<name>.yaml` and `externalsecret-<name>.yaml` -- colocated namespace/secret pre-reqs for an app, listed in the same index file.

The top-level `applications/` directory and `applicationset.yaml` are **vestigial** from an earlier ArgoCD era and should be ignored. Recognizer does not touch them.

The precedent is `glovebox` (`clusters/orac/apps/glovebox.yaml`): a `HelmRelease` referencing a `HelmRepository`, pinning a chart version, overriding image and values. Recognizer mirrors that shape.

### 5.1 OCIRepository source

Because the chart is published to the GitLab container registry over OCI (Section 4), the source type is Flux's `OCIRepository` rather than `HelmRepository`. Add to `clusters/orac/sources/` (either as a new file `oci-repositories.yaml` or appended to the existing sources file, matching whatever convention the repo already has):

```yaml
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: recognizer
  namespace: flux-system
spec:
  interval: 10m
  url: oci://gitlab.orac.local:5050/steve/recognizer/charts/recognizer
  ref:
    semver: ">=0.1.0 <1.0.0"
  secretRef:
    name: recognizer-registry-creds
  # If the cluster CA is already in Flux's trusted store (likely for a cluster
  # with the orac root CA propagated), no certSecretRef is needed. Otherwise
  # certSecretRef points at a Secret containing the CA bundle.
```

`semver` lets Flux roll forward through minor versions automatically while holding a major-version boundary for manual review. A strict pin (`tag: 0.1.0`) is the safer opening posture -- see Section 7 for how the release workflow interacts with this.

### 5.2 HelmRelease

`clusters/orac/apps/recognizer.yaml`:

```yaml
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: recognizer
  namespace: recognizer
spec:
  interval: 30m
  timeout: 10m
  chart:
    spec:
      chart: recognizer
      sourceRef:
        kind: OCIRepository
        name: recognizer
        namespace: flux-system
  install:
    createNamespace: false  # namespace created by the Kustomization
    remediation:
      retries: 5
  upgrade:
    remediation:
      retries: 3
  dependsOn:
    - name: longhorn
      namespace: longhorn-system
    - name: external-secrets
      namespace: external-secrets
  values:
    image:
      registry: gitlab.orac.local:5050
      repository: steve/recognizer
      pullSecrets:
        - name: recognizer-registry
    nfs:
      server: <actual-NAS>
      path: /mnt/tank/recognizer
    # Workload enables default to on; disable here per-cluster if the hardware
    # is absent.
```

Note on the `chart` field: when using `OCIRepository`, the `chart` name is the OCI artifact's chart name. Flux does not append the chart name to the OCIRepository URL -- the URL already points at the chart artifact. The `chart: recognizer` field is required by the schema but redundant; it must match what is inside the OCI artifact's `Chart.yaml`.

### 5.3 Namespace

`clusters/orac/apps/namespace-recognizer.yaml`:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: recognizer
```

### 5.4 Kustomization index

Append to `clusters/orac/apps/kustomization.yaml`:

```yaml
resources:
  # ... existing entries ...
  - namespace-recognizer.yaml
  - externalsecret-recognizer-registry.yaml   # if co-located here (see 5.5)
  - recognizer.yaml
```

Order matters only insofar as the namespace must exist before the ExternalSecret that lives in it, which must exist before the HelmRelease. Flux resolves dependencies via `dependsOn` on the HelmRelease, not by file order, but `kustomization.yaml` order is the conventional way to hint intent.

### 5.5 Registry pull credential

The cluster pulls the chart and the images from `gitlab.orac.local:5050`. Two separate credentials are needed:

- **Flux -> registry** (to pull the chart OCI artifact): the `recognizer-registry-creds` Secret referenced by the `OCIRepository` above. Created in the `flux-system` namespace.
- **Pods -> registry** (to pull images): the `recognizer-registry` Secret referenced in `imagePullSecrets`. Created in the `recognizer` namespace.

Both are backed by the same GitLab project deploy token (read-only scope) synced from Vault via External Secrets. Two `ExternalSecret` manifests (one in `flux-system`, one in `recognizer`) materialize them into their respective namespaces. The `flux-system` one lives in gitops alongside the other Flux-layer secrets; the `recognizer` one lives inside the Helm chart so it ships with the release.

If the cluster already uses an `imagePullSecrets`-injecting admission controller (kyverno, or serviceaccount-default pullSecret), the pod-side secret can be simplified; Section 11 lists this as a check-before-building item.

## 6. Secrets and External Dependencies

- **Image pull secret** (`recognizer-registry`): read-only deploy token for `steve/recognizer` on GitLab, synced into the `recognizer` namespace via ExternalSecret, referenced by every pod spec.
- **Flux OCIRepository credential** (`recognizer-registry-creds` in `flux-system`): same GitLab deploy token (or a separate one), synced into `flux-system` via an ExternalSecret, consumed by Flux for chart pulls.
- **Workload-specific secrets** (already present in the current manifests as `external-secret.yaml` for notification-relay and optical-ripper): carried forward into the chart unchanged.
- **ExternalSecret `SecretStore` reference**: must exist in the `recognizer` namespace. If the cluster uses a ClusterSecretStore, no new SecretStore is needed; if namespace-scoped, the chart creates one (or references a known shared one).

## 7. Versioning and Release Workflow

- Chart `version` and `appVersion` are the same string and come from the git tag.
- Default, untagged pushes to `main`: images tagged `:latest` and `:main-<short-sha>`; no chart is published (Flux's `semver` pin would not track it, and an arbitrary `latest` chart tag defeats GitOps-style review).
- Tagged releases (`v0.1.0`, `v0.2.0`, etc.): images get the version tag, chart is packaged and pushed at that version, GitLab Release is created.
- The gitops `OCIRepository` pins a chart version. Two strategies:
  - **Strict pin** (`tag: 0.1.0`): every upgrade is a PR against gitops that bumps the string. Explicit, auditable, matches how `glovebox` is pinned today.
  - **Semver range** (`semver: ">=0.1.0 <0.2.0"`): Flux rolls forward through patch/minor automatically; a major boundary still requires a PR. Less ceremony, but publishing a bad chart version auto-rolls the cluster.
  Opening posture: **strict pin** to match existing precedent. Revisit once the pipeline is proven.
- `Chart.AppVersion` drives default image tags in templates, so a single version bump moves everything in lockstep.

## 8. Prerequisites (not in scope; blockers if missing)

These are required for the pipeline to succeed; none are created by this work:

1. **GitLab Runner(s) registered against `gitlab.orac.local`** with a suitable executor (docker+dind or kubernetes) and the cluster root CA mounted so HTTPS git clone and container registry push both work.
2. **The `steve/recognizer` project exists on GitLab** (done by user, 2026-04-22).
3. **External Secrets operator is running** in the cluster with a functional `ClusterSecretStore` or equivalent.
4. **NFD and smarter-device-manager are installed** at the cluster level, outside this chart. If they are not installed, `documentScanner` and `opticalRipper` will not reach Ready status until they are.
5. **NFS server reachable** from cluster nodes at the path referenced in `values.yaml`.

## 9. Desired End State

When this work is complete and merged:

1. `https://gitlab.orac.local/steve/recognizer` shows a green pipeline on `main`.
2. Tagging `v0.1.0` on main triggers a pipeline that:
   - Publishes `gitlab.orac.local:5050/steve/recognizer/document-scanner:0.1.0`, `.../notification-relay:0.1.0`, `.../optical-ripper:0.1.0` (multi-arch).
   - Publishes the Helm chart at `oci://gitlab.orac.local:5050/steve/recognizer/charts/recognizer:0.1.0`.
   - Creates a GitLab Release `v0.1.0` with changelog excerpt.
3. The gitops repo has `clusters/orac/sources/` entry for the OCIRepository, `clusters/orac/apps/recognizer.yaml` (HelmRelease), `clusters/orac/apps/namespace-recognizer.yaml`, an ExternalSecret for the registry pull credential, and all three referenced from `clusters/orac/apps/kustomization.yaml`.
4. Flux reconciles the `recognizer` HelmRelease. The `recognizer` namespace contains:
   - `document-scanner` DaemonSet (running on scanner-equipped nodes)
   - `notification-relay` Deployment
   - `optical-ripper` DaemonSet (running on optical-drive-equipped nodes)
   - Associated ConfigMaps, Services, ExternalSecrets, NetworkPolicies
   - NFS PV/PVC bound
   - ServiceMonitor and PrometheusRule visible to Prometheus
5. The archiver repo no longer contains `.github/workflows/`, `manifests/`, or `CHANGELOG.md` references to the old image paths.
6. `bd close archiver-8j9` closes the tracking bead.

## 10. Implementation Order

Each step should be a separate commit (or small series of commits) and runnable as an incremental change:

1. **Update git remote.** `git remote set-url gitlab https://gitlab.orac.local/steve/recognizer.git`. Verify `git ls-remote gitlab` succeeds.
2. **Create the Helm chart** (`charts/recognizer/`): `Chart.yaml`, `values.yaml`, `_helpers.tpl`, and one template per current manifest file. Keep the archiver -> recognizer rename localized to the chart (do not touch `manifests/` yet).
3. **Verify equivalence locally.** `helm template charts/recognizer > /tmp/new.yaml`; `kustomize build manifests/ > /tmp/old.yaml`; diff and review. Expected differences: namespace rename, resource-name rename, image path. Unexpected differences: investigate and reconcile.
4. **Add `.gitlab-ci.yml`** with all four stages, but initially with the `build` stage `push: false` to smoke-test the pipeline without publishing. Push to a feature branch on GitLab and confirm jobs pass.
5. **Enable image push** on `main` and tags. Push to `main`; confirm images appear in the GitLab registry.
6. **Enable chart push**. Tag a throwaway `v0.0.1-rc.1`; confirm chart appears in the registry.
7. **Delete `manifests/` and `.github/workflows/`** in the same commit that flips the chart to the source of truth. Update `CHANGELOG.md`.
8. **Add gitops entries.** In the gitops repo: add the `OCIRepository` to `clusters/orac/sources/`, add `clusters/orac/apps/recognizer.yaml` (HelmRelease), `clusters/orac/apps/namespace-recognizer.yaml`, the registry ExternalSecret, and register all of them in `clusters/orac/apps/kustomization.yaml`. Commit and push. Flux reconciles within the `interval` on the cluster Kustomization.
9. **Verify cluster state.** `flux get sources oci`, `flux get helmreleases -n recognizer`, `kubectl -n recognizer get pods`. All healthy.
10. **Cut `v0.1.0`.** Update `CHANGELOG.md`, tag, push. Verify the full pipeline publishes images + chart + release, and (if the OCIRepository pin is `semver`) that Flux picks up the new version automatically, or (if `tag:`) that bumping the pin in gitops rolls the cluster forward.
11. **Close tracking bead** `archiver-8j9`.

## 11. Risks and Open Questions

- **GitLab Runner CA trust (Section 8.1)** is the most common failure mode. If runners cannot trust the self-signed cluster CA, every job fails at `git clone`. Verify before burning cycles on pipeline debugging.
- **`helm push` to OCI** on GitLab container registry requires GitLab >= 14.1 and a runner-side `helm` >= 3.8 with `HELM_EXPERIMENTAL_OCI=1` (implicit in helm 3.8+). Worth confirming GitLab version on `gitlab.orac.local` before committing to OCI (alternative is the project-level Helm package registry over HTTPS, which has been GA longer).
- **Flux OCIRepository + GitLab registry auth** is supported but less common than the HelmRepository + HTTPS path. If Flux cannot authenticate to `gitlab.orac.local:5050` (wrong secret shape, missing CA, GitLab-side token scope), the fallback is a `HelmRepository` pointing at GitLab's HTTPS Helm package registry (`https://gitlab.orac.local/api/v4/projects/<id>/packages/helm/stable`). The chart and CI stay the same -- only the source kind in gitops changes.
- **Pod imagePullSecret admission**: if the cluster already injects pull secrets via kyverno or a default-serviceaccount mutation, the chart's `externalsecret` for the pod-side pull credential is redundant. Check before building.
- **Multi-arch builds on the runner** need QEMU binfmt registered, or a runner pool with both amd64 and arm64 nodes. If neither is set up, drop to amd64-only in v1 and fix later.
- **Name collision during rename**: if anything external still expects `archiver` (e.g., NFS paths configured on the NAS, Prometheus scrape configs, downstream consumers subscribed to events from the old namespace), switching to `recognizer` breaks them. Audit before cutover; pause here if any of those are live.

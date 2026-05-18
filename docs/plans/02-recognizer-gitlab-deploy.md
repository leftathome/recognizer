# Recognizer: GitLab CI, Helm Chart, and Flux Deployment -- Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Get recognizer building cleanly on `gitlab.orac.local` (multi-arch images + OCI Helm chart) and deployed to the orac K8s cluster via Flux in the sibling gitops repo, replacing the existing GitHub Actions + raw Kustomize manifests.

**Spec:** `docs/specs/02-recognizer-gitlab-deploy.md`
**Beads:** `archiver-8j9` (this repo), depends on `gitops-5sz` (sibling repo) for cluster-side pull credentials.

**Architecture:** One umbrella Helm chart at `charts/recognizer/` packages the three workloads (document-scanner, notification-relay, optical-ripper) plus app-adjacent resources. `.gitlab-ci.yml` runs tests, builds multi-arch images, packages the chart, pushes both to `registry.orac.local/steve/recognizer/`. Flux on the cluster pulls from a `HelmRepository(type: oci)` source -- mirroring the `glovebox` precedent already in use.

**Tech Stack:** Helm 3.8+ (OCI), `docker buildx`, GitLab CI shared runners with docker+dind, Flux v2 (`helm.toolkit.fluxcd.io/v2`), kubeconform for schema validation.

**Repo boundary:** Tasks with IDs starting **A-G** live in this repo (`/mnt/c/Users/steve/Code/archiver`). Tasks **F1-F7** live in the sibling gitops repo (`/mnt/c/Users/steve/Code/gitops`). Task **F7** blocks on external bead `gitops-5sz`.

---

## File Structure

### Created in this repo

| Path | Purpose |
|---|---|
| `.gitlab-ci.yml` | Pipeline: test -> build -> package -> release |
| `charts/recognizer/Chart.yaml` | Chart metadata |
| `charts/recognizer/values.yaml` | Default values (all workloads enabled) |
| `charts/recognizer/.helmignore` | Standard ignore list |
| `charts/recognizer/templates/_helpers.tpl` | Name/label/image helpers |
| `charts/recognizer/templates/namespace.yaml` | Namespace (guarded) |
| `charts/recognizer/templates/networkpolicies.yaml` | From `manifests/base/network-policies.yaml` |
| `charts/recognizer/templates/nfs-pv.yaml` | From `manifests/base/nfs-pv.yaml`; carries `helm.sh/resource-policy: keep` |
| `charts/recognizer/templates/nfs-pvc.yaml` | From `manifests/base/nfs-pvc.yaml` |
| `charts/recognizer/templates/document-scanner/daemonset.yaml` | From `manifests/document-scanner/daemonset.yaml` |
| `charts/recognizer/templates/document-scanner/configmap.yaml` | From `manifests/document-scanner/configmap.yaml` |
| `charts/recognizer/templates/document-scanner/service.yaml` | From `manifests/document-scanner/service.yaml` |
| `charts/recognizer/templates/notification-relay/deployment.yaml` | From `manifests/notification-relay/deployment.yaml` |
| `charts/recognizer/templates/notification-relay/configmap.yaml` | From `manifests/notification-relay/configmap.yaml` |
| `charts/recognizer/templates/notification-relay/service.yaml` | From `manifests/notification-relay/service.yaml` |
| `charts/recognizer/templates/notification-relay/externalsecret.yaml` | From `manifests/notification-relay/external-secret.yaml` |
| `charts/recognizer/templates/optical-ripper/daemonset.yaml` | From `manifests/optical-ripper/daemonset.yaml` (image stays upstream: `automaticrippingmachine/automatic-ripping-machine:2.6.0`) |
| `charts/recognizer/templates/optical-ripper/configmap.yaml` | From `manifests/optical-ripper/configmap.yaml` |
| `charts/recognizer/templates/optical-ripper/service.yaml` | From `manifests/optical-ripper/service.yaml` |
| `charts/recognizer/templates/optical-ripper/externalsecret.yaml` | From `manifests/optical-ripper/external-secret.yaml` |
| `charts/recognizer/templates/monitoring/servicemonitor.yaml` | From `manifests/monitoring/servicemonitor.yaml` |
| `charts/recognizer/templates/monitoring/prometheusrule.yaml` | From `manifests/monitoring/prometheusrule.yaml` |
| `charts/recognizer/templates/tests/chart-render-test.yaml` | Helm chart test hook for smoke validation |

### Modified

| Path | Change |
|---|---|
| `CHANGELOG.md` | Entries for chart introduction, GitLab migration, v0.1.0 |

### Deleted (at Task E1)

| Path | Reason |
|---|---|
| `manifests/` (entire directory) | Replaced by chart |
| `.github/workflows/ci.yml` | Replaced by `.gitlab-ci.yml` |
| `.github/workflows/release.yml` | Replaced by GitLab release job |

### Created/modified in gitops repo

| Path | Change |
|---|---|
| `clusters/orac/sources/helm-repositories.yaml` | Append `HelmRepository(type: oci)` for recognizer |
| `clusters/orac/apps/recognizer.yaml` | New: `HelmRelease` |
| `clusters/orac/apps/namespace-recognizer.yaml` | New: `Namespace` |
| `clusters/orac/apps/configmap-nfd-worker-recognizer.yaml` | New: moved out of chart; lives in `node-feature-discovery` namespace |
| `clusters/orac/apps/configmap-smarter-device-manager-recognizer.yaml` | New: moved out of chart; lives in `capture` namespace |
| `clusters/orac/apps/kustomization.yaml` | Append the new resources |

---

## Prerequisites

Confirm before starting:

- `helm` >= 3.8 installed locally: `helm version --short`
- `kubeconform` installed locally: `kubeconform -v`
- `yq` (mikefarah's Go version) installed locally: `yq --version`
- `docker buildx` available: `docker buildx version`
- `kubectl` pointed at the orac cluster and `flux` CLI installed: `flux version`
- `bd` (beads) CLI installed, authenticated, and able to reach the project's `.beads/` backend: `bd list --status=open` lists `archiver-8j9`
- `glab` (optional but recommended for headless MR operations): `glab auth status`
- GitLab project `steve/recognizer` exists on `gitlab.orac.local` (confirmed with user 2026-04-22)
- Sibling gitops repo at `/mnt/c/Users/steve/Code/gitops` checked out to `main`, working tree clean
- A Python 3.12 venv usable for local `pytest` runs (verified in Task A2)

---

## Phase A -- Repo Preparation

### Task A0: Verify local test baseline (pre-CI sanity)

**Files:**
- (none; verification only)

Before writing any CI config, confirm the existing test suite actually runs cleanly on a fresh workstation. This catches any "the runner image won't have what's needed" surprises early.

- [ ] **Step 1: Create a fresh venv and install dependencies**

```bash
cd /mnt/c/Users/steve/Code/archiver
python3.12 -m venv .venv-check
source .venv-check/bin/activate
pip install -r requirements.txt
```

- [ ] **Step 2: Run the Python tests**

```bash
pytest tests/ -v --tb=short 2>&1 | tail -20
```

Expected: all existing tests pass, or fail only for reasons documented in the project's open bugs. If tests fail unexpectedly, STOP and fix those first -- CI cannot be green over a broken baseline.

- [ ] **Step 3: Run the Go tests**

```bash
cd images/document-scanner/scanner-session-manager
go vet ./...
go test ./... -count=1 -race
cd ../../..
```

Expected: all pass.

- [ ] **Step 4: Clean up**

```bash
deactivate
rm -rf .venv-check
```

- [ ] **Step 5: No commit** (verification only)

Exit criteria: local `pytest` and `go test` both pass.

### Task A1: Point the `gitlab` remote at the renamed project

**Files:**
- Modify: (none -- git config only)

- [ ] **Step 1: Verify current remote state**

```bash
cd /mnt/c/Users/steve/Code/archiver
git remote -v
```

Expected: `gitlab  https://gitlab.orac.local/steve/archiver.git (fetch/push)`

- [ ] **Step 2: Update the remote URL**

```bash
git remote set-url gitlab https://gitlab.orac.local/steve/recognizer.git
git remote -v
```

Expected: `gitlab  https://gitlab.orac.local/steve/recognizer.git (fetch/push)`

- [ ] **Step 3: Smoke-test the remote**

```bash
git ls-remote gitlab 2>&1 | head -5
```

Expected: a list of refs from the GitLab server. If this fails with a TLS error, stop and add the orac cluster CA to your local trust store before continuing.

- [ ] **Step 4: No commit** (git config is not tracked)

Exit criteria: `git ls-remote gitlab` prints refs. Stops here if it doesn't.

---

## Phase B -- Helm Chart

All of Phase B uses `helm template` + `kubeconform` as the red/green gate. The "failing test" in a freshly-cloned chart is `helm lint` reporting missing required fields; the "passing test" is lint + template + conform all clean.

### Task B1: Scaffold the chart

**Files:**
- Create: `charts/recognizer/Chart.yaml`
- Create: `charts/recognizer/values.yaml`
- Create: `charts/recognizer/.helmignore`
- Create: `charts/recognizer/templates/_helpers.tpl`

- [ ] **Step 1: Write the failing check**

```bash
cd /mnt/c/Users/steve/Code/archiver
helm lint charts/recognizer 2>&1 | tee /tmp/helm-lint-b1.txt
```

Expected: `Error: Chart.yaml file is missing` (because the directory doesn't exist yet).

- [ ] **Step 2: Create the chart directory and `Chart.yaml`**

Contents of `charts/recognizer/Chart.yaml`:

```yaml
apiVersion: v2
name: recognizer
description: Homelab archiver -- document scanners, optical rippers, notification relay
type: application
version: 0.1.0
appVersion: "0.1.0"
kubeVersion: ">=1.30.0-0"
maintainers:
  - name: Steve Wagner
    email: leftathome@gmail.com
keywords:
  - archive
  - capture
  - homelab
home: https://gitlab.orac.local/steve/recognizer
sources:
  - https://gitlab.orac.local/steve/recognizer
```

- [ ] **Step 3: Create `charts/recognizer/values.yaml`**

```yaml
image:
  registry: registry.orac.local
  repository: steve/recognizer
  pullPolicy: IfNotPresent
  pullSecrets:
    - name: recognizer-registry

createNamespace: false

commonLabels: {}
commonAnnotations: {}

nfs:
  enabled: true
  server: ""
  path: /mnt/tank/recognizer
  storageClassName: nfs-recognizer
  capacity: 1Ti
  reclaimPolicy: Retain

networkPolicies:
  enabled: true

monitoring:
  enabled: true

documentScanner:
  enabled: true
  image:
    name: document-scanner
    tag: ""
  resources:
    requests:
      cpu: 100m
      memory: 128Mi
    limits:
      cpu: 500m
      memory: 512Mi
  nodeSelector:
    feature.node.kubernetes.io/scanner: "true"

notificationRelay:
  enabled: true
  image:
    name: notification-relay
    tag: ""
  replicaCount: 2
  resources:
    requests:
      cpu: 50m
      memory: 64Mi
    limits:
      cpu: 250m
      memory: 256Mi

opticalRipper:
  enabled: true
  image:
    repository: automaticrippingmachine/automatic-ripping-machine
    tag: "2.6.0"
  resources:
    requests:
      cpu: 200m
      memory: 512Mi
    limits:
      cpu: "2"
      memory: 4Gi
  nodeSelector:
    smarter-devices/sr0: "1"
```

- [ ] **Step 4: Create `charts/recognizer/.helmignore`**

```
.DS_Store
.git/
.gitignore
.vscode/
*.tgz
*.swp
OWNERS
```

- [ ] **Step 5: Create `charts/recognizer/templates/_helpers.tpl`**

```tpl
{{/*
Expand the name of the chart.
*/}}
{{- define "recognizer.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "recognizer.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Common labels.
*/}}
{{- define "recognizer.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/name: {{ include "recognizer.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- with .Values.commonLabels }}
{{ toYaml . }}
{{- end }}
{{- end -}}

{{/*
Image reference. Usage: {{ include "recognizer.image" (dict "root" $ "name" "document-scanner" "tag" .Values.documentScanner.image.tag) }}
*/}}
{{- define "recognizer.image" -}}
{{- $tag := .tag | default .root.Chart.AppVersion -}}
{{- printf "%s/%s/%s:%s" .root.Values.image.registry .root.Values.image.repository .name $tag -}}
{{- end -}}
```

- [ ] **Step 6: Run the check to verify it passes**

```bash
helm lint charts/recognizer
helm template recognizer charts/recognizer > /tmp/helm-render-b1.yaml
wc -l /tmp/helm-render-b1.yaml
```

Expected: `==> Linting charts/recognizer` followed by `1 chart(s) linted, 0 chart(s) failed`. Rendered YAML is empty (0 lines) -- no templates yet -- that's fine.

- [ ] **Step 7: Commit**

```bash
git add charts/recognizer/Chart.yaml charts/recognizer/values.yaml \
        charts/recognizer/.helmignore charts/recognizer/templates/_helpers.tpl
git commit -m "feat(chart): scaffold recognizer Helm chart"
```

Exit criteria: `helm lint charts/recognizer` is clean.

### Task B2: Namespace + NFS + NetworkPolicies (shared/base)

**Files:**
- Create: `charts/recognizer/templates/namespace.yaml`
- Create: `charts/recognizer/templates/nfs-pv.yaml`
- Create: `charts/recognizer/templates/nfs-pvc.yaml`
- Create: `charts/recognizer/templates/networkpolicies.yaml`

- [ ] **Step 1: Red -- run kubeconform against empty render**

```bash
helm template recognizer charts/recognizer | kubeconform -strict -summary
```

Expected: `Summary: 0 resources found`.

- [ ] **Step 2: Write `templates/namespace.yaml`**

```yaml
{{- if .Values.createNamespace }}
apiVersion: v1
kind: Namespace
metadata:
  name: {{ .Release.Namespace }}
  labels:
    {{- include "recognizer.labels" . | nindent 4 }}
{{- end }}
```

- [ ] **Step 3: Write `templates/nfs-pv.yaml`**

Use `manifests/base/nfs-pv.yaml` as source. Replace hardcoded values with template references and add `helm.sh/resource-policy: keep`:

```yaml
{{- if .Values.nfs.enabled }}
apiVersion: v1
kind: PersistentVolume
metadata:
  name: {{ include "recognizer.fullname" . }}-nfs
  labels:
    {{- include "recognizer.labels" . | nindent 4 }}
  annotations:
    helm.sh/resource-policy: keep
spec:
  capacity:
    storage: {{ .Values.nfs.capacity }}
  accessModes:
    - ReadWriteMany
  persistentVolumeReclaimPolicy: {{ .Values.nfs.reclaimPolicy }}
  storageClassName: {{ .Values.nfs.storageClassName }}
  nfs:
    server: {{ required "nfs.server is required when nfs.enabled" .Values.nfs.server | quote }}
    path: {{ .Values.nfs.path | quote }}
{{- end }}
```

- [ ] **Step 4: Write `templates/nfs-pvc.yaml`**

```yaml
{{- if .Values.nfs.enabled }}
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: {{ include "recognizer.fullname" . }}-nfs
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "recognizer.labels" . | nindent 4 }}
spec:
  accessModes:
    - ReadWriteMany
  resources:
    requests:
      storage: {{ .Values.nfs.capacity }}
  storageClassName: {{ .Values.nfs.storageClassName }}
  volumeName: {{ include "recognizer.fullname" . }}-nfs
{{- end }}
```

- [ ] **Step 5: Write `templates/networkpolicies.yaml`**

Port from `manifests/base/network-policies.yaml`. Guard the whole file with `{{- if .Values.networkPolicies.enabled }}...{{- end }}`. Keep the existing rules exactly -- the chart's job is to template, not redesign. Reference labels via the helper.

- [ ] **Step 6: Green -- render and validate**

```bash
helm template recognizer charts/recognizer \
  --set nfs.server=nas.orac.local \
  --namespace recognizer > /tmp/helm-render-b2.yaml
kubeconform -strict -summary /tmp/helm-render-b2.yaml
```

Expected: all resources validate. Count: 1 PV + 1 PVC + N NetworkPolicies. (Namespace is not rendered because `createNamespace: false` by default; Flux-side gitops creates it.)

- [ ] **Step 7: Commit**

```bash
git add charts/recognizer/templates/
git commit -m "feat(chart): add namespace, NFS PV/PVC, and network policies"
```

Exit criteria: `helm template ... | kubeconform -strict` is clean with at least 3 resources rendered.

### Task B3a: Document-scanner DaemonSet template

**Files:**
- Create: `charts/recognizer/templates/document-scanner/daemonset.yaml`

This subtask carries a **fully-worked example** so the replacement pattern is unambiguous. B3b, B3c, B4, B5, and B6 all follow the same shape -- refer back here.

- [ ] **Step 1: Read the source manifest**

```bash
cat manifests/document-scanner/daemonset.yaml
```

- [ ] **Step 2: Create `templates/document-scanner/daemonset.yaml` with this content**

```yaml
{{- if .Values.documentScanner.enabled }}
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: {{ include "recognizer.fullname" . }}-document-scanner
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "recognizer.labels" . | nindent 4 }}
    app.kubernetes.io/component: document-scanner
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: {{ include "recognizer.name" . }}
      app.kubernetes.io/instance: {{ .Release.Name }}
      app.kubernetes.io/component: document-scanner
  template:
    metadata:
      labels:
        {{- include "recognizer.labels" . | nindent 8 }}
        app.kubernetes.io/component: document-scanner
    spec:
      {{- with .Values.image.pullSecrets }}
      imagePullSecrets:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      nodeSelector:
        {{- toYaml .Values.documentScanner.nodeSelector | nindent 8 }}
      containers:
        - name: document-scanner
          image: {{ include "recognizer.image" (dict "root" $ "name" .Values.documentScanner.image.name "tag" .Values.documentScanner.image.tag) }}
          imagePullPolicy: {{ .Values.image.pullPolicy }}
          # Port other fields from manifests/document-scanner/daemonset.yaml here:
          # - env (preserve as-is, just substituting any namespace references)
          # - volumeMounts
          # - securityContext
          # - readinessProbe/livenessProbe
          resources:
            {{- toYaml .Values.documentScanner.resources | nindent 12 }}
      # Port volumes from source manifest here; any NFS volume switches to:
      #   - name: nfs
      #     persistentVolumeClaim:
      #       claimName: {{ include "recognizer.fullname" . }}-nfs
{{- end }}
```

Everything after the `# Port other fields` comment preserves the source daemonset's behavior exactly -- walk the source YAML top-to-bottom and transliterate each field. The ONLY replacements are:

| Source | Template |
|---|---|
| `namespace: archiver` | `namespace: {{ .Release.Namespace }}` |
| Any hardcoded `name: archiver-document-scanner` | `name: {{ include "recognizer.fullname" . }}-document-scanner` |
| `image: <something>` | The helper line shown above |
| `imagePullPolicy: <something>` | `imagePullPolicy: {{ .Values.image.pullPolicy }}` |
| Top-level labels | `{{- include "recognizer.labels" . \| nindent 4 }}` + the component label |
| `resources: { requests: ..., limits: ... }` | `{{- toYaml .Values.documentScanner.resources \| nindent 12 }}` |
| `nodeSelector: {...}` | `{{- toYaml .Values.documentScanner.nodeSelector \| nindent 8 }}` |
| An `imagePullSecrets:` block (add if missing) | The `{{- with }}` block shown above |
| NFS-backed volume (ReadWriteMany PVC) | `claimName: {{ include "recognizer.fullname" . }}-nfs` |

Do not redesign the probes, volume mounts, env vars, or securityContext -- leave them exactly as source.

- [ ] **Step 3: Green -- render and validate**

```bash
helm template recognizer charts/recognizer --set nfs.server=nas.orac.local --namespace recognizer | yq 'select(.kind == "DaemonSet")' > /tmp/ds-rendered.yaml
kubeconform -strict -summary /tmp/ds-rendered.yaml
```

Expected: one DaemonSet, kubeconform reports `Summary: 1 resources found ... Valid: 1`.

- [ ] **Step 4: Toggle test**

```bash
helm template recognizer charts/recognizer --set nfs.server=nas.orac.local --set documentScanner.enabled=false --namespace recognizer | grep document-scanner
```

Expected: no output.

- [ ] **Step 5: Commit**

```bash
git add charts/recognizer/templates/document-scanner/daemonset.yaml
git commit -m "feat(chart): add document-scanner DaemonSet template"
```

Exit criteria: DaemonSet renders and validates; disable-toggle works.

### Task B3b: Document-scanner ConfigMap

**Files:**
- Create: `charts/recognizer/templates/document-scanner/configmap.yaml`

- [ ] **Step 1: Port `manifests/document-scanner/configmap.yaml`** using the same replacement table from B3a. ConfigMaps are simpler -- typically only the `namespace`, `name`, and `labels` lines change.

- [ ] **Step 2: Validate + toggle-test + commit** (same commands as B3a Steps 3-5).

```bash
git add charts/recognizer/templates/document-scanner/configmap.yaml
git commit -m "feat(chart): add document-scanner ConfigMap template"
```

### Task B3c: Document-scanner Service

**Files:**
- Create: `charts/recognizer/templates/document-scanner/service.yaml`

- [ ] **Step 1: Port `manifests/document-scanner/service.yaml`** same way.

- [ ] **Step 2: Validate + toggle-test + commit**.

```bash
git add charts/recognizer/templates/document-scanner/service.yaml
git commit -m "feat(chart): add document-scanner Service template"
```

### Task B4a: Notification-relay Deployment

**Files:**
- Create: `charts/recognizer/templates/notification-relay/deployment.yaml`

- [ ] **Step 1: Port `manifests/notification-relay/deployment.yaml`** using B3a's replacement table. Additional wiring: `replicas: {{ .Values.notificationRelay.replicaCount }}`. Image helper uses `.Values.notificationRelay.image.name` and `.tag`.

- [ ] **Step 2: Validate + toggle-test + commit**

```bash
helm template recognizer charts/recognizer --set nfs.server=nas.orac.local --namespace recognizer | yq 'select(.kind == "Deployment" and .metadata.name | contains("notification-relay"))' | kubeconform -strict -summary
git add charts/recognizer/templates/notification-relay/deployment.yaml
git commit -m "feat(chart): add notification-relay Deployment template"
```

### Task B4b: Notification-relay ConfigMap + Service

**Files:**
- Create: `charts/recognizer/templates/notification-relay/configmap.yaml`
- Create: `charts/recognizer/templates/notification-relay/service.yaml`

- [ ] **Step 1: Port both files** using the same rules.

- [ ] **Step 2: Validate + commit**

```bash
git add charts/recognizer/templates/notification-relay/{configmap,service}.yaml
git commit -m "feat(chart): add notification-relay ConfigMap and Service templates"
```

### Task B4c: Notification-relay ExternalSecret

**Files:**
- Create: `charts/recognizer/templates/notification-relay/externalsecret.yaml`

The ExternalSecret carries over its `spec.secretStoreRef`, `spec.target.template`, and `spec.data` blocks from the current manifest unchanged. Do not invent new fields. Kubeconform needs the CRDs-catalog schema location to find the `external-secrets.io/v1beta1` ExternalSecret schema.

- [ ] **Step 1: Port `manifests/notification-relay/external-secret.yaml`**. Add `{{- if .Values.notificationRelay.enabled }}...{{- end }}` around the whole resource.

- [ ] **Step 2: Validate with CRDs schema**

```bash
helm template recognizer charts/recognizer --set nfs.server=nas.orac.local --namespace recognizer \
  | yq 'select(.kind == "ExternalSecret")' \
  | kubeconform -strict -summary \
      -schema-location default \
      -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'
```

- [ ] **Step 3: Commit**

```bash
git add charts/recognizer/templates/notification-relay/externalsecret.yaml
git commit -m "feat(chart): add notification-relay ExternalSecret template"
```

### Task B5a: Optical-ripper DaemonSet

**Files:**
- Create: `charts/recognizer/templates/optical-ripper/daemonset.yaml`

Critical difference from B3a/B4a: optical-ripper image stays upstream (`automaticrippingmachine/automatic-ripping-machine:2.6.0`), not from our GitLab registry. Use the direct form, NOT the `recognizer.image` helper:

```yaml
image: "{{ .Values.opticalRipper.image.repository }}:{{ .Values.opticalRipper.image.tag }}"
```

- [ ] **Step 1: Port `manifests/optical-ripper/daemonset.yaml`** with the replacement table from B3a, substituting the image line above.

- [ ] **Step 2: Validate + toggle-test + commit**

```bash
git add charts/recognizer/templates/optical-ripper/daemonset.yaml
git commit -m "feat(chart): add optical-ripper DaemonSet template"
```

### Task B5b: Optical-ripper ConfigMap + Service

**Files:**
- Create: `charts/recognizer/templates/optical-ripper/configmap.yaml`
- Create: `charts/recognizer/templates/optical-ripper/service.yaml`

- [ ] **Step 1: Port both files** using B3a rules.

- [ ] **Step 2: Validate + commit**

```bash
git add charts/recognizer/templates/optical-ripper/{configmap,service}.yaml
git commit -m "feat(chart): add optical-ripper ConfigMap and Service templates"
```

### Task B5c: Optical-ripper ExternalSecret

**Files:**
- Create: `charts/recognizer/templates/optical-ripper/externalsecret.yaml`

- [ ] **Step 1: Port `manifests/optical-ripper/external-secret.yaml`** as a templated resource guarded by `{{- if .Values.opticalRipper.enabled }}`.

- [ ] **Step 2: Validate with CRDs schema + commit**

```bash
helm template recognizer charts/recognizer --set nfs.server=nas.orac.local --namespace recognizer \
  | yq 'select(.kind == "ExternalSecret")' \
  | kubeconform -strict -summary \
      -schema-location default \
      -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'
git add charts/recognizer/templates/optical-ripper/externalsecret.yaml
git commit -m "feat(chart): add optical-ripper ExternalSecret template"
```

### Task B6: Monitoring templates

**Files:**
- Create: `charts/recognizer/templates/monitoring/servicemonitor.yaml`
- Create: `charts/recognizer/templates/monitoring/prometheusrule.yaml`

- [ ] **Step 1: Red -- confirm no monitoring CRDs present**

```bash
helm template recognizer charts/recognizer --set nfs.server=nas.orac.local --namespace recognizer | grep -cE 'kind: (ServiceMonitor|PrometheusRule)'
```

Expected: `0`.

- [ ] **Step 2: Port both files**

Wrap both with `{{- if .Values.monitoring.enabled }}...{{- end }}`. Preserve the alert/rule logic exactly.

- [ ] **Step 3: Green -- render with monitoring CRDs**

```bash
helm template recognizer charts/recognizer --set nfs.server=nas.orac.local --namespace recognizer | kubeconform -strict -summary -schema-location default -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' | tee /tmp/kubeconform-b6.txt
```

Expected: `Summary: N resources found parsing stdin - Valid: N, Invalid: 0, Errors: 0, Skipped: 0`.

- [ ] **Step 4: Commit**

```bash
git add charts/recognizer/templates/monitoring/
git commit -m "feat(chart): add monitoring templates (ServiceMonitor + PrometheusRule)"
```

### Task B7: Equivalence diff against existing manifests

**Files:**
- (none created; verification only)

Requires `yq` (mikefarah's Go version) locally. Install with `go install github.com/mikefarah/yq/v4@latest` if not present.

- [ ] **Step 1: Render the chart**

```bash
cd /mnt/c/Users/steve/Code/archiver
helm template recognizer charts/recognizer \
  --namespace recognizer \
  --set nfs.server=nas.orac.local \
  > /tmp/chart-render.yaml
```

- [ ] **Step 2: Flatten existing manifests into one multi-doc stream**

```bash
find manifests -name '*.yaml' -type f -exec sh -c 'for f; do echo "---"; cat "$f"; done' sh {} + > /tmp/manifests-flat.yaml
```

The leading `---` per file guarantees valid multi-doc separators regardless of whether each source file already has one.

- [ ] **Step 3: Build structural summaries with `yq`**

```bash
yq eval-all '[.kind, .metadata.namespace // "(cluster-scoped)", .metadata.name] | @tsv' /tmp/chart-render.yaml | sort -u > /tmp/chart-summary.txt
yq eval-all '[.kind, .metadata.namespace // "(cluster-scoped)", .metadata.name] | @tsv' /tmp/manifests-flat.yaml | sort -u > /tmp/manifests-summary.txt
```

- [ ] **Step 4: Diff the two summaries**

```bash
diff /tmp/manifests-summary.txt /tmp/chart-summary.txt
```

Expected differences:
- `< ConfigMap    node-feature-discovery  nfd-worker-config` (only in old; moved to gitops in F4)
- `< ConfigMap    capture                 smarter-device-manager-config` (only in old; moved to gitops in F5)
- Rows where namespace changed from `archiver` to `recognizer`
- Rows where `name` changed from `archiver-*` to `<release>-*` (Helm prefixes with the release name, which is `recognizer` when we pass `--namespace recognizer` and the release name equals the namespace by convention)

Any other difference is unexpected; investigate before proceeding.

- [ ] **Step 5: No commit** (verification only). Save the diff output to the tracking bead:

```bash
bd update archiver-8j9 --notes="B7 equivalence diff: $(diff /tmp/manifests-summary.txt /tmp/chart-summary.txt | head -40)"
```

Exit criteria: only the expected three categories of differences appear in the diff.

---

## Phase C -- GitLab CI Pipeline (smoke-test mode)

Phase C gates happen on a feature branch pushed to GitLab. The "test" for each task is that the pipeline runs green.

### Task C1: `.gitlab-ci.yml` -- test stage only

**Files:**
- Create: `.gitlab-ci.yml`

- [ ] **Step 1: Red -- push empty pipeline; GitLab should show no pipeline**

(GitLab will not auto-create a pipeline until `.gitlab-ci.yml` exists.)

- [ ] **Step 2: Create the initial pipeline with test stage**

Contents of `.gitlab-ci.yml`:

```yaml
stages:
  - test

variables:
  GOCACHE: "$CI_PROJECT_DIR/.cache/go-build"
  GOPATH: "$CI_PROJECT_DIR/.cache/go"
  PIP_CACHE_DIR: "$CI_PROJECT_DIR/.cache/pip"

.cache:
  cache:
    key: "$CI_COMMIT_REF_SLUG"
    paths:
      - .cache/

test:python:
  stage: test
  image: python:3.12-slim
  extends: .cache
  before_script:
    - apt-get update && apt-get install -y --no-install-recommends curl ca-certificates
    - curl -sL https://github.com/yannh/kubeconform/releases/download/v0.6.7/kubeconform-linux-amd64.tar.gz | tar xz -C /usr/local/bin/
    - pip install -r requirements.txt
  script:
    - pytest tests/ -v --tb=short --junitxml=report-python.xml
  artifacts:
    when: always
    reports:
      junit: report-python.xml
    expire_in: 1 week

test:go:
  stage: test
  image: golang:1.26-bookworm
  extends: .cache
  variables:
    GO_MODULE_DIR: images/document-scanner/scanner-session-manager
  before_script:
    - go install github.com/jstemmer/go-junit-report/v2@latest
  script:
    - cd "$GO_MODULE_DIR"
    - go vet ./...
    - go test ./... -count=1 -race -v 2>&1 | go-junit-report -set-exit-code > "$CI_PROJECT_DIR/report-go.xml"
  artifacts:
    when: always
    reports:
      junit: report-go.xml
    expire_in: 1 week

vuln:go:
  stage: test
  image: golang:1.26-bookworm
  extends: .cache
  before_script:
    - go install golang.org/x/vuln/cmd/govulncheck@latest
  script:
    - cd images/document-scanner/scanner-session-manager
    - govulncheck ./...
  allow_failure: false

scan:trivy-fs:
  stage: test
  image: aquasec/trivy:latest
  script:
    - trivy fs --exit-code 1 --severity HIGH,CRITICAL --no-progress .
  allow_failure: false

helm:lint:
  stage: test
  image:
    name: alpine/helm:3.14.4
    entrypoint: [""]
  script:
    - helm lint charts/recognizer
    - helm template recognizer charts/recognizer --set nfs.server=nas.orac.local --namespace recognizer > /tmp/render.yaml
    - wc -l /tmp/render.yaml
  artifacts:
    paths:
      - /tmp/render.yaml
    expire_in: 1 week
```

- [ ] **Step 3: Push to a feature branch on GitLab and observe**

```bash
git checkout -b feat/gitlab-ci
git add .gitlab-ci.yml
git commit -m "ci: add test stage (smoke-test mode)"
git push -u gitlab feat/gitlab-ci
```

- [ ] **Step 4: Green -- verify pipeline passes**

Open `https://gitlab.orac.local/steve/recognizer/-/pipelines` in a browser. Expected: a pipeline runs against `feat/gitlab-ci`; the `test` stage has 5 green jobs (test:python, test:go, vuln:go, scan:trivy-fs, helm:lint).

If any job fails:
- Runner CA trust issues -> see spec Section 11, escalate to runner admin.
- Missing kubeconform schema for CRDs -> that is fine for `helm:lint` in this task (no CRDs rendered without `--set`s we haven't added yet).

Exit criteria: pipeline is green on `feat/gitlab-ci`.

### Task C2: Build stage, `push: false`

**Files:**
- Modify: `.gitlab-ci.yml`

- [ ] **Step 1: Red -- current pipeline has no build stage**

Confirm via GitLab UI that `build` does not appear in the pipeline.

- [ ] **Step 2: Append build stage and jobs**

Add `build` to `stages:` and append. Note: **no `build:optical-ripper` job** -- optical-ripper uses the upstream `automaticrippingmachine/automatic-ripping-machine:2.6.0` image and is not built from this repo.

```yaml
.build:
  stage: build
  image: docker:27
  services:
    - docker:27-dind
  variables:
    DOCKER_TLS_CERTDIR: "/certs"
  before_script:
    - docker info
    - docker buildx create --use --name builder
    - docker buildx inspect --bootstrap
  script:
    - >
      docker buildx build
      --platform linux/amd64,linux/arm64
      --file "$DOCKERFILE"
      --tag "$IMAGE_NAME:$CI_COMMIT_SHORT_SHA"
      --push=false
      --progress plain
      .

build:document-scanner:
  extends: .build
  variables:
    DOCKERFILE: images/document-scanner/Dockerfile
    IMAGE_NAME: dummy/document-scanner

build:notification-relay:
  extends: .build
  variables:
    DOCKERFILE: images/notification-relay/Dockerfile
    IMAGE_NAME: dummy/notification-relay
```

- [ ] **Step 3: Commit and push**

```bash
git add .gitlab-ci.yml
git commit -m "ci: add build stage (push=false smoke test)"
git push gitlab feat/gitlab-ci
```

- [ ] **Step 4: Green -- verify build jobs pass**

Expected: both `build:*` jobs run to completion. The build output shows `linux/amd64` and `linux/arm64` layers. No push happens (the `dummy/` image prefix would fail a push anyway -- this is a deliberate dead-man's switch).

Note: multi-arch buildx with `--push=false` does not load images into the local daemon; that is expected. The job has succeeded if buildx prints `pushing manifest ... (dry run)` or equivalent for each platform and exits 0.

If buildx fails because of missing QEMU binfmt, the runner pool is missing native arm64 nodes AND emulation registration. Fix on the runner side (this is a prereq) before proceeding.

Exit criteria: `build` jobs green on feature branch.

### Task C3: Package stage (lint + conform + `helm package`, no push)

**Files:**
- Modify: `.gitlab-ci.yml`

- [ ] **Step 1: Red -- no package stage yet**

- [ ] **Step 2: Append package stage**

Add `package` to `stages:` and append:

The `alpine/helm` image uses `helm` as its entrypoint, which breaks `apk add` in before_script. Override the entrypoint:

```yaml
package:chart:
  stage: package
  image:
    name: alpine/helm:3.14.4
    entrypoint: [""]
  before_script:
    - apk add --no-cache curl ca-certificates
    - curl -sL https://github.com/yannh/kubeconform/releases/download/v0.6.7/kubeconform-linux-amd64.tar.gz | tar xz -C /usr/local/bin/ kubeconform
  script:
    - VERSION="${CI_COMMIT_TAG:-0.0.0-$CI_COMMIT_SHORT_SHA}"
    - VERSION="${VERSION#v}"
    - helm lint charts/recognizer --strict
    - >
      helm template recognizer charts/recognizer
      --set nfs.server=nas.orac.local
      --namespace recognizer
      | kubeconform -strict -summary
        -schema-location default
        -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'
    - helm package charts/recognizer --version "$VERSION" --app-version "$VERSION" --destination .
    - ls -l recognizer-*.tgz
  artifacts:
    paths:
      - recognizer-*.tgz
    expire_in: 1 week
```

- [ ] **Step 3: Commit and push**

```bash
git add .gitlab-ci.yml
git commit -m "ci: add package stage with lint + kubeconform"
git push gitlab feat/gitlab-ci
```

- [ ] **Step 4: Green -- verify package job**

Expected: `package:chart` job passes. Artifact `recognizer-0.0.0-<sha>.tgz` is available on the job page.

Exit criteria: package job green on feature branch.

### Task C4: Release stage (tag-triggered, metadata only)

**Files:**
- Modify: `.gitlab-ci.yml`

- [ ] **Step 1: Red -- no release stage**

- [ ] **Step 2: Append release stage**

Add `release` to `stages:` and append:

```yaml
release:gitlab:
  stage: release
  image: registry.gitlab.com/gitlab-org/release-cli:latest
  rules:
    - if: $CI_COMMIT_TAG =~ /^v[0-9]+\.[0-9]+\.[0-9]+/
  before_script:
    - apk add --no-cache gawk
  script:
    - VERSION="${CI_COMMIT_TAG#v}"
    - awk "/^## \[${VERSION}\]/{found=1; next} /^## \[/{if(found) exit} found{print}" CHANGELOG.md > release-notes.md
    - cat release-notes.md
  release:
    tag_name: $CI_COMMIT_TAG
    name: $CI_COMMIT_TAG
    description: ./release-notes.md
```

- [ ] **Step 3: Commit and push**

```bash
git add .gitlab-ci.yml
git commit -m "ci: add release stage (tag-triggered)"
git push gitlab feat/gitlab-ci
```

- [ ] **Step 4: Green**

Expected: the release job does not run on branch pushes (because of the `rules:` gate). Pipeline shows `release:gitlab` as `skipped`. This is the intended state until D3 tags a release.

Exit criteria: release stage present; skipped on non-tag pushes.

---

## Phase D -- Enable Publishing

Phase D flips push-mode on. From here forward, main-branch pushes and tags cause real artifacts to land in the GitLab registry.

### Task D1: Enable image push on main and tags

**Files:**
- Modify: `.gitlab-ci.yml`

- [ ] **Step 1: Update the `.build` template to authenticate and push**

Replace the `build:*` jobs' `script:` block with:

Push runs on `main` and tags only. MR pipelines skip `build:*` entirely to avoid polluting the registry with speculative tags. The existing `test:*` jobs still run on MRs.

Note: there is no `build:optical-ripper` job. Optical-ripper uses the upstream image `automaticrippingmachine/automatic-ripping-machine:2.6.0` and is not built here.

```yaml
.build:
  stage: build
  image: docker:27
  services:
    - docker:27-dind
  variables:
    DOCKER_TLS_CERTDIR: "/certs"
  before_script:
    - docker info
    - echo "$CI_REGISTRY_PASSWORD" | docker login -u "$CI_REGISTRY_USER" --password-stdin "$CI_REGISTRY"
    - docker buildx create --use --name builder
    - docker buildx inspect --bootstrap
  script:
    - TAGS="--tag $CI_REGISTRY/steve/recognizer/$COMPONENT:$CI_COMMIT_SHORT_SHA"
    - if [ "$CI_COMMIT_REF_NAME" = "main" ]; then TAGS="$TAGS --tag $CI_REGISTRY/steve/recognizer/$COMPONENT:latest --tag $CI_REGISTRY/steve/recognizer/$COMPONENT:main-$CI_COMMIT_SHORT_SHA"; fi
    - if [ -n "$CI_COMMIT_TAG" ]; then VERSION="${CI_COMMIT_TAG#v}"; TAGS="$TAGS --tag $CI_REGISTRY/steve/recognizer/$COMPONENT:$VERSION"; fi
    - >
      docker buildx build
      --platform linux/amd64,linux/arm64
      --file "$DOCKERFILE"
      $TAGS
      --push
      --provenance=mode=max
      --sbom=true
      --progress plain
      .
  rules:
    - if: $CI_COMMIT_REF_NAME == "main"
    - if: $CI_COMMIT_TAG

build:document-scanner:
  extends: .build
  variables:
    DOCKERFILE: images/document-scanner/Dockerfile
    COMPONENT: document-scanner

build:notification-relay:
  extends: .build
  variables:
    DOCKERFILE: images/notification-relay/Dockerfile
    COMPONENT: notification-relay
```

- [ ] **Step 2: Commit and push to feature branch first (NOT main yet)**

```bash
git add .gitlab-ci.yml
git commit -m "ci: enable image push with GitLab registry auth"
git push gitlab feat/gitlab-ci
```

Expected: on the feature branch, the `build` jobs are **skipped** (neither `main` nor a tag). This is a safe intermediate state.

- [ ] **Step 3: Merge the feature branch into main**

Open an MR on GitLab (`feat/gitlab-ci` -> `main`), get a green pipeline, merge.

- [ ] **Step 4: Green -- verify main-branch pipeline publishes images**

After the merge runs on `main`:

```bash
# From the workstation, authenticate to the registry first if not already:
docker login registry.orac.local
docker pull registry.orac.local/steve/recognizer/document-scanner:latest
docker inspect registry.orac.local/steve/recognizer/document-scanner:latest | grep -E 'Architecture|Os'
```

Expected: image is multi-arch (docker inspect reveals a `Manifests` list containing both amd64 and arm64).

Exit criteria: `latest` and `main-<sha>` tags present in GitLab registry for both images.

### Task D2: Enable chart push (tag-only)

**Files:**
- Modify: `.gitlab-ci.yml`

- [ ] **Step 1: Append push step to `package:chart`**

Modify `package:chart.script:` -- append after `helm package`:

```yaml
    - >
      if [ -n "$CI_COMMIT_TAG" ]; then
        echo "$CI_REGISTRY_PASSWORD" | helm registry login -u "$CI_REGISTRY_USER" --password-stdin "$CI_REGISTRY"
        helm push recognizer-*.tgz "oci://$CI_REGISTRY/steve/recognizer/charts"
      else
        echo "Non-tag build; chart not pushed."
      fi
```

- [ ] **Step 2: Commit to main via feature branch**

```bash
git checkout main && git pull
git checkout -b feat/ci-chart-push
git add .gitlab-ci.yml
git commit -m "ci: enable OCI chart push on tags"
git push gitlab feat/ci-chart-push
# Open and merge MR via GitLab UI after the pipeline passes
```

- [ ] **Step 3: Green**

Expected: `package:chart` continues to pass on non-tag pushes (the push step is gated and prints the skip message).

Exit criteria: non-tag pipelines still green; chart push path compiles cleanly (but doesn't run yet).

### Task D3: Throwaway release to verify the full pipeline

**Files:**
- (none; a tag triggers CI)

- [ ] **Step 1: Cut throwaway tag**

```bash
git checkout main && git pull
git tag -a v0.0.1-rc.1 -m "pipeline smoke test"
git push gitlab v0.0.1-rc.1
```

- [ ] **Step 2: Observe the tag pipeline**

Open `https://gitlab.orac.local/steve/recognizer/-/pipelines`. Expected:
- `test` stage green
- `build` jobs push images tagged `0.0.1-rc.1`
- `package:chart` pushes `recognizer-0.0.1-rc.1.tgz` to OCI
- `release:gitlab` creates a GitLab Release (CHANGELOG might not have a matching entry; release-notes.md will be empty but the job still succeeds)

- [ ] **Step 3: Verify chart pull works**

```bash
helm pull oci://registry.orac.local/steve/recognizer/charts/recognizer --version 0.0.1-rc.1
ls -l recognizer-0.0.1-rc.1.tgz
tar tzf recognizer-0.0.1-rc.1.tgz | head
```

Expected: `helm pull` downloads the tarball; `tar tzf` shows `recognizer/Chart.yaml`, `recognizer/values.yaml`, and `recognizer/templates/...`.

- [ ] **Step 4: Delete the throwaway release**

Via GitLab UI: Releases -> v0.0.1-rc.1 -> Delete. Also delete the git tag:

```bash
git tag -d v0.0.1-rc.1
git push gitlab :refs/tags/v0.0.1-rc.1
```

- [ ] **Step 5: (Optional) Clean up the registry tag**

Deleting the git tag does not delete the container or chart tags in the registry. The `v0.0.1-rc.1` image tags and the `recognizer-0.0.1-rc.1.tgz` chart will remain. This is harmless because Task F3 pins `"0.1.0"` exactly (not a range), but it's cruft. If you care:

```bash
# Get numeric project ID
PROJECT_ID=$(glab api projects/steve%2Frecognizer 2>/dev/null | yq .id)
# Delete image tag via API (example for document-scanner)
glab api -X DELETE "projects/$PROJECT_ID/registry/repositories/$(glab api projects/$PROJECT_ID/registry/repositories 2>/dev/null | yq '.[] | select(.name == "document-scanner") | .id')/tags/0.0.1-rc.1"
# Repeat for notification-relay and charts/recognizer.
```

Skip this step on the first pass if you don't have `glab` authenticated -- it's cleanup, not a gate.

Exit criteria: the full publish path is known-working; throwaway release cleaned up (registry tags optional).

### Task D4: Pre-flight cluster checks

**Files:**
- (none; cluster-side verification)

This corresponds to spec Section 10.8. No changes to commit; record the output of each check in the beads issue.

- [ ] **Step 1: Check for collision with the old name**

```bash
kubectl get ns archiver 2>&1
```

Expected: `Error from server (NotFound): namespaces "archiver" not found`. If it exists, investigate what lives there before continuing.

- [ ] **Step 2: Check in-cluster DNS resolvability of `gitlab.orac.local`**

```bash
kubectl run -it --rm dns-probe --image=busybox:stable --restart=Never -- nslookup gitlab.orac.local
```

Expected: a valid IP address (`192.168.1.220` at time of spec). If this fails, CoreDNS / split-horizon DNS is broken for `.orac.local` -- this blocks all further Flux work.

- [ ] **Step 3: Check kubelet can pull from the registry**

```bash
cat <<YAML | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: registry-pull-probe
  namespace: default
spec:
  restartPolicy: Never
  containers:
    - name: probe
      image: registry.orac.local/steve/recognizer/document-scanner:latest
      command: ["sleep", "30"]
  imagePullSecrets:
    - name: recognizer-registry-test
YAML
```

This will fail with `ImagePullBackOff` until `gitops-5sz` lands a pull-secret. Record that outcome in the bead; do not block on it for D4 (but it gates F7).

```bash
kubectl delete pod registry-pull-probe
```

- [ ] **Step 4: Record findings**

```bash
bd update archiver-8j9 --notes="Pre-flight (D4): namespace archiver=NotFound OK; DNS resolution OK; registry pull=pending gitops-5sz."
```

Exit criteria: DNS resolvability and namespace collision checks pass; registry pull status documented.

---

## Phase E -- Cut Over

> **Execution order note:** Phase E runs AFTER Phase F1-F6 (gitops wiring) even though it appears above it in this document. The dependency graph is authoritative. F1-F6 needs the `manifests/nfd/*` and `manifests/device-plugin/*` source files as references while copying their ConfigMap content into gitops (F4, F5). E1 then deletes those source files. If you execute phases in document order, F4/F5 will have to pull the content from an earlier git revision.

### Task E1: Delete obsolete content

**Files:**
- Delete: `manifests/` (entire directory)
- Delete: `.github/workflows/ci.yml`
- Delete: `.github/workflows/release.yml`
- Delete: `.github/` (if empty after above)
- Modify: `CHANGELOG.md` (audit for GHCR paths)
- Modify: `README.md` if GHCR paths mentioned
- Modify: `docs/specs/01-archive-importer-pattern.md` if GHCR paths mentioned
- Delete: `tests/test_manifests.py`, `tests/test_layer1_manifests.py`, `tests/test_document_scanner.py`, `tests/test_optical_ripper.py` (all read from `../manifests/`)

**Safety note:** This task is safe because the raw `manifests/` directory is NOT currently applied to any cluster -- it was scaffolding for an earlier deploy plan that never landed (see spec Section 1). So removing `manifests/` does not disturb any running state. If that ever changes, re-sequence E1 after F6.

- [ ] **Step 1: Red -- confirm current state**

```bash
ls manifests/ .github/workflows/
```

Expected: both directories exist.

- [ ] **Step 2: Audit for GHCR references**

```bash
grep -rln 'ghcr.io\|leftathome/recognizer\|leftathome/archiver' --include='*.md' --include='*.yaml' --include='*.yml' | grep -v '^charts/' | grep -v '^docs/specs/02-' | grep -v '^docs/plans/02-'
```

Review each hit and decide: rewrite, annotate as historical, or leave (docs describing the *past* state are fine if clearly scoped).

- [ ] **Step 3: Remove obsolete files**

```bash
git rm -r manifests/ .github/workflows/
rmdir .github 2>/dev/null || true  # if it only contained workflows
git rm tests/test_manifests.py tests/test_layer1_manifests.py tests/test_document_scanner.py tests/test_optical_ripper.py
```

Rationale for removing those tests: each one does `yaml.safe_load(...)` + `kubeconform` checks against the `manifests/` directory. That role is now served by the `helm:lint` CI job (which runs `helm template | kubeconform`). Keeping them would require a non-trivial rewrite (helm rendering in-process) for no gain.

`tests/test_schemas.py` stays -- it validates JSON schemas, not manifests.

- [ ] **Step 4: Update CHANGELOG**

Add a new entry under `## [Unreleased]` or bump the next-version section:

```markdown
### Changed
- Build and release pipeline moved from GitHub Actions + GHCR to GitLab CI + gitlab.orac.local container registry.
- Kubernetes manifests replaced by a Helm chart at `charts/recognizer/`; cluster deployment now via Flux in the sibling gitops repo.
- Images republished as `registry.orac.local/steve/recognizer/{document-scanner,notification-relay}`.

### Removed
- `manifests/` directory (replaced by chart)
- `.github/workflows/` (replaced by `.gitlab-ci.yml`)
```

- [ ] **Step 5: Green -- verify repo is coherent**

```bash
helm lint charts/recognizer
helm template recognizer charts/recognizer --set nfs.server=nas.orac.local --namespace recognizer | kubeconform -strict -summary -schema-location default -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'
grep -rln 'ghcr.io' --include='*.md' --include='*.yaml' --include='*.yml' | grep -v historical
```

Expected: chart still clean; no stray GHCR refs outside of clearly-historical contexts.

- [ ] **Step 6: Commit and push via MR**

```bash
git checkout -b chore/drop-old-manifests
git add -A
git commit -m "chore: remove GitHub Actions + manifests/ (replaced by chart + .gitlab-ci.yml)"
git push gitlab chore/drop-old-manifests
# Merge via GitLab UI after pipeline passes
```

Exit criteria: main branch no longer contains `manifests/` or `.github/workflows/`; full pipeline still green.

---

## Phase F -- Gitops Wiring

**All tasks in Phase F happen in the sibling gitops repo:** `/mnt/c/Users/steve/Code/gitops`.

**Execution order note:** Tasks F1-F6 run BEFORE E1. Task F7 runs AFTER G2 (because F7 needs the 0.1.0 chart artifact to be in the registry). F7 additionally blocks on external bead `gitops-5sz` for cluster-side pull credentials. See the Dependency Graph section.

### Task F1: Append the HelmRepository source

**Files:**
- Modify: `clusters/orac/sources/helm-repositories.yaml`

- [ ] **Step 1: Red -- confirm recognizer source doesn't exist**

```bash
cd /mnt/c/Users/steve/Code/gitops
grep -A 1 'name: recognizer' clusters/orac/sources/helm-repositories.yaml 2>&1 || echo "not present"
```

Expected: `not present`.

- [ ] **Step 2: Append source**

Append to the end of `clusters/orac/sources/helm-repositories.yaml`:

```yaml
---
apiVersion: source.toolkit.fluxcd.io/v1
kind: HelmRepository
metadata:
  name: recognizer
  namespace: flux-system
spec:
  type: oci
  interval: 1h
  url: oci://registry.orac.local/steve/recognizer/charts
  secretRef:
    name: recognizer-registry-creds
```

- [ ] **Step 3: Green -- validate**

```bash
kubeconform -strict -schema-location default -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' clusters/orac/sources/helm-repositories.yaml
```

Expected: clean.

- [ ] **Step 4: Commit (do not push yet; bundle F1-F6 into one push)**

```bash
git checkout -b feat/recognizer
git add clusters/orac/sources/helm-repositories.yaml
git commit -m "feat(flux): add recognizer HelmRepository source (oci)"
```

### Task F2: Namespace manifest

**Files:**
- Create: `clusters/orac/apps/namespace-recognizer.yaml`

- [ ] **Step 1: Create file**

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: recognizer
  labels:
    app.kubernetes.io/part-of: recognizer
```

- [ ] **Step 2: Commit**

```bash
git add clusters/orac/apps/namespace-recognizer.yaml
git commit -m "feat(flux): add recognizer namespace"
```

### Task F3: HelmRelease

**Files:**
- Create: `clusters/orac/apps/recognizer.yaml`

- [ ] **Step 1: Create file**

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
      version: "0.1.0"
      sourceRef:
        kind: HelmRepository
        name: recognizer
        namespace: flux-system
  install:
    createNamespace: false
    remediation:
      retries: 5
  upgrade:
    remediation:
      retries: 3
  dependsOn:
    - name: longhorn
      namespace: longhorn-system
  values:
    image:
      registry: registry.orac.local
      repository: steve/recognizer
      pullSecrets:
        - name: recognizer-registry
    nfs:
      server: REPLACE-ME.example.invalid   # MUST be set before push
      path: /mnt/tank/recognizer
```

Before committing, replace `REPLACE-ME.example.invalid` with the real NAS. The placeholder parses as valid YAML so YAML tools will not catch the mistake; `kubectl describe helmrelease recognizer -n recognizer` after deploy would show the bad value in values -- but at that point you've already merged bad config. If you don't know the NAS address, stop and ask.

- [ ] **Step 2: Validate**

```bash
kubeconform -strict -schema-location default -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' clusters/orac/apps/recognizer.yaml
```

- [ ] **Step 3: Commit**

```bash
git add clusters/orac/apps/recognizer.yaml
git commit -m "feat(flux): add recognizer HelmRelease"
```

### Task F4: NFD worker ConfigMap

**Files:**
- Create: `clusters/orac/apps/configmap-nfd-worker-recognizer.yaml`

Content is embedded below -- do NOT depend on `manifests/nfd/nfd-worker-config.yaml` being present, because E1 may run first.

- [ ] **Step 1: Create the file with exactly this content**

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: nfd-worker-config
  namespace: node-feature-discovery
  labels:
    app.kubernetes.io/part-of: recognizer
data:
  nfd-worker.conf: |
    sources:
      usb:
        deviceClassWhitelist:
          - "08"
          - "ff"
        deviceLabelFields:
          - "vendor"
          - "device"
    rules:
      - name: "pioneer-bdr-xs07uhd"
        labels:
          "recognizer.io/device-optical-drive": "pioneer-bdr-xs07uhd"
        matchFeatures:
          - feature: usb.device
            matchExpressions:
              vendor:
                op: In
                value:
                  - "07e8"
              # device:
              #   op: In
              #   value:
              #     - "XXXX"  # TODO: fill in after first plug-in via lsusb

      - name: "epson-ds-1630"
        labels:
          "recognizer.io/device-scanner": "epson-ds-1630"
        matchFeatures:
          - feature: usb.device
            matchExpressions:
              vendor:
                op: In
                value:
                  - "04b8"
              # device:
              #   op: In
              #   value:
              #     - "XXXX"  # TODO: fill in after first plug-in via lsusb
```

The `app.kubernetes.io/part-of` label changed from `capture-framework` to `recognizer` to match the new project name.

- [ ] **Step 2: Validate**

```bash
kubeconform -strict clusters/orac/apps/configmap-nfd-worker-recognizer.yaml
```

- [ ] **Step 3: Commit**

```bash
git add clusters/orac/apps/configmap-nfd-worker-recognizer.yaml
git commit -m "feat(flux): add recognizer NFD worker config"
```

### Task F5: Smarter-device-manager ConfigMap

**Files:**
- Create: `clusters/orac/apps/configmap-smarter-device-manager-recognizer.yaml`

- [ ] **Step 1: Create the file with exactly this content**

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: smarter-device-manager-config
  namespace: capture
  labels:
    app.kubernetes.io/part-of: recognizer
data:
  conf.yaml: |
    - devicematch: ^sr[0-9]+$
      nummaxdevices: 1
    - devicematch: ^sg[0-9]+$
      nummaxdevices: 1
    - devicematch: ^bus/usb/
      nummaxdevices: 4
```

- [ ] **Step 2: Validate and commit**

```bash
kubeconform -strict clusters/orac/apps/configmap-smarter-device-manager-recognizer.yaml
git add clusters/orac/apps/configmap-smarter-device-manager-recognizer.yaml
git commit -m "feat(flux): add recognizer device-plugin config"
```

### Task F6: Register all in apps kustomization

**Files:**
- Modify: `clusters/orac/apps/kustomization.yaml`

- [ ] **Step 1: Append resource references**

Add to the `resources:` list (keeping existing order):

```yaml
  - namespace-recognizer.yaml
  - configmap-nfd-worker-recognizer.yaml
  - configmap-smarter-device-manager-recognizer.yaml
  - recognizer.yaml
```

- [ ] **Step 2: Validate the overall kustomization renders**

```bash
kustomize build clusters/orac/apps > /tmp/apps-render.yaml
grep -c recognizer /tmp/apps-render.yaml
```

Expected: at least 4 matches (Namespace, 2 ConfigMaps, HelmRelease).

- [ ] **Step 3: Commit and push the whole feature branch**

```bash
git add clusters/orac/apps/kustomization.yaml
git commit -m "feat(flux): register recognizer resources in apps kustomization"
git push origin feat/recognizer
```

Open an MR on GitLab (gitops project); merge after review.

### Task F7: Verify Flux reconciles the new HelmRelease

**Blocked on:** `gitops-5sz` (cluster-side pull credentials). Without that, the HelmRepository stays `FetchFailed`.

- [ ] **Step 1: Watch Flux reconcile**

```bash
flux reconcile kustomization apps -n flux-system --with-source
flux get helmreleases -n recognizer
flux get sources helm -n flux-system | grep recognizer
```

Expected (after `gitops-5sz` lands): `HelmRepository/recognizer` shows `Fetched revision: 0.1.0`, `HelmRelease/recognizer` shows `Release reconciliation succeeded`.

Before `gitops-5sz`: both show `FetchFailed: authentication required`. That is acceptable; document and wait.

- [ ] **Step 2: Verify cluster state**

```bash
kubectl get pods,svc,cm,externalsecrets -n recognizer
kubectl get cm -n node-feature-discovery | grep nfd-worker-config
kubectl get cm -n capture | grep smarter-device-manager-config
kubectl get pv | grep recognizer
```

Expected: all workloads running (or at least `Pending` with a clear reason like "waiting for NFD labels"), configs present in their target namespaces, PV bound.

- [ ] **Step 3: Update the tracking bead**

```bash
cd /mnt/c/Users/steve/Code/archiver
bd update archiver-8j9 --notes="F7 complete: Flux reconciles recognizer; all pods running."
```

Exit criteria: `flux get hr -n recognizer` shows `READY: True`.

---

## Phase G -- First Real Release

### Task G1: Update CHANGELOG for v0.1.0

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Add the v0.1.0 section**

Follow existing CHANGELOG style. Summarize what v0.1.0 delivers (the migration + the chart + the first green Flux reconcile).

- [ ] **Step 2: Commit via MR**

```bash
cd /mnt/c/Users/steve/Code/archiver
git checkout main && git pull
git checkout -b release/v0.1.0
git add CHANGELOG.md
git commit -m "docs: prep v0.1.0 changelog entry"
git push gitlab release/v0.1.0
# Merge after pipeline passes
```

### Task G2: Tag v0.1.0 and verify the pipeline publishes

**Files:**
- (tag only)

- [ ] **Step 1: Cut the tag**

```bash
git checkout main && git pull
git tag -a v0.1.0 -m "First GitLab-published release"
git push gitlab v0.1.0
```

- [ ] **Step 2: Observe pipeline**

Expected: test, build (images tagged `0.1.0` and `latest`), package (chart `0.1.0` pushed), release (GitLab Release created with CHANGELOG excerpt).

- [ ] **Step 3: Verify**

```bash
docker pull registry.orac.local/steve/recognizer/document-scanner:0.1.0
helm pull oci://registry.orac.local/steve/recognizer/charts/recognizer --version 0.1.0
```

Both succeed.

Exit criteria: v0.1.0 artifacts exist and are pullable.

### Task G3: Roll the cluster forward (if HelmRelease isn't already pinned at 0.1.0)

**Files:**
- Modify (in gitops): `clusters/orac/apps/recognizer.yaml`

- [ ] **Step 1: Confirm current pin**

```bash
cd /mnt/c/Users/steve/Code/gitops
grep 'version:' clusters/orac/apps/recognizer.yaml
```

If it already says `"0.1.0"` (set in F3), there's nothing to do here -- Flux picks up the now-existing 0.1.0 chart on its next reconcile. Skip to step 4.

If not, continue.

- [ ] **Step 2: Update the pin**

Edit `clusters/orac/apps/recognizer.yaml`, set `chart.spec.version: "0.1.0"`.

- [ ] **Step 3: Commit via MR**

```bash
git checkout -b feat/recognizer-0.1.0
git add clusters/orac/apps/recognizer.yaml
git commit -m "feat(flux): pin recognizer HelmRelease to 0.1.0"
git push origin feat/recognizer-0.1.0
# Merge via GitLab UI
```

- [ ] **Step 4: Verify Flux picks it up**

```bash
flux reconcile helmrelease recognizer -n recognizer --with-source
flux get hr -n recognizer
```

Exit criteria: HelmRelease `READY: True` at `revision: 0.1.0`; pods running with images from `registry.orac.local/steve/recognizer/*:0.1.0`.

### Task G4: Close tracking beads

- [ ] **Step 1: Close archiver-8j9**

```bash
cd /mnt/c/Users/steve/Code/archiver
bd close archiver-8j9 --reason="Spec delivered; recognizer builds on GitLab, chart published, Flux reconciled, v0.1.0 cut."
```

- [ ] **Step 2: Remember the lessons**

```bash
bd remember "Flux HelmRepository(type: oci) + GitLab container registry works with CI_REGISTRY_USER/PASSWORD auth. URL is a prefix; HelmRelease supplies chart+version. Matches glovebox/ghcr precedent exactly."
```

- [ ] **Step 3: Push beads state**

```bash
bd dolt push
git push gitlab main  # if any local-only commits remain
```

Exit criteria: `bd list --status=open` no longer shows `archiver-8j9`.

---

## Dependency Graph

```
A0 (local test baseline) -> A1 (git remote)
                              |
                              v
B1 (chart scaffold) -> B2 (namespace/NFS/netpol)
 -> B3a (doc-scanner DaemonSet) -> B3b (doc-scanner CM) -> B3c (doc-scanner Service)
 -> B4a (notif-relay Deployment) -> B4b (notif-relay CM+Service) -> B4c (notif-relay ExternalSecret)
 -> B5a (optical-ripper DaemonSet) -> B5b (optical-ripper CM+Service) -> B5c (optical-ripper ExternalSecret)
 -> B6 (monitoring) -> B7 (equivalence diff)
                         |
                         v
                        C1 (test stage) -> C2 (build stage, push=false) -> C3 (package stage) -> C4 (release stage)
                                                                                                            |
                                                                                                            v
                                                                                                           D1 (image push) -> D2 (chart push) -> D3 (smoke v0.0.1-rc.1) -> D4 (pre-flight)
                                                                                                                                                                              |
                                                                                                                                                                              v
F1 (HelmRepository source) -> F2 (namespace) -> F3 (HelmRelease) -> F4 (NFD cm) -> F5 (device-plugin cm) -> F6 (register in kustomization)
                                                                                                                                       |
                                                                                                                                       v
                                                                                                                                      E1 (cutover: delete manifests/ + old tests + .github/)
                                                                                                                                       |
                                                                                                                                       v
                                                                                                                                      G1 (CHANGELOG v0.1.0) -> G2 (tag v0.1.0, pipeline publishes)
                                                                                                                                       |
                                                                                                                                       v
                                                                                                                                      F7 (verify Flux reconciles; blocks on gitops-5sz)
                                                                                                                                       |
                                                                                                                                       v
                                                                                                                                      G3 (pin HelmRelease to 0.1.0 if needed) -> G4 (close beads)
```

Re-ordered: **F1-F6 run before E1**, so the gitops repo gets its ConfigMap content copies made from the still-present `manifests/` before E1 removes them. F7 runs after G2 because it needs the 0.1.0 chart artifact to exist in the registry. F7 also blocks on external bead `gitops-5sz` for cluster-side pull credentials.

---

## Exit Criteria (overall)

Reconfirming spec Section 9:

- [ ] `https://gitlab.orac.local/steve/recognizer` shows a green pipeline on main
- [ ] `docker pull registry.orac.local/steve/recognizer/{document-scanner,notification-relay}:0.1.0` succeeds on a workstation authenticated to the GitLab registry
- [ ] `helm pull oci://registry.orac.local/steve/recognizer/charts/recognizer --version 0.1.0` succeeds
- [ ] `flux get hr -n recognizer` shows `READY: True`, `revision: 0.1.0`
- [ ] `kubectl -n recognizer get pods` shows all workloads running
- [ ] `kubectl -n node-feature-discovery get cm nfd-worker-config` exists
- [ ] `kubectl -n capture get cm smarter-device-manager-config` exists
- [ ] The archiver repo no longer contains `.github/workflows/`, `manifests/`, or un-annotated `ghcr.io/leftathome/recognizer` references
- [ ] Beads `archiver-8j9` is closed

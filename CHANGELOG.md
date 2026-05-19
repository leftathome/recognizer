# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Helm chart** `charts/recognizer/` packaging all three workloads
  (document-scanner, notification-relay, optical-ripper) plus shared
  resources (namespace, NFS PV/PVC, NetworkPolicies, monitoring).
- **GitLab CI pipeline** (`.gitlab-ci.yml`): test, build, package, release
  stages. Multi-stage with kaniko-based image builds (amd64 only for now)
  and OCI Helm chart push to `registry.orac.local`.

### Changed

- Migrated build + release pipeline from GitHub Actions + GHCR to GitLab
  CI on `gitlab.orac.local`. Images now published to
  `registry.orac.local/steve/recognizer/{document-scanner,notification-relay}`.
  Chart published to `oci://registry.orac.local/steve/recognizer/charts/recognizer`.
- Renamed `openclaw.io/device-*` NFD labels to `recognizer.io/device-*`
  and `app.kubernetes.io/part-of: capture-framework` to `recognizer`
  (vestigial labels from before the project split off).

### Removed

- `manifests/` directory (replaced by the Helm chart).
- `.github/workflows/` (replaced by `.gitlab-ci.yml`).
- `tests/test_manifests.py`, `tests/test_layer1_manifests.py`,
  `tests/test_document_scanner.py`, `tests/test_optical_ripper.py` --
  these tested the static `manifests/` files; that role is now served
  by `helm:lint` + `kubeconform` in CI against the chart's rendered
  output.

## [0.1.0] - 2026-04-05

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

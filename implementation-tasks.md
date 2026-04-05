# Implementation Tasks - Phase 1 Breakdown

## Dependency Graph

```
Layer 0: Foundation (no deps)
  [1] Namespace + RBAC manifests
  [2] NFS PV/PVC manifests
  [3] Schema validation test harness

Layer 1: Detection + Security (depends on namespace)
  [4] NFD custom USB rules           -> [1]
  [5] Smarter Device Manager config  -> [1]
  [6] Cilium network policies        -> [1]
  [7] ExternalSecret resources       -> [1]

Layer 2: Notification Relay (depends on L1)
  [8]  Relay: event validation logic      -> [3]
  [9]  Relay: webhook fan-out + config    -> [8]
  [10] Relay: retry + dead-letter         -> [9]
  [11] Relay: Dockerfile + manifests      -> [10, 6, 7]

Layer 3a: Optical Ripper (depends on L1 + relay)
  [12] ARM ConfigMap (arm.yaml overrides) -> [2, 7]
  [13] ARM post-rip notification hook     -> [8]
  [14] ARM DaemonSet manifest             -> [4, 5, 12, 13]

Layer 3b: Document Scanner — Go (depends on L1 + relay)
  [15] SANE interface wrapper (Go)                -> [3]
  [16] Session lifecycle (ADF + flatbed, Go)      -> [15]
  [17] Manifest generation (Go)                   -> [16, 3]
  [18] Scanner notification dispatch (Go)         -> [17, 8]
  [19] Scanner web UI (Go, net/http)              -> [16, 18]
  [20] Scanner Dockerfile + DaemonSet manifest    -> [19, 4, 5, 6]

Layer 4: Observability (soft dep on Prometheus Operator being installed)
  [21] Prometheus ServiceMonitor + alert rules -> [11, 14, 20]
  [22] Grafana dashboard                       -> [21]

Note: Flux Kustomization pointing at this repo lives in the gitops repo, not here.
```

#!/usr/bin/env bash
# Promote the recognizer-archive-importer CronJob into a one-off Job
# targeting a specific archive. Stock kubectl + yq.
#
# Usage:
#   scripts/run-job.sh <archive-filename>
#     where <archive-filename> is relative to /data/incoming/archives/raw/
#     and already lives there on the chart's NFS / Longhorn PVC.

set -euo pipefail

ARCHIVE="${1:-}"
if [[ -z "$ARCHIVE" ]]; then
  echo "usage: $0 <archive-filename>" >&2
  exit 1
fi

NAMESPACE="${NAMESPACE:-recognizer}"
CRONJOB="${CRONJOB:-recognizer-archive-importer}"

STEM="${ARCHIVE%.zip}"
STEM="${STEM%.tar.gz}"
STEM="${STEM%.7z}"
STEM="${STEM%.mbox}"
# k8s label values: lowercase alphanumerics + '-' + '_' + '.', start/end
# alphanumeric, max 63 chars. Job names also feed labels via
# batch.kubernetes.io/job-name. Lowercase, replace anything outside
# [a-z0-9-] with '-' (drop '.' entirely since multi-label names are
# error-prone), collapse runs of '-', strip leading/trailing.
STEM_NORM="$(printf '%s' "$STEM" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9-' '-' | sed 's/-\+/-/g; s/^-*//; s/-*$//')"
SUFFIX="$(date +%s)$(openssl rand -hex 2 2>/dev/null || head -c 4 /dev/urandom | xxd -p)"
# Truncate stem so "archive-import-<stem>-<suffix>" fits in 63 chars.
PREFIX="archive-import-"
BUDGET=$(( 63 - ${#PREFIX} - 1 - ${#SUFFIX} ))
if (( ${#STEM_NORM} > BUDGET )); then
  STEM_NORM="${STEM_NORM:0:$BUDGET}"
  STEM_NORM="${STEM_NORM%-}"
fi

kubectl -n "$NAMESPACE" get cronjob "$CRONJOB" -o yaml \
| yq '
    .spec.jobTemplate as $jt
    | $jt
    | .apiVersion = "batch/v1"
    | .kind = "Job"
    | .metadata.name = "archive-import-'"$STEM_NORM"'-'"$SUFFIX"'"
    | .metadata.namespace = "'"$NAMESPACE"'"
    | .spec.template.spec.containers[0].args = ["ingest", "/data/incoming/archives/raw/'"$ARCHIVE"'"]
  ' \
| kubectl apply -f -

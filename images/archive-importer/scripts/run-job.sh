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
SUFFIX="$(date +%s)$(openssl rand -hex 2 2>/dev/null || head -c 4 /dev/urandom | xxd -p)"

kubectl -n "$NAMESPACE" get cronjob "$CRONJOB" -o yaml \
| yq '
    .spec.jobTemplate as $jt
    | $jt
    | .apiVersion = "batch/v1"
    | .kind = "Job"
    | .metadata.name = "archive-import-'"$STEM"'-'"$SUFFIX"'"
    | .metadata.namespace = "'"$NAMESPACE"'"
    | .spec.template.spec.containers[0].args = ["ingest", "/data/incoming/archives/raw/'"$ARCHIVE"'"]
  ' \
| kubectl apply -f -

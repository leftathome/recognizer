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
{{- toYaml . | nindent 0 }}
{{- end }}
{{- end -}}

{{/*
Image reference for in-house images that we publish to the GitLab registry.
Usage: {{ include "recognizer.image" (dict "root" $ "name" "document-scanner" "tag" .Values.documentScanner.image.tag) }}

DO NOT use this helper for opticalRipper -- that workload pulls an upstream
image (automaticrippingmachine/automatic-ripping-machine) directly. See
values.yaml `opticalRipper.image` for the schema; render its image string
inline as `"{{ .Values.opticalRipper.image.repository }}:{{ .Values.opticalRipper.image.tag }}"`.
*/}}
{{- define "recognizer.image" -}}
{{- $tag := .tag | default .root.Chart.AppVersion -}}
{{- printf "%s/%s/%s:%s" .root.Values.image.registry .root.Values.image.repository .name $tag -}}
{{- end -}}

{{/*
Name of the PVC that workloads mount for their shared capture data.
When `storage.backend == existing`, returns the operator-supplied
`storage.existing.claimName`. Otherwise returns the chart-managed
`<release>-data` name (which the chart creates via data-pvc.yaml).
*/}}
{{- define "recognizer.dataClaimName" -}}
{{- if eq .Values.storage.backend "existing" -}}
{{- required "storage.existing.claimName is required when storage.backend=existing" .Values.storage.existing.claimName -}}
{{- else -}}
{{- printf "%s-data" (include "recognizer.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
URL of the in-cluster notification-relay Service.
Default used by archiveImporter when archiveImporter.config.relayUrl is empty.
*/}}
{{- define "recognizer.relayUrl" -}}
{{- printf "http://%s-notification-relay.%s.svc.cluster.local:8080/notify"
      (include "recognizer.fullname" .)
      .Release.Namespace -}}
{{- end -}}

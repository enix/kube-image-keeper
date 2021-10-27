{{/*
Expand the name of the chart.
*/}}
{{- define "cache-registry.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "cache-registry.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "cache-registry.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "cache-registry.labels" -}}
helm.sh/chart: {{ include "cache-registry.chart" . }}
{{ include "cache-registry.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "cache-registry.proxy-labels" -}}
{{ include "cache-registry.labels" . }}
app.kubernetes.io/component: proxy
{{- end }}

{{- define "cache-registry.registry-labels" -}}
{{ include "cache-registry.labels" . }}
app.kubernetes.io/component: registry
{{- end }}

{{- define "cache-registry.registry-ui-labels" -}}
{{ include "cache-registry.labels" . }}
app.kubernetes.io/component: registry-ui
{{- end }}

{{/*
Selector labels
*/}}
{{- define "cache-registry.selectorLabels" -}}
app.kubernetes.io/name: {{ include "cache-registry.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "cache-registry.proxy-selectorLabels" -}}
{{ include "cache-registry.selectorLabels" . }}
app.kubernetes.io/component: proxy
control-plane: controller-manager
{{- end }}

{{- define "cache-registry.registry-selectorLabels" -}}
{{ include "cache-registry.selectorLabels" . }}
app.kubernetes.io/component: registry
{{- end }}

{{- define "cache-registry.registry-ui-selectorLabels" -}}
{{ include "cache-registry.selectorLabels" . }}
app.kubernetes.io/component: registry-ui
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "cache-registry.serviceAccountName" -}}
{{- default (include "cache-registry.fullname" .) .Values.serviceAccount.name }}
{{- end }}

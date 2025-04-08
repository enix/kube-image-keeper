{{/*
Expand the name of the chart.
*/}}
{{- define "kube-image-keeper.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "kube-image-keeper.fullname" -}}
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
{{- define "kube-image-keeper.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "kube-image-keeper.labels" -}}
helm.sh/chart: {{ include "kube-image-keeper.chart" . }}
{{ include "kube-image-keeper.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "kube-image-keeper.controllers-labels" -}}
{{ include "kube-image-keeper.labels" . }}
app.kubernetes.io/component: controllers
{{- end }}

{{- define "kube-image-keeper.proxy-labels" -}}
{{ include "kube-image-keeper.labels" . }}
app.kubernetes.io/component: proxy
{{- end }}

{{- define "kube-image-keeper.registry-labels" -}}
{{ include "kube-image-keeper.labels" . }}
app.kubernetes.io/component: registry
{{- end }}

{{- define "kube-image-keeper.registry-ui-labels" -}}
{{ include "kube-image-keeper.labels" . }}
app.kubernetes.io/component: registry-ui
{{- end }}

{{- define "kube-image-keeper.garbage-collection-labels" -}}
{{ include "kube-image-keeper.labels" . }}
app.kubernetes.io/component: garbage-collection
{{- end }}

{{/*
Selector labels
*/}}
{{- define "kube-image-keeper.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kube-image-keeper.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "kube-image-keeper.controllers-selectorLabels" -}}
{{ include "kube-image-keeper.selectorLabels" . }}
app.kubernetes.io/component: controllers
control-plane: controller-manager
{{- end }}

{{- define "kube-image-keeper.proxy-selectorLabels" -}}
{{ include "kube-image-keeper.selectorLabels" . }}
app.kubernetes.io/component: proxy
control-plane: controller-manager
{{- end }}

{{- define "kube-image-keeper.registry-selectorLabels" -}}
{{ include "kube-image-keeper.selectorLabels" . }}
app.kubernetes.io/component: registry
{{- end }}

{{- define "kube-image-keeper.registry-ui-selectorLabels" -}}
{{ include "kube-image-keeper.selectorLabels" . }}
app.kubernetes.io/component: registry-ui
{{- end }}

{{- define "kube-image-keeper.garbage-collection-selectorLabels" -}}
{{ include "kube-image-keeper.selectorLabels" . }}
app.kubernetes.io/component: garbage-collection
{{- end }}

{{/*
Create the name of the ClusterRole to use
*/}}
{{- define "kube-image-keeper.clusterRoleName" -}}
{{- printf "%s-%s" (include "kube-image-keeper.fullname" .) "controllers" }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "kube-image-keeper.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
  {{- default (include "kube-image-keeper.clusterRoleName" .) .Values.serviceAccount.name }}
{{- else -}}
  {{- default "default" .Values.serviceAccount.name }}
{{- end -}}
{{- end }}

{{/*
Create the name of the registry service account to use
*/}}
{{- define "kube-image-keeper.registry-serviceAccountName" -}}
{{- if .Values.registry.serviceAccount.create -}}
  {{- default (printf "%s-%s" (include "kube-image-keeper.fullname" .) "registry") .Values.registry.serviceAccount.name }}
{{- else -}}
  {{- default "default" .Values.registry.serviceAccount.name }}
{{- end -}}
{{- end }}

{{- define "kube-image-keeper.registry-stateless-mode" -}}
{{- ternary "true" "false" (or .Values.minio.enabled (not (empty .Values.registry.persistence.s3)) (not (empty .Values.registry.persistence.gcs)) (not (empty .Values.registry.persistence.azure))) }}
{{- end }}

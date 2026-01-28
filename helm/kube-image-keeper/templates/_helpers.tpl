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

{{- define "kube-image-keeper.manager-labels" -}}
{{ include "kube-image-keeper.labels" . }}
app.kubernetes.io/component: manager
control-plane: controller-manager
{{- end }}

{{/*
Selector labels
*/}}
{{- define "kube-image-keeper.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kube-image-keeper.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "kube-image-keeper.manager-selectorLabels" -}}
{{ include "kube-image-keeper.selectorLabels" . }}
app.kubernetes.io/component: manager
{{- end }}

{{/*
Create the name of the ClusterRole to use
*/}}
{{- define "kube-image-keeper.clusterRoleName" -}}
{{- printf "%s-%s" (include "kube-image-keeper.fullname" .) "manager" }}
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



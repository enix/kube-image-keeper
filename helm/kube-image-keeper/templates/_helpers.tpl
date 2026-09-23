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

{{/*
Labels of one process. Takes a dict: ctx (the root context) and process (its name, which
is also its component label). kuik runs as three separate processes, see
docs/v3/architecture.md.
*/}}
{{- define "kube-image-keeper.process-labels" -}}
{{ include "kube-image-keeper.labels" .ctx }}
app.kubernetes.io/component: {{ .process }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "kube-image-keeper.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kube-image-keeper.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "kube-image-keeper.process-selectorLabels" -}}
{{ include "kube-image-keeper.selectorLabels" .ctx }}
app.kubernetes.io/component: {{ .process }}
{{- end }}

{{/*
The three processes, as a YAML list: read it with fromYamlArray. name is the subcommand and
the component label, values the key of its block in values.yaml. One list for every template
that renders something per process, so they cannot disagree.
*/}}
{{- define "kube-image-keeper.processes" -}}
- name: webhook
  values: webhook
  servesWebhook: true
  elected: false
- name: reconciler
  values: reconciler
  servesWebhook: false
  elected: true
- name: secret-syncer
  values: secretSyncer
  servesWebhook: false
  elected: true
{{- end }}

{{/*
The ServiceAccount of one process, as YAML: read it with fromYaml. Takes a dict: ctx (the root
context), process (its name) and values (the key of its block). create falls back to the root
when the block leaves it null, annotations and extraLabels are merged over the root ones
(notes/0008), and name defaults to <fullname>-<process>, or "default" when none is created.
*/}}
{{- define "kube-image-keeper.process-serviceAccount" -}}
{{- $root := .ctx.Values.serviceAccount }}
{{- $own := default dict (index .ctx.Values .values).serviceAccount }}
{{- $create := $root.create }}
{{- if kindIs "bool" $own.create }}
{{- $create = $own.create }}
{{- end }}
{{- $name := $own.name }}
{{- if not $name }}
{{- $name = ternary (printf "%s-%s" (include "kube-image-keeper.fullname" .ctx) .process) "default" $create }}
{{- end }}
{{- toYaml (dict
  "create" $create
  "name" $name
  "annotations" (mergeOverwrite (deepCopy (default dict $root.annotations)) (default dict $own.annotations))
  "extraLabels" (mergeOverwrite (deepCopy (default dict $root.extraLabels)) (default dict $own.extraLabels))) }}
{{- end }}

{{/*
The name of the ServiceAccount of one process. Takes the same dict as process-serviceAccount.
*/}}
{{- define "kube-image-keeper.process-serviceAccountName" -}}
{{- (include "kube-image-keeper.process-serviceAccount" . | fromYaml).name }}
{{- end }}

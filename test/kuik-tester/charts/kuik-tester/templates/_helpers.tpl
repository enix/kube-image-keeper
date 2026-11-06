{{/* Release-scoped name: <release>-kuik-tester unless the release already contains the chart name. */}}
{{- define "kuik-tester.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "kuik-tester.labels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "kuik-tester.selectorLabels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "kuik-tester.zotSelectorLabels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}-zot
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/* Fails early on values the binary would refuse at start. */}}
{{- define "kuik-tester.validate" -}}
{{- if and .Values.tls.secretName .Values.ingress.registry.enabled -}}
{{- fail "tls.secretName cannot be combined with ingress.registry.enabled: the binary then serves TLS on :8080 while the Ingress sends plain HTTP; set ingress.registry.enabled=false" -}}
{{- end -}}
{{- with .Values.plainHTTP -}}
{{- if and .enabled .service.nodePort (not (has .service.type (list "NodePort" "LoadBalancer"))) -}}
{{- fail (printf "plainHTTP.service.nodePort needs plainHTTP.service.type NodePort or LoadBalancer, got %s" .service.type) -}}
{{- end -}}
{{- end -}}
{{- if and .Values.networkPolicy.enabled .Values.ingress.admin.enabled (not .Values.networkPolicy.adminFrom) -}}
{{- fail "networkPolicy.adminFrom must allow the Ingress controller when ingress.admin.enabled is true" -}}
{{- end -}}
{{- if not .Values.zot.enabled -}}
{{- range $i, $p := .Values.proxies -}}
{{- if not $p.backend -}}
{{- fail (printf "proxies[%d].backend is required when zot.enabled is false" $i) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/* zot configuration (spec §7); the delays come from the values. */}}
{{- define "kuik-tester.zotConfig" -}}
{"distSpecVersion":"1.1.1",
 "storage":{"rootDirectory":"/var/lib/registry","dedupe":true,"gc":true,"gcDelay":{{ .Values.zot.gcDelay | quote }},"gcInterval":{{ .Values.zot.gcInterval | quote }},
   "retention":{"delay":{{ .Values.zot.retentionDelay | quote }},"policies":[{"repositories":["**"],"deleteReferrers":false,"deleteUntagged":true,"keepTags":[{"patterns":[".*"]}]}]}},
 "http":{"address":"0.0.0.0","port":"5000"},
 "log":{"level":"info"}}
{{- end -}}

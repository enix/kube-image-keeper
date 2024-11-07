# High availability
# =================
{{ if and
  (eq (include "kube-image-keeper.registry-stateless-mode" .) "false")
  (gt (int .Values.registry.replicas) 1)
  (ne .Values.registry.persistence.accessModes "ReadWriteMany")
}}
  {{ fail "Registry needs a configured S3/GCS/Azure storage driver or a PVC which supports ReadWriteMany to enable HA mode (>1 replicas), please enable minio, configure an external storage driver or provide a supported PVC." }}
{{ end }}

# Multiple storage
# ================
{{ $enabledOptions := list }}

{{ if .Values.registry.persistence.enabled }}
  {{ $enabledOptions = append $enabledOptions "registry.persistence.enabled" }}
{{ end }}
{{ if .Values.minio.enabled }}
  {{ $enabledOptions = append $enabledOptions "minio.enabled" }}
{{ end }}
{{ if .Values.registry.persistence.s3 }}
  {{ $enabledOptions = append $enabledOptions "registry.persistence.s3" }}
{{ end }}
{{ if .Values.registry.persistence.gcs }}
  {{ $enabledOptions = append $enabledOptions "registry.persistence.gcs" }}
{{ end }}
{{ if .Values.registry.persistence.azure }}
  {{ $enabledOptions = append $enabledOptions "registry.persistence.azure" }}
{{ end }}

{{ if gt (len $enabledOptions) 1 }}
  {{ fail (printf "Multiple storage options enabled: %v. Please enable only one." $enabledOptions) }}
{{ end }}

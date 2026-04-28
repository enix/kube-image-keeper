# Operator configuration

The kuik manager reads a YAML configuration file at startup to tune routing, monitoring, and metrics behaviors. This page lists every supported field, its type, default value, and effect.

CRDs (`ReplicatedImageSet`, `ImageSetMirror`, `ClusterImageSetAvailability`, ...) are documented separately in [`crds.md`](./crds.md). This page only covers the operator-wide configuration file.

## File location and loading

- Default path: `/etc/kube-image-keeper/config.yaml`
- Override with the `--config` flag on the manager binary (see `cmd/main.go`).
- Loaded with [koanf](https://github.com/knadh/koanf): YAML parsed into `internal/config/config.go`'s `Config` struct.
- The file is optional. When it is missing, the operator boots on its built-in defaults (`config.LoadDefault()` / `defaultConfig`).

### Precedence

1. Built-in defaults defined in `internal/config/config.go`.
2. Values from the YAML file (when present) are merged on top of (1).

The Helm chart does not ship a default `configuration:` block. Any field set under `configuration:` in `values.yaml` (or via `--set`) is rendered into a ConfigMap mounted at `/etc/kube-image-keeper/config.yaml`. Leaving `configuration` empty makes the chart skip the ConfigMap entirely so the operator runs purely on its defaults.

### Example (full)

The following file shows every supported key with its default value. You only need to set the keys you want to override; everything else falls back to the defaults below.

```yaml
routing:
  activeCheck:
    timeout: 1s
    staleMirrorCleanup:
      maxConcurrent: 10
      timeout: 5s
  rewriteOnNeverImagePullPolicy: false
  honorPrioritiesOnAlwaysImagePullPolicy: false

monitoring:
  registries:
    default:
      method: HEAD
      interval: 3h
      maxPerInterval: 25
      # timeout: 0          (no per-check timeout)
      # fallbackCredentialSecret:
      #   namespace: ...
      #   name: ...
    items:
      docker.io:
        interval: 1h
        maxPerInterval: 6

metrics:
  imageLastMonitorAgeMinutes:
    bucketFactor: 1.1
    zeroThreshold: 1.0
    maxBucketNumber: 20
    legacy:
      bucketType: exponential
      count: 12
      start: 1
      factor: 1.94
      # min, max, custom: see "legacy" section below
```

## `routing`

Controls the mutating webhook that rewrites Pod container images.

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `routing.activeCheck.timeout` | duration | `1s` | Per-image upper bound on the availability HTTP probe (HEAD request) made by the webhook before falling back to the next alternative. |
| `routing.activeCheck.staleMirrorCleanup.maxConcurrent` | int | `10` | Maximum number of concurrent goroutines clearing stale mirror status entries. The cleanup is dropped (not retried inline) if the semaphore is full; the next availability check that returns `NotFound` will trigger it again. |
| `routing.activeCheck.staleMirrorCleanup.timeout` | duration | `5s` | Per-cleanup deadline for the goroutine that clears a stale mirror status entry. |
| `routing.rewriteOnNeverImagePullPolicy` | bool | `false` | When `false`, containers with `imagePullPolicy: Never` are left untouched (the cluster-local image is assumed authoritative). Set to `true` to rewrite them as well. |
| `routing.honorPrioritiesOnAlwaysImagePullPolicy` | bool | `false` | When `false`, containers with `imagePullPolicy: Always` always keep the original image first regardless of CR priorities (mirrors and upstreams remain available as fallbacks). Set to `true` to opt these containers into the regular priority sort. See [#561](https://github.com/enix/kube-image-keeper/issues/561). |

Durations use Go's `time.ParseDuration` syntax (`"500ms"`, `"30s"`, `"2h"`, ...).

### Example

Lower the active-check timeout, keep `Always` containers in the standard
priority sort:

```yaml
routing:
  activeCheck:
    timeout: 500ms
  honorPrioritiesOnAlwaysImagePullPolicy: true
```

## `monitoring`

Controls the rate at which `ClusterImageSetAvailability` checks reach upstream registries. See also the [ClusterImageSetAvailability operator-configuration block](./crds.md#operator-configuration) for how these values interact with the CRD.

`monitoring.registries.default` applies to every registry that has no explicit entry in `monitoring.registries.items`. Each `items.<host>` entry overrides the matching field from `default` for that host only; unset fields inherit from `default`.

### Per-registry fields

| Field | Type | Default (from `default`) | Description |
| --- | --- | --- | --- |
| `method` | string | `HEAD` | HTTP method for the availability probe. `HEAD` or `GET`. |
| `interval` | duration | `3h` | Time window over which `maxPerInterval` checks are spread for that registry. |
| `maxPerInterval` | int | `25` | Maximum number of image checks per `interval` for the registry. |
| `timeout` | duration | `0` (no timeout) | Deadline per individual check. |
| `fallbackCredentialSecret` | object | unset | Reference (`name`, `namespace`) to a `kubernetes.io/dockerconfigjson` Secret used when no Pod-level pull secret is available for the image. |

### Default for `monitoring.registries.items`

The operator ships a single override out of the box, applied to Docker Hub to stay below its tighter rate limits:

```yaml
monitoring:
  registries:
    items:
      docker.io:
        interval: 1h
        maxPerInterval: 6
```

If you set `monitoring.registries.items` in your own configuration, you replace this default map (koanf merges keys, so add `docker.io` back if you still want it).

### Example

Add a credential fallback for an internal registry and tighten its rate:

```yaml
monitoring:
  registries:
    items:
      registry.internal.example.com:
        method: HEAD
        interval: 30m
        maxPerInterval: 60
        timeout: 5s
        fallbackCredentialSecret:
          name: internal-registry-creds
          namespace: kuik-system
```

## `metrics`

Tunes the histograms exposed by the manager's metrics endpoint (`/metrics` on `:8080`). Currently only the `kuik_monitoring_image_last_monitor_age_minutes` histogram is configurable.

### `metrics.imageLastMonitorAgeMinutes`

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `bucketFactor` | float | `1.1` | Native histogram bucket factor (`NativeHistogramBucketFactor`). |
| `zeroThreshold` | float | `1.0` | Native histogram zero threshold (`NativeHistogramZeroThreshold`). |
| `maxBucketNumber` | uint32 | `20` | Maximum number of native histogram buckets. |
| `legacy` | object | see below | Configuration of the classic (non-native) bucket boundaries. Required for Prometheus deployments that do not yet ingest native histograms. |

#### `metrics.imageLastMonitorAgeMinutes.legacy`

`bucketType` selects which other fields are read.

| Field | Type | Default | Used when `bucketType` is | Description |
| --- | --- | --- | --- | --- |
| `bucketType` | string | `exponential` | always | One of `exponential`, `exponentialRange`, `custom`, `disabled`. |
| `start` | float | `1` | `exponential` | First bucket boundary. Must be `> 0`. |
| `factor` | float | `1.94` | `exponential` | Multiplier between consecutive buckets. Must be `> 1`. |
| `count` | int | `12` | `exponential`, `exponentialRange` | Number of buckets. Must be `>= 1`. |
| `min` | float | `1` | `exponentialRange` | Lower bound of the bucket range. Must be `> 0`. |
| `max` | float | `1440` | `exponentialRange` | Upper bound of the bucket range. Must be `> min`. |
| `custom` | []float | `[1, 5, 10, 30, 60, 120, 180, 360, 720, 1440]` | `custom` | Explicit, strictly ascending list of bucket upper bounds (in minutes). |

`bucketType: disabled` turns off the legacy buckets entirely (the native
histogram is still emitted).

### Example

Use a custom set of legacy buckets tailored to a 24-hour reporting window:

```yaml
metrics:
  imageLastMonitorAgeMinutes:
    legacy:
      bucketType: custom
      custom: [1, 15, 60, 240, 720, 1440]
```

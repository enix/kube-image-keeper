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
skipLabels: []
skipAnnotations: []

routing:
  activeCheck:
    timeout: 1s
    staleMirrorCleanup:
      maxConcurrent: 10
      timeout: 5s
  rewriteOnNeverImagePullPolicy: false
  honorPrioritiesOnAlwaysImagePullPolicy: false

mirroring:
  platforms:
    - architecture: amd64

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

## `skipLabels` / `skipAnnotations`

Cluster-wide pod skip lists. Pods whose labels or annotations match any entry are ignored by the mutating webhook (no image rewrite) and by all reconcilers (no mirroring, no availability tracking). The check runs before any CR is consulted and takes precedence over per-CR `podFilter` rules.

| Field | Type | Default |
| --- | --- | --- |
| `skipLabels` | []string | `[]` |
| `skipAnnotations` | []string | `[]` |

Both fields are skip-only (no include counterpart). The selector syntax is the same as `spec.podFilter` on individual CRDs — see [Pod filtering](./resource-filtering.md#pod-filtering-podfilter) in the resource-filtering guide for full syntax reference. A typo causes the operator to fail at startup (fail-fast).

### Migrating from KuiK v1

v1 honored `kube-image-keeper.enix.io/image-caching-policy: ignore` to opt a pod out of mirroring entirely. To get the same behavior on v2:

```yaml
skipLabels:
  - kube-image-keeper.enix.io/image-caching-policy=ignore
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

## `mirroring`

Controls how `ImageSetMirror` and `ClusterImageSetMirror` reconcilers copy images to their destination registries.

### `mirroring.platforms`

List of platform manifests to keep when copying multi-arch images. Single-arch source images are copied as long as they satisfy at least one entry; multi-arch indexes are filtered to only include matching manifests. Configured platforms that are not present in the source image are logged as a warning and skipped, so only the intersection of configured and available platforms is mirrored. The copy fails only when the source image has none of the configured platforms.

Defaults to `[{architecture: amd64}]` (single entry, OS and variant unset).

> [!NOTE]
> Setting `mirroring.platforms` **replaces** the default, koanf does not deep-merge slices. If you want to keep `amd64` while adding more platforms, list it explicitly.

Each entry supports the following fields:

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `os` | string | `""` (any) | Operating system (`linux`, `windows`, ...). Empty matches any. |
| `architecture` | string | (required) | CPU architecture (`amd64`, `arm64`, `arm`, ...). |
| `variant` | string | `""` (any) | Architecture variant (`v8`, `v7`, ...). Empty matches any. |

The list must contain at least one entry with a non-empty `architecture`; the operator refuses to start otherwise.

> [!WARNING]
> Changing this list after images have been mirrored does not re-mirror or delete previously copied manifests; the new platform set only applies to subsequent mirror operations.

### Example

Mirror Linux `amd64` and `arm64/v8`:

```yaml
mirroring:
  platforms:
    - os: linux
      architecture: amd64
    - os: linux
      architecture: arm64
      variant: v8
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

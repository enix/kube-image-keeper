---
title: Custom Resource Definitions
---

This document describes the available Custom Resource Definitions (CRDs). Examples provided are non-exhaustive; for a full list of fields, please refer to the `kubectl describe <resource-name>` command.

For filtering (scoping resources to specific images, namespaces, or pods via the unified `spec.filter` field) see [Resource filtering](/resource-filtering/).

Resource Types:

* [ReplicatedImageSet](#clusterreplicatedimageset)
* [ClusterReplicatedImageSet](#clusterreplicatedimageset)
* [ImageSetMirror](#clusterimagesetmirror)
* [ClusterImageSetMirror](#clusterimagesetmirror)
* [ClusterImageSetAvailability](#clusterimagesetavailability)

## (Cluster)ReplicatedImageSet

The `ReplicatedImageSet` and `ClusterReplicatedImageSet` resources declare equivalence between image patterns across different registries. By mapping multiple upstream locations to a single logical "ImageSet", you ensure the cluster treats these varied sources as the same entity.

This is particularly useful for multi-homed projects (e.g., Thanos, Prometheus, Kubernetes components) where the same binary is published to multiple registries.

### Fields

| Field | Required | Description |
| --- | --- | --- |
| `spec.priority` | | Controls ordering of alternatives relative to the original image and other CRs. Negative values place alternatives before the original image; positive values place them after. Default is `0` (original image first). |
| `spec.filter` | | Selects which pods and namespaces (cluster-scoped only) this resource applies to. The `image` dimension is **not** supported here and an `image` item is **rejected at admission**: image selection is done per-upstream via `spec.upstreams[].imageFilter`. See [Resource filtering](/resource-filtering/). |
| `spec.upstreams[]` | | List of upstream image sources that should be considered equivalent. |
| `spec.upstreams[].registry` | ✅ | Registry where the upstream image is hosted (e.g. `docker.io`, `quay.io`). |
| `spec.upstreams[].path` | ✅ | Path identifying the image in the registry (e.g. `/thanosio/thanos`). |
| `spec.upstreams[].priority` | | Controls ordering of this upstream relative to other upstreams with the same parent priority. `0` means no specific ordering (YAML declaration order is preserved). Positive values are sorted ascending: lower value = higher priority. |
| `spec.upstreams[].imageFilter` | | Rules used to select which images from this upstream are considered replicated. This per-upstream filter is distinct from the deprecated top-level `imageFilter` and is unaffected by its deprecation. See [Per-upstream image filtering](/resource-filtering/#per-upstream-image-filtering-on-clusterreplicatedimageset). |
| `spec.upstreams[].discardAlternative` | | When `true`, keeps the upstream in the configuration but excludes it from image routing. The upstream still participates in image matching, so other upstreams in the same CR continue to work. Useful when a registry no longer exists, to avoid waiting for the check timeout. |
| `spec.upstreams[].credentialSecret` | | Reference to a Secret used to pull matching images from this upstream. |
| `spec.upstreams[].credentialSecret.name` | | Name of the Secret. |
| `spec.upstreams[].credentialSecret.namespace` | | Namespace of the Secret. Ignored for namespaced `ReplicatedImageSet` (uses the parent namespace instead). |

### Example

In the following example, the `ClusterReplicatedImageSet` declares that the images hosted on Quay and Docker Hub are identical. This allows the system to resolve them as the same ImageSet regardless of the source registry used in a Pod spec and fallback to one or another depending on their availability.

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterReplicatedImageSet
metadata:
  name: thanos
spec:
  upstreams:
  - registry: docker.io
    imageFilter:
      include:
      - /thanosio/thanos:.+
    path: /thanosio/thanos
  - registry: quay.io
    imageFilter:
      include:
      - /thanos/thanos:.+
    path: /thanos/thanos/
```

`ReplicatedImageSet` are the same, but scoped to a namespace. In the following example, we have three different upstreams for the same image and we use priority to order alternatives. So if our original image is using Quay and the registry is unreachable, kuik will consider both GHCR and DockerHub as alternative, but try using the one from GHCR first and only fallback to DockerHub if it's unreachable too:

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ReplicatedImageSet
metadata:
  name: x509-certificate-exporter
  namespace: monitoring
spec:
  upstreams:
  - registry: quay.io
    imageFilter:
      include:
      - /enix/x509-certificate-exporter:.+
    path: /enix/
    priority: 1
  - registry: ghcr.io
    imageFilter:
      include:
      - /enix/x509-certificate-exporter:.+
    path: /enix/
    priority: 2
  - registry: docker.io
    imageFilter:
      include:
      - /enix/x509-certificate-exporter:.+
    path: /enix/
    priority: 3
```

## (Cluster)ImageSetMirror

The `ImageSetMirror` and `ClusterImageSetMirror` resources define the actual mirroring implementation for your cluster. They determine which images are selected for synchronization, specify the target destination, and manage the authentication via push secrets.

### Fields

| Field | Required | Description |
| --- | --- | --- |
| `spec.priority` | | Controls ordering of alternatives relative to the original image and other CRs. Negative values place alternatives before the original image; positive values place them after. Default is `0` (original image first). |
| `spec.filter` | | Selects which pods, namespaces (cluster-scoped only) and images this resource applies to. See [Resource filtering](/resource-filtering/). |
| `spec.imageFilter` | | **Deprecated** (superseded by `spec.filter`, with which it is mutually exclusive). Rules used to select which images are eligible for mirroring. See [Migration](/resource-filtering/#migration-from-imagefilter--namespacefilter--podfilter). |
| `spec.cleanup` | | Cleanup strategy for mirrored images. |
| `spec.cleanup.enabled` | | Whether automatic cleanup of unused mirrored images is enabled. Default is `false`. |
| `spec.cleanup.retention` | | Duration to retain unused mirrored images before cleanup (e.g. `720h`). |
| `spec.mirrors[]` | | List of mirror destinations. |
| `spec.mirrors[].registry` | | Target registry where images will be mirrored (e.g. `registry.example.com`). |
| `spec.mirrors[].path` | | Path prefix on the target registry (e.g. `/mirror`). |
| `spec.mirrors[].priority` | | Controls ordering of this mirror relative to other mirrors with the same parent priority. `0` means no specific ordering (YAML declaration order is preserved). Positive values are sorted ascending: lower value = higher priority. |
| `spec.mirrors[].credentialSecret` | | Reference to a Secret used to push images to this mirror. |
| `spec.mirrors[].credentialSecret.name` | | Name of the Secret. |
| `spec.mirrors[].credentialSecret.namespace` | | Namespace of the Secret. Ignored for namespaced `ImageSetMirror` (uses the parent namespace instead). |
| `spec.mirrors[].cleanup` | | Per-mirror cleanup strategy override. Same fields as `spec.cleanup`. |

### Example

In this example, the `ClusterImageSetMirror` ensures that all images are mirrored to a private registry. It uses a specific `credentialSecret` to authenticate against the destination.

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageSetMirror
metadata:
  name: global-mirror
spec:
  filter:
    include:
    - image: .*
  mirrors:
  - registry: registry.example.com
    path: /mirror
    credentialSecret:
      name: registry-secret
      namespace: kuik-system
  cleanup:
    enabled: true
    retention: 24h
```

The registry secret must be a `docker-registry` type secret. You could create it with:

```bash
kubectl -n kuik-system create secret docker-registry registry-secret --docker-server=registry.example.com --docker-username=username --docker-password=password
```

When cleanup is enabled, kuik only delete mirror image reference once an image is no longer running in the cluster since more than `retention` time (useful to deal with image used by CronJobs). You still have to configure garbage collection on your registry to actually reclaim space.

If an image is rewritten to use our mirror, kuik will copy the secret to the pod's namespace and add it to pod `imagePullSecrets`.

## ClusterImageSetAvailability

The `ClusterImageSetAvailability` resource continuously monitors the upstream availability of container images used in the cluster. It automatically discovers images from running Pods, checks whether they are still reachable on their source registry, and reports their status.

This is useful for detecting images that have been deleted, made private, or are hosted on unreachable registries before they cause issues during a Pod reschedule.

### Fields

| Field | Required | Description |
| --- | --- | --- |
| `spec.unusedImageExpiry` | | How long to keep tracking an image after no Pod uses it. Once elapsed the image is removed from status (e.g. `720h`). Zero means unused images are never removed. |
| `spec.filter` | | Selects which pods, namespaces and images to monitor. See [Resource filtering](/resource-filtering/). |
| `spec.imageFilter` | | **Deprecated** (superseded by `spec.filter`, with which it is mutually exclusive). Rules used to select which images to monitor. See [Migration](/resource-filtering/#migration-from-imagefilter--namespacefilter--podfilter). |

### How it works

1. The controller watches all Pods in the cluster and collects their container image references.
2. Images matching the `filter` are added to `.status.images` with status `Scheduled`.
3. A rate-limited checker performs availability checks against each image's source registry (one image per registry per tick, configurable via `monitoring.registries` in the operator configuration file).
4. When a Pod is deleted and no other Pod uses the same image, `unusedSince` is set. After `unusedImageExpiry`, the image is removed from tracking.

### Example

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageSetAvailability
metadata:
  name: monitor-critical-images
spec:
  unusedImageExpiry: 720h
  filter:
    include:
      - image: ".+/nginx:.+"
      - image: ".+/redis:.+"
```

### Operator configuration

The check rate and method are controlled per-registry in the operator's `config.yaml`, not in the CRD. See the full [operator configuration reference](/configuration/) for the list of all supported fields, their defaults, and the precedence rules.

```yaml
monitoring:
  registries:
    default:
      method: HEAD
      interval: 3h
      maxPerInterval: 25
      timeout: 10s
    items:
      docker.io:
        interval: 1h
        maxPerInterval: 6
        fallbackCredentialSecret:
          name: dockerhub-creds
          namespace: kuik-system
```

| Field | Default | Description |
| --- | --- | --- |
| `method` | `HEAD` | HTTP method used for the availability check (`HEAD` or `GET`). |
| `interval` | `3h` | Time window over which `maxPerInterval` checks are spread. |
| `maxPerInterval` | `25` | Maximum number of image checks per `interval` for a given registry. |
| `timeout` | `0` (none) | Timeout for each individual check. |
| `fallbackCredentialSecret` | none | Secret to use when Pod pull secrets are unavailable. |

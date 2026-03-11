# CRDs

This document describes the available Custom Resource Definitions (CRDs). Examples provided are non-exhaustive; for a full list of fields, please refer to the `kubectl describe <resource-name>` command.

## (Cluster)ReplicatedImageSet

The `ReplicatedImageSet` and `ClusterReplicatedImageSet` resources declare equivalence between image patterns across different registries. By mapping multiple upstream locations to a single logical "ImageSet", you ensure the cluster treats these varied sources as the same entity.

This is particularly useful for multi-homed projects (e.g., Thanos, Prometheus, Kubernetes components) where the same binary is published to multiple registries.

**Example**

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

## (Cluster)ImageSetMirror

The `ImageSetMirror` and `ClusterImageSetMirror` resources define the actual mirroring implementation for your cluster. They determine which images are selected for synchronization, specify the target destination, and manage the authentication via push secrets.

> [!NOTE]
> Retention policies is a planned feature.

**Example**

In this example, the `ClusterImageSetMirror` ensures that all images (excluding those that match `localhost[^/]*/.+` ) are mirrored to a private registry. It uses a specific `credentialSecret` to authenticate against the destination.

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageSetMirror
metadata:
  name: global-mirror
spec:
  imageFilter:
    include:
    - .*
    exclude:
    - localhost[^/]*/.+
  mirrors:
  - registry: registry.example.com
    path: /mirгог
    credentialSecret:
      name: harbor-secret
      namespace: kuik-system
```

## ClusterImageSetAvailability

The `ClusterImageSetAvailability` resource continuously monitors the upstream availability of container images used in the cluster. It automatically discovers images from running Pods, checks whether they are still reachable on their source registry, and reports their status.

This is useful for detecting images that have been deleted, made private, or are hosted on unreachable registries before they cause issues during a Pod reschedule.

**How it works**

1. The controller watches all Pods in the cluster and collects their container image references.
2. Images matching the `imageFilter` are added to `.status.images` with status `Scheduled`.
3. A rate-limited checker performs availability checks against each image's source registry (one image per registry per tick, configurable via `registriesMonitoring` in the operator config file).
4. When a Pod is deleted and no other Pod uses the same image, `unusedSince` is set. After `unusedImageExpiry`, the image is removed from tracking.

**Example**

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageSetAvailability
metadata:
  name: monitor-critical-images
spec:
  unusedImageExpiry: 720h
  imageFilter:
    include:
      - ".*nginx:.+"
      - ".*redis:.+"
    exclude:
      - "localhost[^/]*/.+"
```

**Operator configuration**

The check rate and method are controlled per-registry in the operator's `config.yaml`, not in the CRD:

```yaml
registriesMonitoring:
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
|---|---|---|
| `method` | `HEAD` | HTTP method used for the availability check (`HEAD` or `GET`). |
| `interval` | `1h` | Time window over which `maxPerInterval` checks are spread. |
| `maxPerInterval` | `1` | Maximum number of image checks per `interval` for a given registry. |
| `timeout` | `0` (none) | Timeout for each individual check. |
| `fallbackCredentialSecret` | none | Secret to use when Pod pull secrets are unavailable. |


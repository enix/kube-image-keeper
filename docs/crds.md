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

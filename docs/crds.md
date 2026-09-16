# kuik.enix.io/v1alpha1

This document describes the available Custom Resource Definitions (CRDs). Examples provided are non-exhaustive; for a full list of fields, please refer to the `kubectl describe <resource-name>` command.

Resource Types:

* [ReplicatedImageSet](#clusterreplicatedimageset)
* [ClusterReplicatedImageSet](#clusterreplicatedimageset)
* [ImageSetMirror](#clusterimagesetmirror)
* [ClusterImageSetMirror](#clusterimagesetmirror)
* [ClusterImageSetAvailability](#clusterimagesetavailability)

## (Cluster)ReplicatedImageSet

The `ReplicatedImageSet` and `ClusterReplicatedImageSet` resources declare equivalence between image patterns across different registries. By mapping multiple upstream locations to a single logical "ImageSet", you ensure the cluster treats these varied sources as the same entity.

This is particularly useful for multi-homed projects (e.g., Thanos, Prometheus, Kubernetes components) where the same binary is published to multiple registries.

Image path is always [normalized](https://github.com/distribution/reference/blob/main/normalize.go) by kuik, so use full path in `imageFilter`. For instance `busybox:stable` will be seen as `docker.io/library/busybox:stable`.

### Fields

| Field | Required | Description |
| --- | --- | --- |
| `spec.priority` | | Controls ordering of alternatives relative to the original image and other CRs. Negative values place alternatives before the original image; positive values place them after. Default is `0` (original image first). |
| `spec.upstreams[]` | | List of upstream image sources that should be considered equivalent. |
| `spec.upstreams[].registry` | ✅ | Registry where the upstream image is hosted (e.g. `docker.io`, `quay.io`). |
| `spec.upstreams[].path` | ✅ | Path identifying the image in the registry (e.g. `/thanosio/thanos`). |
| `spec.upstreams[].priority` | | Controls ordering of this upstream relative to other upstreams with the same parent priority. `0` means no specific ordering (YAML declaration order is preserved). Positive values are sorted ascending: lower value = higher priority. |
| `spec.upstreams[].imageFilter` | | Rules used to select which images from this upstream are considered replicated. |
| `spec.upstreams[].imageFilter.include` | | List of regex patterns. Images matching at least one pattern are included. |
| `spec.upstreams[].imageFilter.exclude` | | List of regex patterns. Images matching any pattern are excluded (takes precedence over include). |
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

The `ImageSetMirror` and `ClusterImageSetMirror` resources define the actual mirroring implementation for your cluster. They determine which images are selected for synchronization, specify the target destination, and optionally select credentials for registry operations.

Image path is always [normalized](https://github.com/distribution/reference/blob/main/normalize.go) by kuik, so use full path in `imageFilter`. For instance `busybox:stable` will be seen as `docker.io/library/busybox:stable`.

### Fields

| Field | Required | Description |
| --- | --- | --- |
| `spec.priority` | | Controls ordering of alternatives relative to the original image and other CRs. Negative values place alternatives before the original image; positive values place them after. Default is `0` (original image first). |
| `spec.imageFilter` | | Rules used to select which images are eligible for mirroring. |
| `spec.imageFilter.include` | | List of regex patterns. Images matching at least one pattern are included. |
| `spec.imageFilter.exclude` | | List of regex patterns. Images matching any pattern are excluded (takes precedence over include). |
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

In this example, the `ClusterImageSetMirror` ensures that all images are mirrored to a private registry. It uses a specific `credentialSecret` to authenticate against the destination; this reference is optional when the manager uses Docker or cloud workload identity instead.

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageSetMirror
metadata:
  name: global-mirror
spec:
  imageFilter:
    include:
    - .*
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

The registry Secret must contain Docker registry credentials (`kubernetes.io/dockerconfigjson`); the `docker-registry` kubectl command creates this type. You could create it with:

```bash
kubectl -n kuik-system create secret docker-registry registry-secret --docker-server=registry.example.com --docker-username=username --docker-password=password
```

### Registry authentication

Kuik applies the same authentication policy to source reads, availability checks, mirror pushes, destination checks after an uncertain copy, and mirror cleanup:

1. Matching credentials from Kubernetes Docker-config Secrets supplied for the operation. These can come from Pod `imagePullSecrets`, a CRD `credentialSecret`, or `monitoring.registries.items.<registry>.fallbackCredentialSecret`.
2. Credentials from the manager Pod's Docker configuration.
3. Google credentials for Google Artifact Registry or GCR.
4. AWS credentials for ECR.
5. Azure credentials for ACR.
6. Anonymous access, for public or authentication-disabled registries.

All matching credentials from Kubernetes Secrets remain available in their existing lookup order, which supports credential rotation. If matching Secret credentials are present but the registry rejects them, kuik returns the registry error after those matching credentials are exhausted; it does not fall through to Docker configuration, cloud workload identity, or anonymous access. Empty or unrelated Secret data does not count as a match and follows the normal fallback path. Malformed supported Secret data remains an error.

#### Workload identity setup

Cloud authentication uses the identity of the kuik manager Pod. Bind the manager's Kubernetes ServiceAccount to a cloud identity and grant that cloud identity access to the required registry repositories. The operator owns the cloud-side IAM, trust, federation, and Pod-identity configuration:

| Platform | Manager identity binding |
| --- | --- |
| GKE Workload Identity | Bind the manager Kubernetes ServiceAccount to a Google Service Account and grant that Google Service Account access to Artifact Registry or GCR. |
| EKS with IRSA | Annotate the manager Kubernetes ServiceAccount with the IAM role ARN and configure the role trust policy for the cluster's OIDC provider. |
| EKS Pod Identity | Create an EKS Pod Identity association from the manager namespace/ServiceAccount to an IAM role. |
| Azure Workload Identity | Configure the federated identity credential, annotate the manager Kubernetes ServiceAccount with the client ID, and satisfy the Azure Workload Identity webhook's Pod label/injection requirements. |

The Helm chart selects the manager ServiceAccount through `serviceAccount.name`, can create it with `serviceAccount.create`, and passes through `serviceAccount.annotations`. The chart does not create cloud IAM roles, trust policies, workload-identity associations, or registry permissions. Cloud authentication does not require copying cloud credentials into Kubernetes Secrets or adding a cluster-wide Secret grant. Kuik still needs its existing Kubernetes RBAC for reading Pod `imagePullSecrets` and explicitly referenced `credentialSecret` objects.

#### Registry permissions

Grant only the permissions required by the images and features enabled in your deployment:

| Operation | Used for | Required access |
| --- | --- | --- |
| Pull/read | Source image pulls, availability checks, and destination verification | Registry manifest and layer read access. |
| Push/write | Copying images to `spec.mirrors[]` destinations | Registry upload and manifest-write access on mirror repositories. |
| Delete | Cleanup of unused mirror images and finalizer-driven cleanup | Registry image-delete access on mirror repositories only; omit it when cleanup is disabled. |

For Google Artifact Registry, the Writer role covers repository reads and uploads; deletion requires an additional repository-level permission, such as a narrowly scoped custom role or the repository administration role where that broader permission set is acceptable. For ECR, grant the equivalent repository-scoped read, upload, and, only when needed, `ecr:BatchDeleteImage` actions. Use the corresponding pull, push, and delete permissions for ACR or other registries. Do not grant delete access to source repositories when kuik only mirrors from them.

The manager identity is used for registry operations performed by kuik; the workload Pod still needs permission to pull the final image reference. If kuik rewrites an image to a private mirror, configure the resulting Pod's `imagePullSecrets` or node/workload identity separately.

When cleanup is enabled, kuik only delete mirror image reference once an image is no longer running in the cluster since more than `retention` time (useful to deal with image used by CronJobs). You still have to configure garbage collection on your registry to actually reclaim space.

If an image is rewritten to use our mirror, kuik will copy the secret to the pod's namespace and add it to pod `imagePullSecrets`.

## ClusterImageSetAvailability

The `ClusterImageSetAvailability` resource continuously monitors the upstream availability of container images used in the cluster. It automatically discovers images from running Pods, checks whether they are still reachable on their source registry, and reports their status.

This is useful for detecting images that have been deleted, made private, or are hosted on unreachable registries before they cause issues during a Pod reschedule.

### How it works

1. The controller watches all Pods in the cluster and collects their container image references.
2. Images matching the `imageFilter` are added to `.status.images` with status `Scheduled`.
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
  imageFilter:
    include:
      - ".+/nginx:.+"
      - ".+/redis:.+"
```

### Operator configuration

The check rate and method are controlled per-registry in the operator's `config.yaml`, not in the CRD. See the full [operator configuration reference](./configuration.md) for the list of all supported fields, their defaults, and the precedence rules.

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

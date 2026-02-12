# Image Routing

When a Pod is created, kube-image-keeper's mutating webhook evaluates every container image against the declared `(Cluster)ImageSetMirror` and `(Cluster)ReplicatedImageSet` resources. It builds an ordered list of alternative images and rewrites the container to use the first available one.

## Default ordering

Without any explicit priority, alternatives are ordered as follows:

1. Original image
2. `ClusterImageSetMirror` mirrors
3. `ImageSetMirror` mirrors
4. `ClusterReplicatedImageSet` upstreams
5. `ReplicatedImageSet` upstreams

Within each resource, mirrors or upstreams are listed in their YAML declaration order. The webhook performs a `HEAD` on each alternative image manifest (in order) and uses the first one that is available.

## Priority system

A two-level priority system allows fine-grained control over this ordering. It works like the Linux `nice` value: lower values mean higher priority.

### Level 1: CR priority (`spec.priority`)

Every `(Cluster)ImageSetMirror` and `(Cluster)ReplicatedImageSet` accepts a signed integer `spec.priority` field (default `0`).

| Value | Behavior |
|---|---|
| Negative | Alternatives from this CR are placed **before** the original image, effectively overriding it when available. |
| `0` (default) | The original image is tried first; alternatives from this CR serve as fallback. |
| Positive | Alternatives from this CR are tried after the original, but with lower priority than CRs with `priority: 0`. |

When multiple CRs share the same priority, the default type ordering applies (ClusterImageSetMirror > ImageSetMirror > ClusterReplicatedImageSet > ReplicatedImageSet).

**Example**: always prefer a private mirror over the original image:

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageSetMirror
metadata:
  name: private-mirror
spec:
  priority: -1
  imageFilter:
    include:
    - .*
  mirrors:
  - registry: registry.example.com
    path: /mirror
    credentialSecret:
      name: registry-secret
      namespace: kuik-system
```

With `priority: -1`, the mirrored image is checked **before** the original. If it is available, the pod is rewritten to use it.

### Level 2: intra-CR priority (`mirrors[].priority` / `upstreams[].priority`)

Each mirror or upstream entry accepts an unsigned integer `priority` field (default `0`) that controls ordering **within the same CR**.

| Value | Behavior |
|---|---|
| `0` (default) | Default position; YAML declaration order is preserved among items at priority `0`. |
| Positive | Sorted ascending: lower value = higher priority. |

**Example**: prefer Quay over ECR over Docker Hub:

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterReplicatedImageSet
metadata:
  name: nginx-unprivileged
spec:
  upstreams:
  - registry: docker.io
    priority: 30
    imageFilter:
      include:
      - /nginxinc/nginx-unprivileged:.+
    path: /nginxinc/nginx-unprivileged
  - registry: quay.io
    priority: 10
    imageFilter:
      include:
      - /nginx/nginx-unprivileged:.+
    path: /nginx/nginx-unprivileged
  - registry: public.ecr.aws
    priority: 20
    imageFilter:
      include:
      - /nginx/nginx-unprivileged:.+
    path: /nginx/nginx-unprivileged
```

For a pod requesting `docker.io/nginxinc/nginx-unprivileged:1.29`, this produces the following alternative order:

1. `docker.io/nginxinc/nginx-unprivileged:1.29` (original image, at CR priority `0`)
2. `quay.io/nginx/nginx-unprivileged:1.29` (intra-priority `10`)
3. `public.ecr.aws/nginx/nginx-unprivileged:1.29` (intra-priority `20`)
4. `docker.io/nginxinc/nginx-unprivileged:1.29` (intra-priority `30`, deduplicated with original so it will not be checked)

## Combining both levels

Both levels compose naturally. The full sort key is:

1. **CR priority** (`spec.priority`) — ascending
2. **Type order** — ClusterImageSetMirror > ImageSetMirror > ClusterReplicatedImageSet > ReplicatedImageSet
3. **Intra-CR priority** (`mirrors[].priority` / `upstreams[].priority`) — ascending
4. **Declaration order** — YAML position within the CR

The original image is inserted at CR priority `0`, before any CR alternative at the same priority.

**Example**: combining a namespace-scoped mirror with a cluster-wide mirror:

```yaml
# Cluster-wide mirror, slight preference over original
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageSetMirror
metadata:
  name: global-mirror
spec:
  priority: -1
  imageFilter:
    include:
    - .*
  mirrors:
  - registry: harbor.example.com
    path: /global-mirror
    credentialSecret:
      name: harbor-secret
      namespace: kuik-system
---
# Namespace-scoped mirror, strong preference over original
apiVersion: kuik.enix.io/v1alpha1
kind: ImageSetMirror
metadata:
  name: team-mirror
  namespace: my-app
spec:
  priority: -10
  imageFilter:
    include:
    - docker-registry.example.com/my-app/.+
  mirrors:
  - registry: fast-registry.internal
    priority: 1
    path: /my-app-cache
    credentialSecret:
      name: fast-registry-secret
  - registry: harbor.example.com
    priority: 5
    path: /my-app-mirror
    credentialSecret:
      name: harbor-secret
```

For a pod in namespace `my-app` requesting `docker-registry.example.com/my-app/api:v2`, the resulting order is:

1. `fast-registry.internal/my-app-cache/my-app/api:v2` (CR priority `-10`, intra-priority `1`)
2. `harbor.example.com/my-app-mirror/my-app/api:v2` (CR priority `-10`, intra-priority `5`)
3. `harbor.example.com/global-mirror/my-app/api:v2` (CR priority `-1`)
4. `docker-registry.example.com/my-app/api:v2` (original image, priority `0`)

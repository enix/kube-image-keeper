---
title: Resource filtering
description: Select which pods, namespaces and images a kuik resource applies to with the unified spec.filter field.
---

Every kuik resource exposes a single unified `spec.filter` field that selects which pods, namespaces and images it applies to. A filter is a list of typed `include` / `exclude` items, each carrying exactly one selector:

| Item key | Dimension | Matches against | Syntax |
| --- | --- | --- | --- |
| `image` | image | the full normalised image reference | RE2 regular expression |
| `label` | pod labels | the pod's labels | Kubernetes label-selector |
| `annotation` | pod annotations | the pod's annotations | Kubernetes label-selector |
| `namespace` | namespace | the pod's namespace | RE2 regular expression |

The `namespace` item is only available on the cluster-scoped resources (`ClusterImageSetMirror`, `ClusterReplicatedImageSet`, `ClusterImageSetAvailability`); the namespaced variants (`ImageSetMirror`, `ReplicatedImageSet`) have no namespace dimension.

Each item must set **exactly one** selector key (an item with zero or multiple keys is rejected at admission), and each of `include` / `exclude` holds at most 16 items.

```yaml
spec:
  filter:
    include:
    - image: docker\.io/library/nginx:.+
    - label: app=frontend
    - annotation: monitoring=enabled
    exclude:
    - namespace: kube-.*
```

## Matching semantics

Items are grouped by dimension (all `image` items together, all `label` items together, and so on), then evaluated:

- **Within a dimension** — items are OR-ed. A candidate passes the dimension if it matches at least one `include` item of that dimension.
- **Across dimensions** — dimensions are AND-ed. A candidate must pass every dimension that has `include` items.
- **`exclude`** — if a candidate matches any `exclude` item (in any dimension), it is dropped. `exclude` takes precedence over `include`.
- **Empty dimension** — a dimension with no `include` items matches everything. In particular, a filter with only `label` items applies to **every image**.

This makes the filter a faithful superset of the per-dimension filters it replaces: a resource applies to an `(pod, image)` pair when the image passes the image dimension, the namespace passes the namespace dimension, and the pod passes the label and annotation dimensions.

`(Cluster)ReplicatedImageSet` is the one exception: it has no `image` dimension in `spec.filter` (an `image` item is rejected at admission) and selects images per-upstream instead. See [Per-upstream image filtering](#per-upstream-image-filtering-on-clusterreplicatedimageset).

> [!IMPORTANT]
> Kuik [normalises](https://github.com/distribution/reference/blob/main/normalize.go) image references before matching, so short forms are expanded: `busybox:stable` becomes `docker.io/library/busybox:stable`. Always write `image` patterns against the full normalised form. Regex patterns are implicitly anchored (full-string match).

## Label and annotation selector syntax

`label` and `annotation` items use Kubernetes label-selector syntax:

| Form | Meaning |
| ---- | ------- |
| `key` | key is present |
| `!key` | key is absent |
| `key=value` / `key==value` | key equals value |
| `key!=value` | key does not equal value |
| `key in (a, b)` | key value is one of the listed values |
| `key notin (a, b)` | key value is not in the listed values |

> [!WARNING]
> **About annotation values:** equality matches (`key=value`) require values that conform to DNS-1123 label-value syntax (≤ 63 chars, alphanumeric, `-`, `_`, `.`). For free-form annotation values (URLs, JSON blobs, long strings) use presence (`key`) or absence (`!key`).

The same selector syntax also drives the operator's cluster-wide skip list (`skipLabels` / `skipAnnotations` in the [operator configuration](./configuration.md#skiplabels--skipannotations)), which is exclude-only and applies before any CR is consulted, taking precedence over all per-CR filters.

## Per-upstream image filtering on `(Cluster)ReplicatedImageSet`

`(Cluster)ReplicatedImageSet` selects images **per upstream** via `spec.upstreams[].imageFilter`, which chooses the images each upstream entry replicates. That field is unrelated to the deprecated top-level `imageFilter` below and is **not** affected by its deprecation.

Because image selection is per-upstream, the **`image` dimension of the top-level `spec.filter` is not supported** on `(Cluster)ReplicatedImageSet`: an `image` `include` / `exclude` item is rejected at admission. Only the `label`, `annotation` and (cluster-scoped) `namespace` dimensions of `spec.filter` apply, as a resource-wide pod / namespace gate.

## Examples

**Monitor only nginx and redis images:**

```yaml
spec:
  filter:
    include:
    - image: docker\.io/library/nginx:.+
    - image: docker\.io/library/redis:.+
```

**Mirror everything except images from a specific registry:**

```yaml
spec:
  filter:
    exclude:
    - image: untrusted\.registry\.example\.com/.*
```

**Exclude pods managed by CloudNativePG to prevent image rewriting from breaking database clusters:**

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageSetMirror
metadata:
  name: mirror-except-cnpg
spec:
  filter:
    exclude:
    - label: cnpg.io/podRole=instance
  mirrors:
  - registry: registry.example.com
    path: /mirror
```

**Opt-in mirroring via annotation — only mirror images for pods that carry a specific annotation:**

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageSetMirror
metadata:
  name: opt-in-mirror
spec:
  filter:
    include:
    - annotation: my.company.com/kuik-mirror
  mirrors:
  - registry: registry.example.com
    path: /mirror
```

**Combine namespace, pod and image filtering — mirror non-monitoring pods in `prod-*` namespaces:**

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageSetMirror
metadata:
  name: prod-non-monitoring
spec:
  filter:
    include:
    - namespace: prod-.*
    exclude:
    - label: app.kubernetes.io/part-of=monitoring
  mirrors:
  - registry: registry.example.com
    path: /mirror
```

## Migration from `imageFilter` / `namespaceFilter` / `podFilter`

Earlier releases exposed three separate fields. They map onto `spec.filter` items as follows:

| Legacy field | `spec.filter` item |
| --- | --- |
| `imageFilter.include: [P]` | `include: [{image: P}]` |
| `imageFilter.exclude: [P]` | `exclude: [{image: P}]` |
| `namespaceFilter.include: [N]` | `include: [{namespace: N}]` |
| `namespaceFilter.exclude: [N]` | `exclude: [{namespace: N}]` |
| `podFilter.labels.include: [S]` | `include: [{label: S}]` |
| `podFilter.labels.exclude: [S]` | `exclude: [{label: S}]` |
| `podFilter.annotations.include: [S]` | `include: [{annotation: S}]` |
| `podFilter.annotations.exclude: [S]` | `exclude: [{annotation: S}]` |

- **`namespaceFilter` and `podFilter` have been removed.** They only ever shipped in `v2.3` beta releases. Resources that still set them will have those fields **pruned by the API server on the next apply** — move their selectors into `spec.filter` before upgrading.
- **`imageFilter` is deprecated but still works.** It is **mutually exclusive** with `spec.filter`: setting both on the same resource is rejected at admission. When migrating, fold the `imageFilter` patterns into `spec.filter` `image` items and remove `imageFilter`.

> [!WARNING]
> One semantic difference when folding `imageFilter` into `filter`: an **empty image dimension matches every image**, whereas an omitted `imageFilter` matched nothing. If you rely on `imageFilter` to restrict which images a resource handles, keep at least one `image` item in `spec.filter` — otherwise the resource applies to all images.

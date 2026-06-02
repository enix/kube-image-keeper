---
title: Resource filtering
---

Kuik resources expose three independent filter fields:

| Filter | Available on | Default when omitted |
| --- | --- | --- |
| `imageFilter` | all five CRDs | nothing matches (must be set explicitly) |
| `namespaceFilter` | cluster-scoped CRDs only | every namespace is in scope |
| `podFilter` | all five CRDs | every pod is in scope |

All three filters are optional and compose independently: a resource applies to an image when the image passes `imageFilter`, the pod's namespace passes `namespaceFilter`, and the pod itself passes `podFilter`.

## Image filtering (`imageFilter`)

`imageFilter` is available on all five CRDs: `ClusterImageSetMirror`, `ImageSetMirror`, `ClusterReplicatedImageSet`, `ReplicatedImageSet`, and `ClusterImageSetAvailability`. It selects images by their full normalised reference using RE2 regular expressions.

:::caution
Kuik [normalises](https://github.com/distribution/reference/blob/main/normalize.go) image references before matching, so short forms are expanded: `busybox:stable` becomes `docker.io/library/busybox:stable`. Always write patterns against the full normalised form.
:::

### Fields

| Field | Required | Description |
| --- | --- | --- |
| `spec.imageFilter` | | Rules used to select which images this resource applies to. When both `include` and `exclude` are empty (or the field is omitted), no images match. |
| `spec.imageFilter.include` | | List of RE2 regex patterns. When non-empty, only images matching at least one pattern are in scope. |
| `spec.imageFilter.exclude` | | List of RE2 regex patterns. Images matching any pattern are removed from scope (takes precedence over `include`). When `include` is omitted, a `.*` is injected so that `exclude`-only filters match everything except the excluded patterns. |

:::note
For `(Cluster)ReplicatedImageSet`, the filter lives at `spec.upstreams[].imageFilter` and applies per upstream entry.
:::

### Semantics

- **Both empty** (`imageFilter` omitted or both fields left empty) — nothing matches; no images are selected.
- **`exclude` only** (`include` empty, `exclude` non-empty) — `.*` is injected as the include pattern, so every image is in scope except those matching `exclude`.
- **`include` only** — only images matching at least one `include` pattern are in scope.
- **Both set** — only images matching `include` are in scope, minus those matching `exclude`.

:::caution
Patterns are implicitly anchored (full-string match).
:::

### Examples

**Monitor only nginx and redis images:**

```yaml
spec:
  imageFilter:
    include:
    - docker\.io/library/nginx:.+
    - docker\.io/library/redis:.+
```

**Mirror everything except images from a specific registry:**

```yaml
spec:
  imageFilter:
    exclude:
    - untrusted\.registry\.example\.com/.*
```

**Scope an upstream to a specific image path:**

```yaml
spec:
  upstreams:
  - registry: ghcr.io
    path: /myorg/myapp
    imageFilter:
      include:
      - ghcr\.io/myorg/myapp:.+
```

## Namespace filtering (`namespaceFilter`)

`namespaceFilter` is available on the three cluster-scoped resources: `ClusterImageSetMirror`, `ClusterReplicatedImageSet`, and `ClusterImageSetAvailability`. It has no effect on the namespaced variants (`ImageSetMirror`, `ReplicatedImageSet`).

### Fields

| Field | Required | Description |
| --- | --- | --- |
| `spec.namespaceFilter` | | Restricts which namespaces this resource applies to. Omitted or empty means the resource applies to every namespace. |
| `spec.namespaceFilter.include` | | List of RE2 regex patterns. When non-empty, the resource only applies to pods whose namespace matches at least one entry. |
| `spec.namespaceFilter.exclude` | | List of RE2 regex patterns. Pods whose namespace matches any entry are out of scope. |

### Semantics

Both `include` and `exclude` hold lists of RE2 regular expressions matched against the pod namespace name (full-string match, implicitly anchored).

- **Empty `include`** — every namespace is in scope (default-allow).
- **Non-empty `include`** — only namespaces matching at least one entry are in scope.
- **`exclude`** — removes namespaces from scope; takes precedence over `include` when both match.

### Examples

**Scope to a set of namespaces by prefix:**

```yaml
spec:
  namespaceFilter:
    include:
    - prod-.*
```

**Exclude system namespaces, keep everything else:**

```yaml
spec:
  namespaceFilter:
    exclude:
    - kube-.*
    - kuik-system
```

**Narrow to a prefix family while carving out a legacy namespace:**

```yaml
spec:
  namespaceFilter:
    include:
    - prod-.*
    exclude:
    - prod-legacy
```

**Full example — mirror images only in `prod-*` namespaces:**

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageSetMirror
metadata:
  name: prod-mirror
spec:
  namespaceFilter:
    include:
    - prod-.*
  imageFilter:
    include:
    - .*
  mirrors:
  - registry: registry.example.com
    path: /mirror
```

## Pod filtering (`podFilter`)

:::note
The operator also exposes a cluster-wide skip list (`skipLabels` / `skipAnnotations` in the [operator configuration](/configuration/#skiplabels--skipannotations)). It uses the same selector syntax as `podFilter` but is exclude-only (no include list) and applies before any CR is consulted, taking precedence over all per-CR filters.
:::

`podFilter` is available on all five CRDs: `ClusterImageSetMirror`, `ImageSetMirror`, `ClusterReplicatedImageSet`, `ReplicatedImageSet`, and `ClusterImageSetAvailability`. It selects pods by their labels and/or annotations using Kubernetes label-selector syntax.

### Fields

| Field | Required | Description |
| --- | --- | --- |
| `spec.podFilter` | | Restricts which pods this resource applies to, by pod labels and annotations. Omitted or empty means the resource applies to every pod. |
| `spec.podFilter.labels.include` | | List of Kubernetes label selectors. Pods matching at least one entry are in scope. When omitted, all pods are in scope as far as labels are concerned. |
| `spec.podFilter.labels.exclude` | | List of Kubernetes label selectors. Pods matching any entry are removed from scope (takes precedence over `include` on tie). |
| `spec.podFilter.annotations.include` | | List of selector strings matched against pod annotations. Same syntax as label selectors. |
| `spec.podFilter.annotations.exclude` | | List of selector strings matched against pod annotations. Same syntax as label selectors. |

### Syntax

`podFilter.labels` and `podFilter.annotations` each accept `include` and `exclude` lists of selector strings. Supported forms:

| Form | Meaning |
| ---- | ------- |
| `key` | key is present |
| `!key` | key is absent |
| `key=value` / `key==value` | key equals value |
| `key!=value` | key does not equal value |
| `key in (a, b)` | key value is one of the listed values |
| `key notin (a, b)` | key value is not in the listed values |

Multiple selectors within one list are OR-ed (a pod in scope if it matches any entry). `exclude` takes precedence over `include` when a pod matches both.

`labels` and `annotations` filtering are applied independently and AND-ed: a pod must satisfy both to remain in scope.

:::caution
**About annotation values:** equality matches (`key=value`) require values that conform to DNS-1123 label-value syntax (≤ 63 chars, alphanumeric, `-`, `_`, `.`). For free-form annotation values (URLs, JSON blobs, long strings) use presence (`key`) or absence (`!key`).
:::

### Examples

**Exclude pods managed by CloudNativePG to prevent image rewriting from breaking database clusters:**

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageSetMirror
metadata:
  name: mirror-except-cnpg
spec:
  podFilter:
    labels:
      exclude:
      - cnpg.io/podRole=instance
  imageFilter:
    include:
    - .*
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
  podFilter:
    annotations:
      include:
      - my.company.com/kuik-mirror
  imageFilter:
    include:
    - .*
  mirrors:
  - registry: registry.example.com
    path: /mirror
```

**Combine namespace and pod filtering — scope to `prod-*` namespaces, exclude monitoring pods:**

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageSetMirror
metadata:
  name: prod-non-monitoring
spec:
  namespaceFilter:
    include:
    - prod-.*
  podFilter:
    labels:
      exclude:
      - app.kubernetes.io/part-of=monitoring
  imageFilter:
    include:
    - .*
  mirrors:
  - registry: registry.example.com
    path: /mirror
```

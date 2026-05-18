# Advanced resource filtering

kuik cluster-scoped resources can limit which namespaces and which pods they act on via two independent filter fields: `namespaceFilter` and `podFilter`. Both fields are optional; omitting either means no restriction is applied on that axis.

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

> **Note on annotation values:** equality matches (`key=value`) require values that conform to DNS-1123 label-value syntax (≤ 63 chars, alphanumeric, `-`, `_`, `.`). For free-form annotation values (URLs, JSON blobs, long strings) use presence (`key`) or absence (`!key`).

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

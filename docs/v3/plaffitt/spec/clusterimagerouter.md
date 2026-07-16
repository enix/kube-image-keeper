---
description: Route Pod container images to the first available equivalent location, in a fixed preference order.
---

# ClusterImageRouter

> [!NOTE]
> **v3 draft.** This page is written docs-first to pressure-test the v3 design before any code is
> written. Field names and defaults are provisional. See
> [v3 design decisions](../README.md) for the rationale behind every choice here.

A `ClusterImageRouter` declares that several registry locations hold **the same image**, and the
order in which kuik should prefer them. On Pod creation, kuik's mutating webhook rewrites each
matching container to the **first available** location in that order.

Routing is kuik's base feature. [`ClusterImageCopier`](./clusterimagecopier.md) (copying) and the
router's own `status` (monitoring) exist only to serve it.

## How it works

1. A Pod is created; the webhook matches each container image against every `ClusterImageRouter`.
2. For a matching router, kuik builds the ordered list of `alternatives` by **prefix swap** (see
   below) and checks their availability, top to bottom.
3. The container is rewritten to the **first available** alternative. If none are available, the
   Pod keeps its original image (a router never blocks a Pod).
4. What kuik observed — which images were seen, which alternatives were available, where it routed —
   is recorded in `.status` (this is also how [`ClusterImageCopier`](./clusterimagecopier.md) learns
   what to copy).

### Alternatives are prefixes, matched by prefix swap

Each entry in `spec.alternatives` is a **registry + path prefix**. The first entry that is a prefix
of the Pod's image (matched on path-segment boundaries) matches; the remainder of the reference — the
repository path plus the tag or digest — is the captured suffix and is reattached, unchanged, to every
other alternative.

For a Pod requesting `docker.io/jpetazzo/foo:v1`:

```yaml
spec:
  alternatives:
  - docker.io/jpetazzo/               # matches, suffix = "foo:v1"
  - ghcr.io/jpetazzo/                 # → ghcr.io/jpetazzo/foo:v1
  - harbor.enix.io/jpetazzo/mirrors/  # → harbor.enix.io/jpetazzo/mirrors/foo:v1
```

kuik never remaps the tag or digest: the same `:v1` (or `@sha256:…`) is used at every location.

> [!IMPORTANT]
> kuik assumes a tag maps to the **same content** at every alternative. Mutable tags (`:latest`, or a
> tag you re-push) break that assumption. See [Mutable tags](#mutable-tags-and-imagepullpolicy) below.

### Order is absolute

The list is a **platform policy**, not a per-Pod preference. kuik tries the alternatives strictly
top-to-bottom and uses the first available one — **regardless of which location the Pod originally
requested**. This is what lets you prefer a local mirror over a public registry even when a Pod pins
the public one:

```yaml
spec:
  alternatives:
  - harbor.enix.io/mirrors/   # always preferred, even if the Pod requested docker.io/...
  - docker.io/
```

"Maximize availability" means *find an available copy* — order encodes your preference **among**
available copies, and the availability check still stops at the first one that is actually up.

### Scoping images with the filter

Prefixes decide *where the same image lives*; `spec.filter`'s **image dimension** decides *which of
the matched images this router governs*. It runs **after** prefix match, testing the Pod's original
image reference against `include` / `exclude` regexes (default-allow when `include` is empty). If it
excludes the image, the router does not apply — the image falls through to the next router or keeps
its original.

This is how you carve exceptions out of a broad router. For example, mirror everything except one
project:

```yaml
spec:
  filter:
    exclude:
    - image: .+/cloudnative-pg/(postgresql|cloudnative-pg):.+
  alternatives:
  - harbor.enix.io/enix-k8s-mirror/
  - ""    # every other image, at its original location
```

> [!NOTE]
> The image filter is **per router**, not per alternative. To drop an image from *one* alternative
> but keep it on others, you don't need to configure anything: an image absent from an alternative
> simply isn't available there, and routing skips it. Use the image filter only to remove an image
> from the router's scope entirely.

## Fields

| Field | Required | Description |
| --- | --- | --- |
| `spec.alternatives[]` | ✅ | Ordered list of registry+path prefixes that hold the same images. Order is the absolute preference order (first = most preferred). Matched by prefix swap on path-segment boundaries. |
| `spec.priority` | | Cross-router precedence: when two routers match the same image, the one with the **lowest** `priority` wins. Signed integer, default `0`. At **equal** priority, the router with the **more specific (longer) matching prefix** wins. Does **not** reorder alternatives within a router (that is `alternatives` order). |
| `spec.filter` | | Selects which Pods / namespaces / images this router applies to (label, annotation, namespace and `image` dimensions). Prefix matching decides candidacy and captures the swap suffix; the `image` dimension then **refines** which matched images the router governs (typically exclusions) — see [Scoping images with the filter](#scoping-images-with-the-filter). See [Resource filtering](../../../concepts/resource-filtering.md). |
| `spec.rewriteOnAlwaysImagePullPolicy` | | Per-router override: route containers with `imagePullPolicy: Always` even for tag-based refs. Defaults to the global `routing.rewriteOnAlwaysImagePullPolicy`. See [Mutable tags](#mutable-tags-and-imagepullpolicy). |

### Priority (cross-router only)

Within a router, precedence is the `alternatives` list order — there is no per-entry priority.

`spec.priority` disambiguates **different routers that match the same image**. The router with the
lowest `priority` value wins.

At **equal priority, the more specific match wins**: kuik compares the `alternatives` prefix that
actually matched the image in each router and picks the router with the **longer** matching prefix
(on path-segment boundaries) — the same longest-prefix rule the
[`ClusterImageCopier`](./clusterimagecopier.md#binding-by-prefix-not-by-reference) uses. So a broad
router and a narrower one can safely coexist and the narrower one takes precedence where it applies,
with no priority tuning:

```yaml
# Router "all-of-dockerhub" — broad default
spec:
  alternatives:
  - ghcr.io/docker/
  - docker.io/            # matches docker.io/library/nginx:1.25 (prefix length: "docker.io/")
---
# Router "docker-library" — more specific; wins for docker.io/library/* at equal priority
spec:
  alternatives:
  - public.ecr.aws/docker/library/
  - docker.io/library/    # also matches, longer prefix → this router wins
```

Specificity is **prefix length** (character count), so `docker.io/library` wins over `docker.io/li/`.

Only when the matching prefix is **identical** (same priority *and* same prefix) is the overlap a true
conflict: it is then broken deterministically by name and surfaced as a `ConflictingRouter`
[`status`](#status) condition, so it is not silent. Different-specificity overlaps are resolved by
design and do **not** warn.

If you find yourself wanting to interleave two routers' alternatives, that is a signal they are one
policy — put the entries in a single router. Cross-router interleaving is intentionally not
expressible.

## Mutable tags and `imagePullPolicy`

kuik assumes tags are immutable. Kubernetes' signal for a possibly-moving tag is
`imagePullPolicy: Always` (and Kubernetes defaults `:latest`/untagged images to `Always`).

- **By default, containers with `Always` and a tag-based ref are not rewritten** — they keep their
  original image so the kubelet reaches the upstream source of truth. Routing a moving tag to a mirror
  could silently serve a stale copy.
- **Digest-pinned refs (`@sha256:…`) are always routed**, even under `Always`: the digest guarantees
  identical content everywhere.
- To route `Always` through mirrors anyway (e.g. `Always` enforced cluster-wide, tags known
  immutable, rate-limit protection wanted), set `routing.rewriteOnAlwaysImagePullPolicy` globally or
  `spec.rewriteOnAlwaysImagePullPolicy` per router.
- Containers with `imagePullPolicy: Never` are skipped by default (the node must already hold the
  image under its original name); flip with `routing.rewriteOnNeverImagePullPolicy`.

> [!WARNING]
> `imagePullPolicy` is a proxy, not a guarantee. A non-`latest` tag re-pushed with `IfNotPresent` can
> still serve stale content. For mutable content, use `Always` or pin a digest.

## Status

The router's `status` is kuik's monitoring surface: it records the images actually seen on the
cluster and their availability per alternative, with a last-checked timestamp used to skip redundant
active checks within the configured freshness window.

```yaml
status:
  images:
  - reference: docker.io/jpetazzo/foo:v1
    lastSeen: "2026-07-09T10:00:00Z"
    routedTo: harbor.enix.io/jpetazzo/mirrors/foo:v1
    alternatives:
    - image: harbor.enix.io/jpetazzo/mirrors/foo:v1
      available: true
      lastChecked: "2026-07-09T10:00:01Z"
    - image: docker.io/jpetazzo/foo:v1
      available: true
      lastChecked: "2026-07-09T10:00:01Z"
  conditions:
  - type: ConflictingRouter
    status: "False"
```

Images that stop being used are garbage-collected from `status` after an expiry window, so the status
stays bounded.

## Example: prefer a local mirror, fall back to public registries

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageRouter
metadata:
  name: jpetazzo-images
spec:
  filter:
    include:
    - namespace: apps-.*
  alternatives:
  - harbor.enix.io/jpetazzo/mirrors/  # local, preferred; populated by a ClusterImageCopier
  - docker.io/jpetazzo/
  - ghcr.io/jpetazzo/
```

Pods in namespaces matching `apps-.*` that request `docker.io/jpetazzo/foo:v1` (or the `ghcr.io`
equivalent) are routed to `harbor.enix.io/jpetazzo/mirrors/foo:v1` when it is available. To have kuik
populate that harbor location automatically from an available public source, pair this router with a
[`ClusterImageCopier`](./clusterimagecopier.md) owning the `harbor.enix.io/jpetazzo/mirrors/` prefix.

## See also

- [ClusterImageCopier](./clusterimagecopier.md) — populates missing mirror locations on demand.
- [Resource filtering](../../../concepts/resource-filtering.md) — `spec.filter` semantics.
- [v3 design decisions](../README.md) — rationale and open questions.

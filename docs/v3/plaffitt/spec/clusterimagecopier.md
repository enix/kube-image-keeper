---
description: Populate mirror locations on demand by copying images a ClusterImageRouter needs but cannot find.
---

# ClusterImageCopier

> [!NOTE]
> **v3 draft.** This page is written docs-first to pressure-test the v3 design before any code is
> written. Field names and defaults are provisional. See
> [v3 design decisions](../README.md) for the rationale behind every choice here.

A `ClusterImageCopier` **owns a destination prefix** and keeps it populated. When a
[`ClusterImageRouter`](./clusterimagerouter.md) wants an image at a location under that prefix but the
location is not available, the copier copies the image there from an available alternative.

Copying is not a standalone feature: it exists only to improve availability **for routing**. A copier
does nothing on its own — it reacts to what routers actually need.

## How it works

1. A copier declares a `destinationPrefix` (a push target it controls) and push credentials.
2. It watches every `ClusterImageRouter` `status`. For each image a router wanted at a location under
   the copier's prefix but found unavailable, the copier has a job to do.
3. It copies that image from one of the router's currently-available alternatives into the
   destination, preserving the tag/digest.
4. On the next Pod admission, the router finds the mirror available and routes to it.

Because jobs originate from router `status` — which only ever contains images that were actually
admitted on the cluster — copying is inherently **usage-driven**: kuik never copies an image no Pod
uses. This is what keeps kuik from being a general-purpose image mover.

> [!NOTE]
> A copier's prefix is a **responsibility scope, not a trigger**. A broad prefix such as
> `harbor.enix.io/**` only makes the copier responsible for more destinations; it does not cause it to
> copy anything that a router did not already need.

### Binding by prefix, not by reference

A copier does not reference a router. Router and copier meet implicitly at the **destination
prefix**: any number of routers pushing to the same prefix are served by the one copier that owns it.
There is no reference to dangle — if no copier owns a prefix, that location simply never gets
populated (surfaced in the router's `status`).

When two copiers own overlapping prefixes, the **longest matching prefix** wins (matched on
path-segment boundaries). Exact-duplicate prefixes are a misconfiguration: resolved deterministically
and surfaced as a `status` condition.

## Credentials

| Direction | Source of credentials |
| --- | --- |
| **Pull** (from the source alternative) | The originating router's credentials for that alternative — the router already holds them for availability checks, so they are not duplicated here. |
| **Push** (to the destination) | The copier's own `pushSecret`, falling back to the originating router's credentials. |

> [!WARNING]
> The push fallback to router credentials usually fails: a router's credentials typically carry only
> **pull** scope (all that availability checks need). When the fallback lacks push rights, the copy
> fails with a clear auth error — it is not silent. Provide a `pushSecret` with push scope.

## Fields

| Field | Required | Description |
| --- | --- | --- |
| `spec.destinationPrefix` | ✅ | The registry+path prefix this copier is responsible for populating. Must be a push target you control. Matched by longest-prefix on path-segment boundaries. |
| `spec.pushSecret` | | Reference to a Secret with **push** credentials for the destination. Falls back to the originating router's credentials if unset (usually pull-only — see warning above). |
| `spec.pushSecret.name` | | Name of the Secret. |
| `spec.pushSecret.namespace` | | Namespace of the Secret. |
| `spec.platforms[]` | | Platforms to copy (e.g. `linux/amd64`, `linux/arm64`). Defaults to all platforms present in the source. Restricting platforms produces a smaller image than the source. |

## Status

```yaml
status:
  copied:
  - destination: harbor.enix.io/jpetazzo/mirrors/foo:v1
    source: docker.io/jpetazzo/foo:v1
    copiedAt: "2026-07-09T10:00:05Z"
  conditions:
  - type: PushAuth
    status: "True"
```

## Example: populate a local harbor mirror on demand

Paired with the router example on the [`ClusterImageRouter`](./clusterimagerouter.md) page:

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageCopier
metadata:
  name: jpetazzo-harbor
spec:
  destinationPrefix: harbor.enix.io/jpetazzo/mirrors/
  pushSecret:
    name: harbor-push
    namespace: kuik-system
  platforms:
  - linux/amd64
  - linux/arm64
```

With this copier in place, the first Pod that requests `docker.io/jpetazzo/foo:v1` while
`harbor.enix.io/jpetazzo/mirrors/foo:v1` is missing keeps running from the public source, and the
copier populates harbor from an available alternative so that subsequent Pods route to the local
mirror.

> [!TIP]
> If someone simply wants to "copy images from X to Y", declaring a router (X and Y as alternatives)
> plus a copier (owning Y) achieves it — and incidentally sets up redundancy, which is exactly what
> kuik is for. Bulk pre-migration of images no Pod runs is intentionally **not** supported: copying is
> usage-driven.

## See also

- [ClusterImageRouter](./clusterimagerouter.md) — the routing policy a copier serves.
- [v3 design decisions](../README.md) — rationale and open questions.

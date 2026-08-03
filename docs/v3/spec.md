# spec v3

## Scope and filtering

The three custom resources (`ImageAlternative`, `ImageMirror`, `ImageMonitor`) are all
**cluster-scoped**. There is no namespaced variant: in v2 the mirror and replication kinds each
had a namespaced peer (`ImageSetMirror`, `ReplicatedImageSet`), v3 drops them. Restricting a
resource to a subset of the cluster is done with `namespaceSelector`, not by creating the object
in a given namespace:

```yaml
namespaceSelector:
  matchLabels:
    kubernetes.io/metadata.name: monitoring
```

Filtering is expressed on **workloads**, not on images: `podSelector` and `namespaceSelector`
select the pods a resource applies to, and an empty selector matches everything. The single
exception is `ImageMirror`'s `spec.excludeImagePrefixes`, which keeps specific images out of a
mirror (e.g. huge images) without changing which pods the mirror applies to.

## ImageAlternative

```yaml
kind: ImageAlternative
metadata:
  name: acme-foo
spec:
  # Generic Kubernetes labels selector field to restrict pods where this CR apply
  # https://kubernetes.io/docs/reference/generated/kubernetes-api/latest/#labelselector-v1-meta
  # Empty/Nothing match all pod
  podSelector: {}
  namespaceSelector: {}

  # Image rewrite policy used in mutating webhook
  #   OnFailure: Default. Keep using original image if available, else use first available
  #             image in `alternatives` list
  #   Always: Always rewrite image to first available in `alternatives` list
  #          (bypass quota, latency, network cost, …)
  rewritePolicy: OnFailure    # OnFailure | Always

  # Ordered list of equivalent images (or image subpaths) that could be used if one is
  # unavailable. All entries of a list must use the same form, see "Alternatives matching"
  # Every field besides `imagePrefix` is optional, and with public registries only
  # `imagePrefix` is usually needed
  alternatives:
  - imagePrefix: quay.io/acme/foo
  - imagePrefix: docker.io/acme-org/foo
  - imagePrefix: 123456.dkr.ecr.eu-west-3.amazonaws.com/repo/acme/foo
    auth:
      provider:                    # Provider specific auth, like AWS IRSA, same logic as
        name: aws                  # https://fluxcd.io/flux/components/source/ocirepositories/#provider
        serviceAccountRef:
          name: kuik-ecr-access
      injectPullSecret: false      # Default: false (for provider) - kubelet already have permission
                                   # to pull from provider registry without additional secret
  - imagePrefix: "registry.local:5000/mirror/acme/foo"
    insecure: true                 # HTTP registry
    unavailable: true              # Image no longer available in this repository but if a pod
                                   # use this image, we'll try to substitute an alternative
    auth:                          # credentials to pull images from the registry
      secretRef:
        name: local-registry
      injectPullSecret: true       # Default: true (for secretRef)
                                   # inject secret in pod namespace if this image is used as alternative
                                   # so kubelet could pull the image from the registry
```

### Alternatives matching

The `imagePrefix` of an entry is **not** a glob: there is no implicit wildcard, and no glob
marker is supported. It is either a **single image** or a **subpath**, and the form is given
by the trailing character:

| Form | Written as | Matches |
| ---- | ---------- | ------- |
| Single image | no trailing separator (`quay.io/acme/foo`) | that exact repository only, whatever the tag or digest |
| Subpath | significant trailing `/` (`quay.io/acme/foo/`) | repositories located **directly** under that path (one level only), like a trailing slash for rsync directories |

An `imagePrefix` is a prefix of the image reference at path segment granularity, not a free
form string prefix: `quay.io/acme/foo` matches `quay.io/acme/foo:v1` but never
`quay.io/acme/foo-bar:v1`.

When an image matches, the tag or digest is always preserved, and for the subpath form the
matched remainder (the single path segment below the subpath) is preserved too.

#### Single image form

```yaml
alternatives:
- imagePrefix: quay.io/acme/foo
- imagePrefix: docker.io/acme-org/foo
```

| Image | Result |
| ----- | ------ |
| `quay.io/acme/foo:latest` | matches, rewritten to `docker.io/acme-org/foo:latest` |
| `quay.io/acme/foo-bar:latest` | doesn't match (no prefix matching on a segment) |
| `quay.io/acme/foo/bar:latest` | doesn't match (deeper than the entry) |
| `quay.io/acme/foo/bar/oni:latest` | doesn't match |

#### Subpath form

```yaml
alternatives:
- imagePrefix: quay.io/acme/foo/
- imagePrefix: docker.io/acme-org/foo/
```

| Image | Result |
| ----- | ------ |
| `quay.io/acme/foo:latest` | doesn't match (the subpath itself is not an image) |
| `quay.io/acme/foo-bar:latest` | doesn't match |
| `quay.io/acme/foo/bar:latest` | matches, rewritten to `docker.io/acme-org/foo/bar:latest` |
| `quay.io/acme/foo/bar/oni:latest` | doesn't match (only one level below the subpath) |

#### Invalid `alternatives`

Rejected at admission (validation webhook or CEL rules):

- an `imagePrefix` ending with `:` (it looks like a "any tag of this image" marker, but the
  single image form already covers it):

  ```yaml
  alternatives:
  - imagePrefix: "quay.io/acme/foo:"        # invalid
  - imagePrefix: "docker.io/acme-org/foo:"  # invalid
  ```

- an `imagePrefix` carrying a tag or a digest (`quay.io/acme/foo:v1`,
  `quay.io/acme/foo@sha256:…`): alternatives describe repositories, the tag or digest comes
  from the pod
- mixing the two forms in the same list, since the two sides would not describe the same
  set of images:

  ```yaml
  alternatives:
  - imagePrefix: "quay.io/acme/foo:"       # invalid (trailing `:`) and mixed forms
  - imagePrefix: docker.io/acme-org/foo/
  ```

  ```yaml
  alternatives:
  - imagePrefix: quay.io/acme/foo          # single image form
  - imagePrefix: docker.io/acme-org/foo/   # subpath form => invalid
  ```

Keeping the two forms explicit and non mixable keeps the CR readable and avoids rewriting an
image to an unrelated one because two repository names happen to share a prefix.

#### Overlapping alternatives across several CRs

Several `ImageAlternative` may match the same image, for instance one declaring `quay.io/acme/` and
another declaring `quay.io/acme/foo`. They are **not** mutually exclusive: every matching CR
contributes its alternatives, and the resulting lists are merged into a single candidate list by
[the total order](#the-total-order) — by `rewritePolicy` first, then lexicographic CR name within a
policy. Specificity decides which *entry* of a CR matches (and therefore the remainder to carry
over), not which CR owns the image.

Merging rather than electing a single owner means overlapping CRs add fallbacks instead of shadowing
each other, and the only case reported as a conflict is the one where two CRs actually disagree about
what to put ahead of the original: see
[Duplicate and conflicting candidates](#duplicate-and-conflicting-candidates).

## ImageMirror

```yaml
kind: ImageMirror
metadata:
  name: prod-mirror
spec:
  # Generic Kubernetes labels selector field to restrict pods where this CR apply
  # https://kubernetes.io/docs/reference/generated/kubernetes-api/latest/#labelselector-v1-meta
  # Empty/Nothing match all pod
  podSelector: {}
  namespaceSelector: {}
  # List of image prefix to explicitly exclude from mirroring (e.g. huge images)
  excludeImagePrefixes:
  - ghcr.io/foo-bar/

  # Image rewrite policy used in mutating webhook
  #   OnFailure: Default. Keep using original image if available, else use the mirrored
  #             image in `destination`
  #   Always: Always rewrite image to the mirrored image in `destination`
  #          (bypass quota, latency, network cost, …)
  #   None: Only copy image to mirror, don't use it as an image alternative (archiving, compliance, security scan, …)
  rewritePolicy: OnFailure    # OnFailure (default) | Always | None

  destination:
    path: registry.tld/mirror/
    insecure: false            # Default: false - Allow HTTP registry
    push:                      # Credentials for controller to push images and delete unused tags
      auth:                    # Same schema as `ImageAlternative` `spec.alternatives[].auth`,
                               # except `injectPullSecret` which is ignored here: push credentials
                               # are only used by the controller, never injected in namespaces
        secretRef:
          name: mirror-write-credentials
    pull:                      # Credentials to pull image that will be synced in namespaces
      auth:                    # with rewritten image to use destination registry
                               # Same schema as `ImageAlternative` `spec.alternatives[].auth`
        secretRef:
          name: mirror-read-credentials
        injectPullSecret: true # Default: true (for secretRef), false for `provider`
                               # inject secret in pod namespace when an image is rewritten to this
                               # mirror, so kubelet could pull it from the destination registry

  cleanup:
    enabled: true              # Default: true - Delete image tag no longer referenced by any pod
    retention: 24h             # Image tag hold duration before deleting them, to deal with cronjob for instance


  # Detect and reconcile image tag drift (digest change), e.g. tag `latest`
  #   Ignore: Image is copied once on destination registry and not updated if upstream tag digest change
  #   Warn: Periodically check if tag digest is still the same and warn if different
  #   Sync: Periodically check if tag digest is still the same and resync image in destination if different
  driftPolicy: Ignore          # Ignore (default) | Warn | Sync

  # Multi-arch support
  #   Auto: Copy images for arch retrieved from node labels
  #   All: Copy all arch referenced for an image
  #   List: Explicit list of arch we need to copy images
  platforms:
    mode: Auto                 # Auto (default) | All | List
    #list: []                  # Only used with `mode: List`
```

## ImageMonitor

```yaml
kind: ImageMonitor
metadata:
  name: cluster-images
spec:
  # Generic Kubernetes labels selector field to restrict pods where this CR apply
  # https://kubernetes.io/docs/reference/generated/kubernetes-api/latest/#labelselector-v1-meta
  # Empty/Nothing match all pod
  podSelector: {}
  namespaceSelector: {}

  unusedImageExpiry: 24h       # keep monitoring for a given time after no longer used in cluster
                               # useful for cronjob

  driftDetection: true         # Default: true - Detect if an image tag digest differ from pod running in cluster

  monitorAlternatives: false   # Default: false - Also monitor alternatives images instead of only original ones

```

## Candidate ordering across resources

An image may be covered by several `ImageAlternative` **and** by one or more `ImageMirror` (whose
routing scope is defined by their selectors). `ImageMonitor` never contributes a candidate. v2 ordered
these with a signed `spec.priority` plus a default kind order (Original, CISM, ISM, CRIS, RIS); v3 has
no `priority` field and derives the whole order from `rewritePolicy` plus a fixed kind order.

### The total order

> **`Always` `ImageMirror`** (lexicographic among them) → **`Always` `ImageAlternative`** (lex) →
> **original image** → **`OnFailure` `ImageAlternative`** (lex) → **`OnFailure` `ImageMirror`** (lex)

- the **original appears exactly once, at the pivot**, whatever its declared position in any
  `alternatives` list. All the other entries of a CR go to that CR's band, keeping their declared order
- each matching `ImageMirror` contributes **one** candidate, built from the **original** reference —
  not from whatever an `ImageAlternative` would resolve to — by joining `destination.path` with the
  full original reference, registry host included:

  ```text
  destination.path              registry.example.com/mirror/
  original image                docker.io/library/nginx:1.27
  mirror candidate              registry.example.com/mirror/docker.io/library/nginx:1.27
  ```

- `rewritePolicy: None` on an `ImageMirror` contributes no candidate at all: the image is copied to
  the destination but never used for routing. A mirror also contributes nothing for images matching
  its `excludeImagePrefixes`
- candidates are then **deduplicated keeping the first occurrence**, by resulting reference *including
  its resolved config* (see [Duplicate and conflicting candidates](#duplicate-and-conflicting-candidates))

Rationale for that order: an `Always` mirror exists precisely for latency and quota reasons, so it must
beat a distant upstream alternative. Under `OnFailure` the upstream alternatives are fresh and
canonical, so they come before the local copy and the mirror remains the **ultimate safety net**.

> [!TIP]
> **In the common case there is nothing to think about**: `ImageAlternative` resources that do not
> overlap each other, plus a single `ImageMirror`, produce the order you would naturally expect —
> original, then the declared alternatives, then the mirror as a last resort (or the mirror first
> under `Always`). Sorting by name only matters when several CRs *of the same kind and the same
> policy* cover the same image.

Lexicographic order rather than `creationTimestamp` (the tie-break Gateway API uses for its
conflicts): a timestamp is not stable under GitOps, where deleting and recreating an object silently
changes precedence, whereas the name is. `kubectl get imagealternatives` then displays the
within-kind order for free.

Worked example, an `ImageMirror` and the `ImageAlternative` of
[example 07](./examples/07-alternative-and-mirror-composition.yaml) both matching a `nginx:1.27` pod.
The alternatives are declared `public.ecr.aws/docker/library/`, `mirror.gcr.io/library/`,
`docker.io/library/` — so the original is one of them — and `M` is the mirror candidate above:

| `ImageMirror` | `ImageAlternative` | Candidate order |
| ------------- | ------------------ | --------------- |
| `OnFailure` | `OnFailure` | `docker.io` → `public.ecr.aws` → `mirror.gcr.io` → **M** |
| `OnFailure` | `Always` | `public.ecr.aws` → `mirror.gcr.io` → `docker.io` → **M** |
| `Always` | `OnFailure` | **M** → `docker.io` → `public.ecr.aws` → `mirror.gcr.io` |
| `Always` | `Always` | **M** → `public.ecr.aws` → `mirror.gcr.io` → `docker.io` |

Since the original sits at the pivot regardless of where it is declared, `Always` on an
`ImageAlternative` is never a no-op: entries declared after the original still land ahead of it.

### Duplicate and conflicting candidates

The only genuine conflict is **two `Always` CRs, of either kind, wanting to place a different
candidate ahead of the original for the same image**. The total order still breaks the tie
deterministically, and both CRs get a `Warning` event `AmbiguousRewrite` — on the CRs, not on the
pods, since the pods are not where the ambiguity was configured.

A specific `OnFailure` `ImageAlternative` together with an `OnFailure` `ImageMirror` is **not** a
conflict: that is the intended composition, and it is why `excludeImagePrefixes` on the mirror stays
an optimisation (keeping a global mirror off repositories that already have upstream alternatives)
rather than a correctness requirement.

Deduplication is keyed on the resulting reference **and its resolved config**, so two CRs producing
the same reference with different `auth` are not collapsed into one candidate. That case gets its own
`Warning`, since it is almost always a configuration error.

### What an `ImageMirror` copies

The destination repository is keyed on the **original** reference, so one source image is one
destination repository whatever the alternatives say. A mirror matching a pod copies the image the
pod declared, not the alternatives it might be routed to: mirroring every alternative would multiply
the storage for a single image, and would make `status.repositories` and the cleanup GC depend on
routing decisions taken in the webhook.

When the original is unreachable at copy time and was never copied, the controller may pull the bytes
from any entry of an `ImageAlternative` covering that image and push them to that same destination —
the destination path stays keyed on the original either way. This keeps a mirror useful in the exact
scenario the alternatives exist for, at bounded storage cost. If no source answers at all, nothing is
copied and the image is counted in `status.images.missingSource`.

> [!IMPORTANT]
> Alternatives are asserted equivalent by the operator, not verified to be byte-identical. A
> destination populated from an alternative can therefore hold a digest that differs from the
> original upstream tag, and `driftPolicy: Warn` / `Sync` will report or resync it once the original
> registry is reachable again. `Sync` converges on the original upstream, which is an argument for
> revisiting the `driftPolicy` default.

### Who gets the credit

Exactly one CR is credited per container: the one that supplied the **winning** reference.

- `kuik.enix.io/rewritten-by` names that CR, and carries an entry only for containers that were
  actually rewritten
- `kuik.enix.io/reason` is `Always` or `OnFailure` according to that CR's own `rewritePolicy`, and
  `NoAlternatives` when every candidate failed

Status counters follow the same rule: a pod is counted **only by the winning CR**, the one that
provided the retained reference. Gauges are therefore disjoint between CRs and between kinds, so they
sum consistently instead of reporting the same pod several times.

- `pods.rewritten`, and the `activeFallbacks` entry that goes with it, are counted only by the CR
  named in `rewritten-by`
- `pods.noAlternatives` is counted by every resource that contributed at least one candidate, since
  all of them failed to help and there is no winner to attribute the pod to

> [!NOTE]
> Unsettled: [status v3](./status.md) defines `pods.tracked` as "number of pods this CR could apply
> to", which by construction overlaps between resources and so cannot be disjoint. Either `tracked`
> is exempt from the disjointness rule (it answers "would this CR apply?", useful for reviewing a
> selector before it ever wins), or it is redefined as "pods this CR owns" and the overlap becomes
> invisible. The other gauges are unaffected either way.

The status controllers read the original reference from `kuik.enix.io/original-images` when present
and from the live container image otherwise (a pod left untouched carries no annotation), so a
resource sees the same reference whether or not a rewrite happened.

## Annotations

Annotations added to pod by mutating webhook:

```yaml
metadata:
  annotations:
    # Same as KuiK v2, keep original images name referenced when we rewrite pod spec
    kuik.enix.io/original-images: |
      {"config-reloader":"quay.io/prometheus-operator/prometheus-config-reloader:v0.91.0","prometheus":"quay.io/prometheus/prometheus:v3.13.1-distroless","thanos-sidecar":"quay.io/thanos/thanos:v0.42.2"}'
    # CR that rewritten the image
    kuik.enix.io/rewritten-by: |
      {"config-reloader":"ImageMirror/prod-mirror","prometheus":"ImageAlternative/prometheus","thanos":"ImageAlternative/thanos"}
    # Reason of KuiK action (image rewrite) or inaction if original image isn't available
    #   OnFailure: Original image wasn't available, rewritten to first available alternative
    #   Always: Rewrite to first available alternative requested by rewritePolicy
    #   NoAlternatives: Could not find an available alternative for the image
    kuik.enix.io/reason:
      {"config-reloader":"NoAlternatives","prometheus":"Always","thanos":"OnFailure"}
```

## Global config

```yaml
webhook:
  availabilityCheck:
    timeout: 2s              # max time before considering a registry as unavailble
    # Cache per controller replica to avoid querying registry multiple time on burst
    # A single image used by 50 pods scheduled in a short period should result in 1 check, not 50
    activeCheckCache:
      ttl: 10s
    # Use image negative check result from ImageMirror and ImageMonitor to tests
    # other alternatives first to optimize
    skipHints:
      enabled: true
      maxAge: 30m
registries:
  default:
    check:
      method: HEAD   # HEAD (default) | GET
      interval: 10m
      maxPerInterval: 20
      timeout: 10s
    copy:
      interval: 1h
      maxPerInterval: 20
      timeout: 10s

  private-registry.tld:
    copy:
      interval: 10m
    # Auth used by KuiK ImageMonitor (and eventually ImageAlternative if not provided)
    # to check image availability if we don't have access to image pull secret
    # (if KuiK is configured without cluster-wide secret access for security)
    perPrefixFallbackAuth:
    - prefix: /project1
      secretRef:
        name: project1-creds
    - prefix: /project2
      secretRef:
        name: project2-creds

  123456.dkr.ecr.eu-west-3.amazonaws.com:
    perPrefixFallbackAuth:
    - prefix: /acme
      provider:
        name: aws
        serviceAccountRef:
          name: kuik-ecr-access

  docker.io:
    copy:
      interval: 1h
      maxPerInterval: 6
    perPrefixFallbackAuth:
    - prefix: /
      secretRef:
        name: dockerhub-creds

  public.ecr.aws:
    check:
      interval: 30m
```

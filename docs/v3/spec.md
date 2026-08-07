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
select the pods a resource applies to, and an empty selector matches everything. Two
exception are: `ImageMirror`'s `spec.excludeImagePrefixes`, which keeps specific images out of a
mirror (e.g. huge images) without changing which pods the mirror applies to, and every mirror
destination which are implicitly excluded from every mirror (see [Mirror loop prevention](#mirror-loop-prevention)).

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

  # Where this CR's entries sit relative to the original image, see "Candidate ordering"
  #   OnFailure: Default. Original first, these entries are fallbacks
  #   Always: These entries first (bypass quota, latency, network cost, …)
  rewritePolicy: OnFailure    # OnFailure | Always

  # Ordered list of equivalent images (or image subpaths) that could be used if one is
  # unavailable. All entries of a list must use the same form, see "Alternatives matching"
  # Every field besides `imagePrefix` is optional, and with public registries only
  # `imagePrefix` is usually needed
  alternatives:
  - imagePrefix: quay.io/acme/foo
  - imagePrefix: docker.io/acme-org/foo
  - imagePrefix: 123456.dkr.ecr.eu-west-3.amazonaws.com/repo/acme/foo
    auth:                          # credentials to pull images from the registry, see "Authentication"
      provider:
        name: aws
        serviceAccountRef:
          name: kuik-ecr-access
  - imagePrefix: "registry.local:5000/mirror/acme/foo"
    insecure: true                 # HTTP registry
    unavailable: true              # Image no longer available in this repository but if a pod
                                   # use this image, we'll try to substitute an alternative
    auth:
      secretRef:
        name: local-registry
      injectPullSecret: true       # Default: true (for secretRef), false for `provider`
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
another declaring `quay.io/acme/foo`. They are **not** mutually exclusive: overlapping CRs add
fallbacks instead of shadowing each other, and their lists are merged as described in
[Candidate ordering](#candidate-ordering). Specificity picks which *entry* of a CR matches, and
therefore the remainder to carry over — not which CR owns the image.

Because the semantics are structural rather than regex based, a lookup can walk a trie of path
segments and cost O(segments) whatever the number of CRs.

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
  # The `destination.path` of every ImageMirror is always excluded on top of this list,
  # see "Mirror loop prevention"
  excludeImagePrefixes:
  - ghcr.io/foo-bar/

  # Where the mirrored image sits relative to the original, see "Candidate ordering"
  #   OnFailure: Default. Last resort, behind the original and any alternatives
  #   Always: Ahead of everything (bypass quota, latency, network cost, …)
  #   None: Only copy image to mirror, never use it for routing (archiving, compliance, security scan, …)
  rewritePolicy: OnFailure    # OnFailure (default) | Always | None

  destination:
    path: registry.tld/mirror/
    insecure: false            # Default: false - Allow HTTP registry
    push:                      # Controller credentials to push images and delete unused tags
      auth:                    # see "Authentication" (`injectPullSecret` is ignored here)
        secretRef:
          name: mirror-write-credentials
    pull:                      # Credentials to pull the mirrored image, injected in namespaces
      auth:                    # see "Authentication"
        secretRef:
          name: mirror-read-credentials
        injectPullSecret: true

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

### Mirror loop prevention

A mirror never mirrors itself, and never mirrors another mirror: the `destination.path` of **every**
`ImageMirror` in the cluster is implicitly excluded from **every** `ImageMirror`, on top of each
mirror's own `excludeImagePrefixes`. The exclusion set is the union of those paths, recomputed as
mirrors are created and deleted, and it ignores selectors and `rewritePolicy` (a `None` mirror still
populates a destination, so its path still counts).

Two failure modes it closes:

- **self mirroring**: a pod referencing `registry.tld/mirror/docker.io/library/nginx:1.27` (a GitOps
  repository that committed back a rewritten image, or a hand written manifest) would get that image
  copied to `registry.tld/mirror/registry.tld/mirror/docker.io/library/nginx:1.27`, which is in turn
  a pod image the next round copies one level deeper, without bound
- **cross mirroring**: with two mirrors `A` and `B` matching the same pods, `A` copies what `B`
  produced and `B` copies what `A` produced, each copy handing the other a new, deeper reference to
  copy. Two mirrors are enough to fill both registries

Consequences worth stating:

- **cascading mirrors are not supported.** A deliberate "upstream → mirror A → mirror B" chain is
  indistinguishable from the loop above
- creating a mirror is retroactive: images another mirror had already copied below the new
  `destination.path` become excluded, hence no longer desired, and that other mirror's `cleanup` GC
  removes them after its `retention`
  mirror copies, never where it is allowed to write

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

## Authentication

`auth` is a discriminated union — exactly one of `secretRef` or `provider`, enforced at admission —
and the same schema everywhere credentials appear: `ImageAlternative` entries, `ImageMirror`
`destination.push` / `destination.pull`, and `registries.<host>.perPrefixFallbackAuth`.

```yaml
auth:
  secretRef:                # classic docker-registry secret, no namespace
    name: quay-pull
  injectPullSecret: true    # Default: true (for secretRef)
# --- or ---
auth:
  provider:                 # ambient cloud identity, same idea as Flux's `provider`
    name: aws               # https://fluxcd.io/flux/components/source/ocirepositories/#provider
    serviceAccountRef:      # optional
      name: mirror-pusher
  injectPullSecret: false   # Default: false (for provider)
```

### Secrets resolve in a single namespace

`secretRef` carries **no namespace**: it always resolves in kuik's install namespace (the *cluster
resource namespace*, configurable by an operator flag), exactly as cert-manager resolves a
`ClusterIssuer`'s secrets through `--cluster-resource-namespace`. Two reasons, the first decisive:

- **it closes a confused deputy hole.** The kinds are cluster-scoped, so with a free namespace anyone
  able to create one could name another team's secret (`namespace: team-b`) with
  `injectPullSecret: true` and have kuik copy it into a namespace they control — kuik would be a
  secret exfiltration channel. Resolving in one namespace means referencing a secret requires being
  able to write it in `kuik-system`, which closes the trust loop
- **uniform read RBAC**: every component needs only a `Role` in `kuik-system`, and nothing anywhere
  needs a cluster-wide `get secrets`

### `provider`

The enum is **closed** (`aws`, `gcp`, `azure`), with deliberately no `exec:` credential helper:
running arbitrary binaries from a minimal image is a security surface kuik does not want. Region and
project are derived from the registry hostname.

`serviceAccountRef` is optional and requests a token for that ServiceAccount, so a CR can carry its
own IAM role instead of borrowing the controller's global identity.

### `injectPullSecret`

Whether kuik copies a pull secret into the pod namespace, so the kubelet can pull the image this
`auth` protects. The defaults are asymmetric on purpose:

| `auth` form | Default | Why |
| ----------- | ------- | --- |
| `secretRef` | `true` | with a static secret, if the controller needs it to check the image then the kubelet needs it to pull |
| `provider` | `false` | the majority case is same-cloud, where the kubelet is already authorized natively |

Set to `true` with a `provider`, kuik materializes, **renews** and injects a docker-registry secret —
the cross-cloud case, and what makes 12h ECR tokens usable.

`ImageMirror`'s `destination.push` ignores the field entirely: push credentials are only ever used by
the controller.

### One credential per read, two for the mirror

An `ImageAlternative` entry has a single `auth` and no separate pull credential, because the
controller's availability check and the kubelet's pull are both **read** operations against the same
registry: one credential fits both and only whether to inject it differs, hence the boolean.
`ImageMirror` splits `push` and `pull` because there the two are privileges different in nature.

### No `auth` at all

kuik does nothing: no injection — the pod is expected to carry its own `imagePullSecrets`, or the
kubelet's credential provider handles it — **and checks are anonymous**.

> [!WARNING]
> A private image with neither `auth` nor a matching
> [`perPrefixFallbackAuth`](#global-config) therefore looks perpetually unavailable, even though the
> kubelet can pull it. A persistent anonymous 401/403 raises a `Warning` event pointing at that
> likely oversight.

## Candidate ordering

An image may be covered by several `ImageAlternative` **and** by one or more `ImageMirror`;
`ImageMonitor` never contributes a candidate. v2 ordered them with a signed `spec.priority` plus a
kind order (Original, CISM, ISM, CRIS, RIS). v3 has no `priority` and merges every match into one
list:

> **`Always` `ImageMirror`** → **`Always` `ImageAlternative`** → **original image** →
> **`OnFailure` `ImageAlternative`** → **`OnFailure` `ImageMirror`**, CRs sorted by name within each
> band

- the original appears **exactly once, at the pivot**, whatever its declared position in an
  `alternatives` list; a CR's other entries go to that CR's band in declared order. So `Always` on an
  `ImageAlternative` is never a no-op
- an `ImageMirror` contributes **one** candidate, `destination.path` joined with the full original
  reference (`registry.example.com/mirror/` + `docker.io/library/nginx:1.27`), and none at all under
  `rewritePolicy: None`, for an image matching its `excludeImagePrefixes`, or for an image already
  under some mirror's `destination.path` ([Mirror loop prevention](#mirror-loop-prevention))
- candidates are **deduplicated on (reference, resolved config), keeping the first occurrence**
- sorting by name, not by `creationTimestamp` (the tie-break Gateway API uses): a timestamp is not
  stable under GitOps, where deleting and recreating an object silently changes precedence, and
  `kubectl get imagealternatives` displays the name order for free

`Always` exists for latency and quota reasons, so an `Always` mirror has to beat a distant upstream
alternative; under `OnFailure` the upstreams are canonical and fresh, so the local copy sits behind
them as the ultimate safety net. Sorting by name only ever matters when two CRs of the same kind and
the same policy cover one image.

The four policy combinations, for one mirror candidate `M` and alternatives declared `ecr`, `gcr`,
`docker.io` where `docker.io` is the pod's original ([example 07](./examples/07-alternative-and-mirror-composition.yaml)):

| `ImageMirror` | `ImageAlternative` | Candidate order |
| ------------- | ------------------ | --------------- |
| `OnFailure` | `OnFailure` | `docker.io` → `ecr` → `gcr` → `M` |
| `OnFailure` | `Always` | `ecr` → `gcr` → `docker.io` → `M` |
| `Always` | `OnFailure` | `M` → `docker.io` → `ecr` → `gcr` |
| `Always` | `Always` | `M` → `ecr` → `gcr` → `docker.io` |

### Availability probing

Candidates are probed in list order with a manifest `HEAD` (or `GET`, per
[`registries.<host>.check.method`](#global-config)), concurrently but resolving to the **first
success in list order**, so worst case latency is one `availabilityCheck.timeout` rather than their
sum and a fast mirror never beats a healthy higher-priority entry. A probe returns a typed status
(`Available`, `NotFound`, `Unreachable`, `InvalidAuth`, `QuotaExceeded`).

Concurrent admissions for the same image collapse into a single registry call, and
`activeCheckCache` short-circuits the whole resolution for its TTL, so a 50 replica rollout costs
one resolution. `skipHints` additionally deprioritises candidates that `ImageMonitor` or
`ImageMirror` recorded unavailable less than `maxAge` ago.

The first candidate to answer `Available` is the retained reference. When none answers, the pod is
left untouched (it still starts if the image is in the node's cache).

### What an `ImageMirror` copies

A mirror copies the image the pod declared, to the single destination computed above — never one
destination per alternative, which make `status.repositories` and the cleanup GC depend on routing
decisions taken in the webhook. Images the pod declared that already live under a mirror destination
are not copied at all ([Mirror loop prevention](#mirror-loop-prevention)).

When the original is unreachable at copy time and was never copied, the controller may pull the bytes
from any `ImageAlternative` entry covering that image and push them to that same destination, which
keeps a mirror useful in the exact scenario the alternatives exist for. If no source answers, nothing
is copied and the image is counted in `status.images.missingSource`.

> [!IMPORTANT]
> Alternatives are asserted equivalent by the operator, not verified to be byte-identical, so a
> destination populated from an alternative can hold a digest that differs from the original upstream
> tag. `driftPolicy: Warn` / `Sync` surfaces it once the original registry is reachable again, and
> `Sync` converges on the original upstream — an argument for revisiting the `driftPolicy` default.

### Attribution

The CR that supplied the retained reference is the one named in
[`kuik.enix.io/rewritten-by`](#annotations), and the only one to count the pod in its status, so the
`pods` gauges are disjoint between CRs and between kinds and sum consistently. `pods.noAlternatives`
is the exception: no candidate won, so every CR that contributed one counts the pod.

Status controllers read the original reference from `kuik.enix.io/original-images`, falling back to
the live container image for pods that were never rewritten and therefore carry no annotation.

> [!NOTE]
> Unsettled: [status v3](./status.md) defines `pods.tracked` as "pods this CR could apply to", which
> overlaps between resources by construction and so cannot be disjoint. Either it is exempt from the
> rule above (it answers "would this CR apply?", useful for reviewing a selector before it ever
> wins), or it is redefined as "pods this CR owns". The other gauges are unaffected either way.

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
    # Auth used to check image availability when the CR provides none, by ImageMonitor and
    # ImageAlternative alike. KuiK never reads a pod's imagePullSecrets (no cluster-wide secret
    # access, see "Authentication"), so this is how it gets credentials for a private registry
    # nobody declared `auth` for. Same schema as `auth`, per image prefix
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

# spec v3

## Scope and filtering

The three custom resources (`ImageAlternative`, `ImageMirror`, `ImageMonitor`) are
**cluster-scoped**. There is no namespaced variant as in v2, restricting a resource to a subset of
the cluster is done with `namespaceSelector`, not by creating the object in a given namespace:

```yaml
namespaceSelector:
  matchLabels:
    kubernetes.io/metadata.name: monitoring
```

Filtering is expressed on **workloads**, not on images: `podSelector` and `namespaceSelector`
select the pods a resource applies to, and an empty selector matches everything. There are two
exceptions, both on `ImageMirror`: [`spec.excludeImages`](#excluding-images-from-a-mirror), which
keeps specific images out of a mirror (e.g. huge images) without changing which pods the mirror
applies to, and a mirror's own destination, implicitly excluded from it (see
[Mirror loop prevention](#mirror-loop-prevention)).

### What the webhook never rewrites

Three gates sit ahead of every resource. None carries any configuration, and no resource can opt
into or out of them. Two are **per container**:

- **a container whose current reference kuik produced itself** is never **rewritten** a second time.
  Rewriting it again would record kuik's own output as the origin and destroy the only way back to
  the real one
  ([Why the record lives on the pod](./observability.md#why-the-record-lives-on-the-pod))
- **a container with `imagePullPolicy: Never`** is told to use the node's cache and nothing else.
  Rewriting its reference could only turn a working pod into a failing one, whatever the candidate
  ordering says

The third is **per pod**:

- **a mirror pod**, one the kubelet mirrors into the API from a static manifest on the node, marked
  by the `kubernetes.io/config.mirror` annotation. The kubelet runs the file rather than the API
  object and rejects mutations to it, so a rewrite would change nothing that actually starts while
  making every status and every mirror account for an image no container is pulling

The first gate cannot fire on a pod the webhook has not already been through, and how kuik tells its
own output from anything else belongs to the admission path rather than to any resource:
[Reinvocation](./architecture.md#reinvocation).

One consequence of the first gate is worth stating here, because it reaches well past the webhook.
When another mutating webhook replaces the reference kuik placed, kuik does not write over it: it
stands down, and withdraws its own record with it. The container then reads, to every part of kuik
that *acts* on a pod — routing, mirroring, secret injection — like one kuik never touched: no
attribution, no origin to mirror, no injected pull secret
([What conceding removes](./architecture.md#what-conceding-removes)). What the concession itself
cost is still reported, attributed to the resource that made the rewrite. A mirror therefore treats it
like any other container it never touched, and copies the reference the pod now carries — the one
the other webhook chose — rather than the origin kept in `conceded-rewrites.from`.

Everything else is a matter for the selectors. v2's global `skipLabels` / `skipAnnotations` have no
v3 equivalent: excluding a workload is expressed on the resources that would otherwise apply to it,
so an exclusion is visible on the object that owns the decision rather than in the operator's
configuration. And v2's digest gate is gone outright — digest-pinned containers are routed like any
other image, see [Digest-pinned images](#digest-pinned-images).

## ImageAlternative

```yaml
apiVersion: kuik.enix.io/v1alpha1
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

  # Ordered list of equivalent repository (or repository groups) that could be used if one is
  # unavailable. All entries of a list must use the same form, see "Alternatives matching"
  # Every field besides `repository`/`repositoryGroup` is optional, and with public registries
  # only that one is usually needed
  alternatives:
  - repository: quay.io/acme/foo
  - repository: docker.io/acme-org/foo
  - repository: 123456.dkr.ecr.eu-west-3.amazonaws.com/repo/acme/foo
    auth:                          # credentials to pull images from the registry, see "Authentication"
      provider:
        name: aws
        serviceAccountRef:
          name: kuik-ecr-access
  - repository: "registry.local:5000/mirror/acme/foo"
    insecure: true                 # HTTP registry
    unavailable: true              # Image no longer available in this repository but if a pod
                                   # use this image, we'll try to substitute an alternative
    auth:
      secretRef:
        name: local-registry
      injectPullSecret: true       # Default: true (for secretRef), false for `provider`
```

> [!NOTE]
> `insecure: true` describes what **kuik** does: it probes and pulls that registry over HTTP. It says
> nothing to the kubelet, which refuses an HTTP registry unless the node's container runtime was
> configured for it (`insecure-registries` or its equivalent). Configuring the runtime is an operator
> prerequisite, here as much as on `ImageMirror`'s `destination.insecure`.

### Alternatives matching

An entry matches either a **single repository** (`repository`) or every repository **located
under a path, at any depth** (`repositoryGroup`). Exactly one of the two is set
per entry and neither is a glob: there is no implicit wildcard, and no glob
marker is supported.

| Field | Matches |
| ---- | ------- |
| `repository` | that exact repository only, whatever the tag or digest |
| `repositoryGroup` | every repository located under that path, whatever its depth |

A `repository` or `repositoryGroup` value is a prefix of the image reference at path segment
granularity, not a free form string prefix: `quay.io/acme/foo` matches `quay.io/acme/foo:v1` but
never `quay.io/acme/foo-bar:v1`.

When a `repository` matches, the tag or digest is always preserved, and for `repositoryGroup` the
matched remainder (every path segment below the group) is preserved too.

#### `repository`

```yaml
alternatives:
- repository: quay.io/acme/foo
- repository: docker.io/acme-org/foo
```

| Image | Result |
| ----- | ------ |
| `quay.io/acme/foo:latest` | matches, rewritten to `docker.io/acme-org/foo:latest` |
| `quay.io/acme/foo-bar:latest` | doesn't match (no prefix matching on a segment) |
| `quay.io/acme/foo/bar:latest` | doesn't match (deeper than the entry) |
| `quay.io/acme/foo/bar/oni:latest` | doesn't match |

#### `repositoryGroup`

```yaml
alternatives:
- repositoryGroup: quay.io/acme/foo
- repositoryGroup: docker.io/acme-org/foo
```

| Image | Result |
| ----- | ------ |
| `quay.io/acme/foo:latest` | doesn't match (the group itself is not a repository) |
| `quay.io/acme/foo-bar:latest` | doesn't match |
| `quay.io/acme/foo/bar:latest` | matches, rewritten to `docker.io/acme-org/foo/bar:latest` |
| `quay.io/acme/foo/bar/oni:latest` | matches, rewritten to `docker.io/acme-org/foo/bar/oni:latest` |

#### Invalid `alternatives`

Rejected at admission (validation webhook or CEL rules):

- an entry carrying both `repository` and `repositoryGroup`, or neither: exactly one of the two
  is required
- a `repository` or `repositoryGroup` carrying a tag or a digest (`quay.io/acme/foo:v1`,
  `quay.io/acme/foo@sha256:…`): alternatives describe repositories, the tag or digest comes
  from the pod
- mixing `repository` and `repositoryGroup` entries in the same list, since the two sides would
  not describe the same set of images:

  ```yaml
  alternatives:
  - repository: quay.io/acme/foo
  - repositoryGroup: docker.io/acme-org/foo   # invalid: mixed forms
  ```

Keeping the two forms explicit and non mixable keeps the CR readable and avoids rewriting an
image to an unrelated one because two repository names happen to share a prefix.

#### Overlapping alternatives across several CRs

Several `ImageAlternative` may match the same image, for instance one declaring
`repositoryGroup: quay.io/acme` and another declaring `repository: quay.io/acme/foo`. They are
**not** mutually exclusive: overlapping CRs add
fallbacks instead of shadowing each other, and their lists are merged as described in
[Candidate ordering](#candidate-ordering). Specificity picks which *entry* of a CR matches, and
therefore the remainder to carry over — not which CR owns the image.

Because the semantics are structural rather than regex based, a lookup can walk a trie of path
segments and cost O(segments) whatever the number of CRs.

### `unavailable`

An entry marked `unavailable: true` **still matches, and is never offered**. It takes part in
deciding whether the CR applies to an image, and in nothing else: it produces no candidate at
admission ([Candidate ordering](#candidate-ordering)), it is never read as a copy source by an
`ImageMirror` ([What an `ImageMirror` copies](#what-an-imagemirror-copies)), and
[`monitorAlternatives`](#imagemonitor) does not track it.

One case is different: an entry that names the **original image's own repository**. The original
stays a candidate, because the pod carries that reference and nothing removes it. But it is tried
**last** instead of at the pivot ([Candidate ordering](#candidate-ordering)), so the alternatives
that still work are tried first.

Matching is exactly what it is for. A repository that has been emptied or withdrawn is still the
reference the cluster's pods carry, and if nothing in the CR names it, nothing recognises those pods:
the CR does not apply, and none of its live entries is ever proposed. Declaring the dead source is
what buys the right to replace it — which is the whole point, since a pod on a source that no longer
answers is a pull error waiting for its next reschedule, and the alternative is right there in the
same object.

> [!NOTE]
> `unavailable: true` is a **declaration**, not an observation. It is the operator stating that a
> source is gone. What kuik observes on its own lands in
> [`ImageMonitor.status.unavailableAlternatives`](./status.md#imagemonitor), which reports entries
> that failed a check — entries kuik *was* willing to offer.

## ImageMirror

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ImageMirror
metadata:
  name: prod-mirror
spec:
  # Generic Kubernetes labels selector field to restrict pods where this CR apply
  # https://kubernetes.io/docs/reference/generated/kubernetes-api/latest/#labelselector-v1-meta
  # Empty/Nothing match all pod
  podSelector: {}
  namespaceSelector: {}
  # Globs matching images to keep out of this mirror (e.g. huge images), see "Excluding images
  # from a mirror". This mirror's own `destination.path` is excluded on top of this list,
  # see "Mirror loop prevention"
  excludeImages:
  - ghcr.io/foo-bar/**

  # Where the mirrored image sits relative to the original, see "Candidate ordering"
  #   OnFailure: Default. Last resort, behind the original and any alternatives
  #   Always: Ahead of everything (bypass quota, latency, network cost, …)
  #   None: Only copy image to mirror, never use it for routing (archiving, compliance, security scan, …)
  rewritePolicy: OnFailure    # OnFailure (default) | Always | None

  destination:
    path: registry.tld/mirror/
    insecure: false            # Default: false - Allow HTTP registry
    manage:                    # Controller credentials to read, write and delete tags at the
                               # destination: the pushes, the self-check HEADs, the tag listings
                               # the sweep reads and the deletions it issues
      auth:                    # see "Authentication" (`injectPullSecret` is ignored here)
        secretRef:
          name: mirror-write-credentials
    pull:                      # Credentials to pull the mirrored image, injected in namespaces
      auth:                    # see "Authentication"
        secretRef:
          name: mirror-read-credentials
        injectPullSecret: true

  cleanup:
    enabled: true              # Default: true - Delete image tag no longer referenced by any pod.
                               # With `false` nothing is deleted and `retention` has no effect: a
                               # reference leaves the desired state with its last pod, and the tag
                               # already written stays at the destination until someone removes it
    retention: 168h            # Default: 168h (7 days) - Image tag hold duration before deleting
                               # them, to deal with cronjob for instance
                               # Tags waiting out their retention are listed in
                               # `status.pendingDeletion`, see "Collecting unused tags"


  # Detect and reconcile image tag drift (digest change), e.g. tag `latest`
  #   Ignore: Image is copied once on destination registry and not updated if upstream tag digest change
  #   Warn: Periodically check if tag digest is still the same and warn if different
  #   Sync: Periodically check if tag digest is still the same and resync image in destination if different
  driftPolicy: Ignore          # Ignore (default) | Warn | Sync

  # v3.0 always copies every platform of a multi-platform image (the complete manifest index),
  # with no way to select a subset. Per-platform selection is deferred to a later version:
  #
  # platforms:
  #   mode: Auto                 # Auto (default): copy platforms retrieved from node labels
  #                              # All: copy every platform referenced for an image
  #                              # List: explicit list of platforms to copy
  #   #list: []                  # Only used with `mode: List`
```

### Excluding images from a mirror

Each entry of `excludeImages` is a **glob** matched against the normalized reference
(`nginx:1.27` is matched as `docker.io/library/nginx:1.27`), and matching any entry keeps the image
out of the mirror: no copy, no candidate, no tag in the destination.

A pattern splits on its last `:` when that `:` comes after its last `/`: what follows is a **tag
part**, what precedes is the **repository part**. A `:` sitting before the last `/` is a host port, so
`registry.local:5000/mirror/**` is a repository part alone. Each part is matched whole, against the
corresponding part of the reference:

| In a part | Matches |
| --------- | ------- |
| `*` | any characters inside one path segment |
| `**` | any characters, path separators included |

A pattern made of a repository part alone covers every tag and digest of the repositories it matches;
a tag part narrows it to the tags that part matches, and a digest-pinned reference is matched on its
repository part alone. Matching whole parts makes a pattern a description of the images it excludes
rather than a prefix of them:

| Pattern | Excludes |
| ------- | -------- |
| `ghcr.io/foo-bar/**` | everything under `ghcr.io/foo-bar/`, at any depth |
| `ghcr.io/foo-bar/*` | one level under it, `ghcr.io/foo-bar/app` but not `ghcr.io/foo-bar/team/app` |
| `docker.io/library/nginx` | that repository, whatever the tag or digest |
| `quay.io/acme/*-debug` | `quay.io/acme/foo-debug`, `quay.io/acme/bar-debug` |
| `**:latest` | every mutable `latest`, whatever the registry |

Where [`repository`/`repositoryGroup`](#alternatives-matching) are structural, an exclusion only
answers "do I mirror this?", so it stays a pattern and produces no reference.

### Collecting unused tags

With `cleanup.enabled`, every destination pass of an `ImageMirror`
([`mirror.destinationScan.interval`](#mirror-pacing-the-destination-kuik-owns)) starts by listing the
tags of each repository of `status.repositories` (`GET /v2/<repo>/tags/list`), keeps those carrying
this cluster's identity, and diffs them forward against the tags the desired state expects. A tag outside that set is
recorded in `status.pendingDeletion`, with `unusedSince` stamped at that moment, and deleted once
`cleanup.retention` has elapsed.

Reading the destination is what makes the collection self-healing: images that stop being used while
the controller is down are collected at the first pass after startup, and a
`status.pendingDeletion` lost with the object is rebuilt, each retention clock restarting from then.
Pod events feed the same list, so a reference losing its last pod while the controller runs is
noticed right away rather than at the next listing.

Part C of the [ImageMirror walkthrough](./walkthroughs/02-imagemirror-reconciliation.md) details the
deletion rules.

### Destination registry requirements

A mirror destination is an OCI registry the operator points kuik at, and kuik assumes it holds up its
end. `destination.manage` is the credential every one of those calls is made with, so it needs read
access as much as write: the self-check `HEAD`s and the sweep's tag listings are reads. Three
requirements apply to every `ImageMirror`, cleanup or not:

- **Conformance to the OCI Distribution spec** — `HEAD`/`GET` on manifests and blobs, `GET` on tag
  listings, `POST`/`PATCH`/`PUT` to upload a blob and `PUT` to write the manifest that closes it:
  kuik calls nothing else, and assumes the spec's guarantees on each hold
  (see [Availability probing](#availability-probing) and
  [walkthrough B.2](./walkthroughs/02-imagemirror-reconciliation.md#b2-ask-precisely-one-reference-at-a-time)).
- **Deep repository paths** — the destination reference is `destination.path` joined with the *full*
  original reference, hostname included
  ([walkthrough A.6](./walkthroughs/02-imagemirror-reconciliation.md#a6-compute-the-destination-reference)),
  so the registry has to accept arbitrarily nested repository paths
  (`registry.tld/mirror/quay.io/thanos/thanos`), not a flat or shallow namespace.
- **Rejecting an incomplete manifest** — a manifest `PUT` referencing blobs the registry does not
  hold must be refused, as the OCI Distribution spec suggest (spec say MAY, not MUST) and as
  registries do in practice. That is what makes a manifest's presence sufficient evidence that the
  image behind it is whole, so the self-check can settle a reference with a single `HEAD`
  ([walkthrough B.2](./walkthroughs/02-imagemirror-reconciliation.md#b2-ask-precisely-one-reference-at-a-time)).

With `cleanup.enabled: true`, two more requirements apply, because **kuik only ever deletes tags, never
manifests or blobs**:

- **Tag deletion** (`DELETE /v2/<name>/manifests/<tag>`, OCI 1.1) — what the cleanup sweep uses to
  retire a tag ([walkthrough C.3](./walkthroughs/02-imagemirror-reconciliation.md#c3-sweep-the-repositories)).
  kuik deletes **by tag only, never by digest**, and this is a deliberate choice, not a missing
  optimization: a manifest can carry tags from several clusters on a
  [shared destination](#multi-cluster-shared-destination-one-tag-per-cluster), and deleting it by digest
  would remove every one of them at once — cluster A's cleanup taking down cluster B's live tag as a
  side effect. Deleting by tag is the only form that stays confined to the tag a cluster actually owns,
  whatever the cost in registries that only support the coarser operation.
- **Registry-side garbage collection of untagged artifacts** — once a tag's last reference is deleted,
  the manifest is untagged but keeps occupying storage until the registry's own GC reclaims it
  ([walkthrough C.5](./walkthroughs/02-imagemirror-reconciliation.md#c5-leave-the-last-mile-to-the-registry)).
  kuik never touches it: the registry is the only party with the global view needed to tell whether
  another cluster, or a hand-pushed tag, still needs that content.

Only the tag-deletion requirement is something kuik can check itself, and only by trying: the OCI spec
has no capability negotiation for it, so a registry that rejects tag deletion is discovered the first
time a reconcile issues one and gets back a status that says it can't (typically `405 Method Not
Allowed`). When that happens, cleanup cannot make progress, and the `ImageMirror`'s `Ready` condition
flips to `False` with a reason naming the problem (see [`status.md`](./status.md#imagemirror)) instead
of retrying forever against a registry that will keep refusing. Registry-side GC of untagged artifacts
has no equivalent check — no OCI endpoint answers "will you reclaim this" — so it stays a prerequisite
the operator has to confirm against their registry's own documentation and configuration, not one kuik
can verify or enforce.

### Mirror loop prevention

Two rules keep mirroring bounded, and both are unconditional:

- a mirror copies the **origin** image, read from `kuik.enix.io/original-images` or, absent the
  annotation, from the pod spec — never a reference another mirror produced
- a mirror excludes its **own** `destination.path`, on top of its `excludeImages` and whatever its
  selectors say

The first rule is what makes two mirrors unable to feed each other: matching the same pods, `A` and
`B` both copy the origin image, so neither one ever sees the other's output as an input. The second
closes the only remaining case — a pod referencing this mirror's own output, from a GitOps repository
that committed back a rewritten image or from a hand-written manifest — which would otherwise be
copied one level deeper into itself, and again on the next round.

A pod referencing *another* mirror's destination is not supported (it confuses which reference is the
origin), and it terminates all the same: that mirror copies the reference it was given, one level deep,
and the mirror that produced it keeps working from the origin. Nothing recurses.

**Cascading mirrors are not expressible**, for the same reason: a deliberate "upstream → mirror A →
mirror B" chain would require `B` to source `A`'s copy, and every mirror sources the origin.

### Multi-cluster: shared destination, one tag per cluster

Several clusters mirroring to one registry want two things that pull in opposite directions: they want
to **share the transfer** (the first cluster to need an image pays for it, the others do not) and to
stay **autonomous** (whatever one cluster deletes, the others keep running).

Giving each cluster its own `destination.path` buys the autonomy and loses the sharing. A registry
deduplicates the *storage* of identical blobs, so the disk cost stays close to a single copy, but every
cluster still **uploads** every layer: blobs are linked per repository in OCI Distribution, so another
repository means another upload. Sharing the *tag* along with the path loses the autonomy instead,
since cluster A's `cleanup` then deletes a tag cluster B is actively routing to.

v3 keeps both by sharing the repository and splitting the tag. Every cluster writes to the same
repository, derived from `destination.path` exactly as in the mono-cluster case, and appends its own
identity to the tag:

```text
registry.tld/mirror/quay.io/thanos/thanos:v0.42.2_cluster-a   # written by cluster A
registry.tld/mirror/quay.io/thanos/thanos:v0.42.2_cluster-b   # written by cluster B
```

Two properties follow, and together they are the point of the design:

- **the second cluster uploads nothing.** A copy starts by `HEAD`ing the manifest **by digest** in the
  target repository ([walkthrough A.8](./walkthroughs/02-imagemirror-reconciliation.md#a8-record-the-repository-choose-the-source-then-push)):
  present already means one tag `PUT` of a few kilobytes, absent means a normal copy, and kuik never
  has to know which cluster copied what
- **the `ImageMirror` object is identical on every cluster.** The identity comes from the operator's
  [`clusterID`](#global-config) and never from the CR: no templating, no per-cluster overlay, no
  dimension of the spec that exists only in multi-cluster setups

Both tags point at the **same manifest**, and there is deliberately no shared canonical tag, so nobody
has to own or repair one.

v3.0 copies every platform of a multi-platform image whole (see the note in
[ImageMirror](#imagemirror)), so the pushed manifest is always the upstream's own index, verbatim,
and its digest is known **before** anything is transferred — it is the digest already published
upstream, not one kuik computes. The copy is therefore always shared between clusters, whatever
their node pools.

Blobs and per-platform child manifests are shared in every case, so the second cluster's `HEAD`
finds the manifest already at the target repository, and its own tag costs one `PUT` of a few
kilobytes, never a re-transfer.

Per-platform selection will change this: a filtered index
has a digest of its own, so two clusters with different node pools could end up pushing different
indices under the same repository.

**Each cluster owns its tags and nothing else.** Creating, verifying and deleting are all restricted to
the tags carrying its own suffix: the self-check re-`PUT`s one of its tags that went missing, with no
blob transferred
([walkthrough B.4](./walkthroughs/02-imagemirror-reconciliation.md#b4-handle-each-divergence)), and the
cleanup filters a repository's tag listing on the suffix before diffing, which makes another cluster's
tags invisible to it
([walkthrough C.3](./walkthroughs/02-imagemirror-reconciliation.md#c3-sweep-the-repositories)). There is
no shared mutable state, hence no cross-cluster race and no coordination protocol. A cluster that dies
blocks nothing: its tags keep its manifests alive without stopping anyone else from managing theirs,
which is a retention choice rather than a deadlock, and `status.repositories` stays a local view — a
repository can be listed on A while B has already emptied it.

**Reclaiming space is delegated to the registry.** As long as any cluster tag points at a manifest,
that manifest is uncollectable; when the last one disappears it becomes untagged, and the registry's
native untagged GC reclaims the space
([walkthrough C.5](./walkthroughs/02-imagemirror-reconciliation.md#c5-leave-the-last-mile-to-the-registry)).
kuik does not attempt a cross-cluster refcount.

**Digest-pinned references are unaffected on the routing side.** A `@sha256:` reference is content
addressed, so the mirror candidate keeps the digest verbatim and is identical on every cluster; the
suffix is carried by the tags kuik pushes alongside it, the origin-derived one and the anchor of
[Digest-pinned images](#the-anchor-tag).

**Upstream quotas are what clusters share involuntarily.** Every cluster paces its own reads of the
source registries, so whatever the registry uses to identify the caller (a shared credential, or a
shared egress IP for anonymous pulls) hands that account or IP the sum of their rates, see
[Quotas count per identity](#quotas-count-per-identity-clusters-pace-independently).

#### Tag naming constraints

- OCI tags are limited to **128 characters**, and to `[a-zA-Z0-9._-]` after the first character.
  `clusterID` is validated against `^[a-zA-Z0-9][a-zA-Z0-9.-]*$` — that alphabet **minus `_`**,
  which is the separator — and is expected to be short. Allowing `_` would make the suffix
  ambiguous: clusters `a` and `1_a` would both claim the tag `v1_1_a`
- a reference whose suffixed tag would exceed 128 characters is truncated deterministically and a
  hash of the full upstream tag is appended, giving **`<truncated>-<hash>_<clusterID>`**. The suffix
  stays the **last** segment — which is what the cleanup sweep filters on — and the mapping stays
  injective for the endless tags CI systems produce
- the separator is `_`, and appending it is **unambiguous**: exactly one trailing `_<clusterID>` is
  added, so an upstream tag literally ending in `_cluster-a` is copied by cluster A to
  `…_cluster-a_cluster-a` rather than colliding with itself. What the suffix does *not* buy is a way
  back: for a tag short enough to survive whole, stripping that one suffix happens to recover the
  upstream tag, but a truncated-and-hashed tag recovers nothing, so **the mapping is one-way and
  nothing in kuik reads it backwards** — every loop computes destination tags forward from the desired
  state instead ([walkthrough 02,
  invariants](./walkthroughs/02-imagemirror-reconciliation.md#cross-cutting-invariants)). The residual
  hazard is a *foreign* writer pushing a tag ending in `_<clusterID>` under `destination.path`, which
  cleanup would take for its own: a mirror destination is expected to be kuik's alone

#### `clusterID` is required, and changing it orphans tags

`clusterID` is **required**, in a single-cluster install as much as in a shared destination. Every
tag an `ImageMirror` writes carries it, and a cluster only ever creates, verifies and deletes the
tags carrying its own suffix.

> [!WARNING]
> **Changing `clusterID` orphans every tag written under the previous one.** Those tags stop
> matching the sweep's filter, so they never enter `status.pendingDeletion` and `cleanup` never
> deletes them: they stay at the destination until someone removes them by hand. kuik keeps no
> record of previous identities.

## ImageMonitor

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ImageMonitor
metadata:
  name: cluster-images
spec:
  # Generic Kubernetes labels selector field to restrict pods where this CR apply
  # https://kubernetes.io/docs/reference/generated/kubernetes-api/latest/#labelselector-v1-meta
  # Empty/Nothing match all pod
  podSelector: {}
  namespaceSelector: {}

  unusedImageRetention: 168h   # Default: 168h (7 days) - keep monitoring for a given time after no
                               # longer used in cluster, useful for cronjob

  driftDetection: true         # Default: true - Detect if an image tag digest differ from pod running in cluster

  monitorAlternatives: false   # Default: false - Also monitor the alternatives kuik would offer for
                               # each tracked image, from ImageAlternative entries only: a mirror
                               # destination is verified by the mirror's own self-check. Entries
                               # marked `unavailable: true` are never tracked

```

What an `ImageMonitor` tracks is the **origin** reference of every container of every pod its
`podSelector` and `namespaceSelector` select — read from
[`kuik.enix.io/original-images`](./observability.md#annotations) for pods the webhook already
rewrote, per [Attribution](#attribution). A monitor therefore never sees a mirror's reference in
place of the origin it replaced, whatever any routing resource did to the pod.

`monitorAlternatives: true` adds, for each of those images, **every candidate an `ImageAlternative`
would offer for it**. A monitor holds no list of its own: it follows what the webhook would propose,
which is why the [status](./status.md#imagemonitor) reports the resource each alternative came from.
Entries marked [`unavailable: true`](#unavailable) are excluded, being references nothing will ever
be routed to.

**An `ImageMirror` destination is not among them.** A mirror already verifies every reference it is
meant to hold, on its own pass over the destination
([walkthrough 02, B.2](./walkthroughs/02-imagemirror-reconciliation.md#b2-ask-precisely-one-reference-at-a-time)),
and reports the outcome in its own status. Tracking it from a monitor would `HEAD` the same
references a second time, and would put a destination in `status.checks.registries` — which
[Scheduling](#scheduling) says never happens.

Drift is checked per tracked **tag**, not per pod: pods referencing the same tag can have pulled it at
different times, so more than one digest can be running for that tag at once (skew). `status.driftedImages`
records that breakdown — one entry per drifted ref, with a `runningDigests` list of every digest currently
seen and how many pods reference each — against the single `upstreamDigest` the last check observed (see
[status](./status.md#imagemonitor)).

## Scheduling

Checks and copies run on **windows** counted from the start of the controller process, at a rate of at
most one image per window. A process started at 13:32 with `interval: 5m` opens its windows at 13:37,
13:42, 13:47 and so on, and each opening takes one image: the next of a monitoring ring, or the next of
a mirror's copy queue.

The phase is **fixed** — a window opens when `now` reaches `start + k × interval` — so the work done
inside a window never moves the following ones. An opening that finds nothing to do, or that comes
while the previous image is still being transferred, is lost rather than banked, which makes the
rate a ceiling: a host is asked for at most one image per
`interval`, whatever happens. Re-arming the clock on each image instead would make the real period
`interval` plus the time that image took, stretching a ring's lap by the latency of every image in it.

The first window of a process is a full `interval` away, and that extends the ceiling across restarts: a
controller whose lifetime stays under `interval` sends nothing at all, so a crash loop shows up as a
ring that stops turning.

A [config reload](#global-config) is the one thing that moves a phase, and it moves only the hosts it
touches: a host whose `check` or `copy` settings changed **restarts its series from the reload**,
first window a full (new) `interval` later, exactly as a process start does for every host. The
ceiling therefore holds across a reload — editing the ConfigMap in a loop cannot burst a registry,
since each edit pushes the next window further away rather than nearer. Hosts whose settings did not
move keep their phase, and **no ring cursor moves in any case**: a reload changes the pace, never
the position.

**Checks** are paced by [`registries.<host>.check.interval`](#global-config). Every `ImageMonitor`
holds a ring of the images it tracks on a host, in lexicographic order, and each window of that host
takes the next image of one of them, so an image comes back once per lap — the `cycleDuration`
reported in [status](./status.md#imagemonitor). A monitor alone on a host with 60 tracked images and
`interval: 1m` laps in an hour; lowering `interval` re-checks each image sooner and sends that host
more requests. Drift detection (`driftDetection: true`) reads the same manifest on the same windows,
and an `ImageMirror` takes a window of its **source** host when `driftPolicy` is `Warn` or `Sync`, to
re-read the upstream tag — a paced read like any other, so it holds a ring of its own there and
reports its cursor and lap in [status](./status.md#imagemirror) exactly as a monitor does.

**Copies** are paced by [`registries.<host>.copy.interval`](#global-config), on windows of their own.
Where checks cycle a ring, copies drain a queue: the images the mirrors still owe (`images.desired`
minus `images.copied` in status, plus what `driftPolicy: Sync` queues again once a check reports
drift). A source kept slow lengthens the drain rather than bursting.

Windows pace what kuik **pulls from**, and nothing else. A quota is what an upstream enforces on
reads, while a mirror destination is the operator's own registry: neither the pushes nor the
self-check `HEAD`s that verify them wait for a window, and an `ImageMirror` holds no place in any ring
for its destination ([walkthrough 02, B.3](./walkthroughs/02-imagemirror-reconciliation.md)). Its
destination is compared to its desired state whole, once per
[`mirror.destinationScan.interval`](#mirror-pacing-the-destination-kuik-owns) — a period between
passes rather than a rate between requests — with nothing to resume and no cycle to report.

### One budget per host, one ring per resource

Windows belong to the **registry host**, since protecting its quota is what they are for: every
`ImageMonitor` tracking images there draws from the same series of check windows, as does an
`ImageMirror` re-reading an upstream tag under `driftPolicy: Warn` / `Sync`, and every mirror pulling
from that host shares its copy windows.

The **ring** and its `cursor` belong to a **(resource, host)** pair. A monitor tracking images on
`docker.io` and `quay.io` holds one ring per host, two monitors on `docker.io` hold one each, and each
cursor lives in the status of the resource that owns it. A cursor is the reference last checked, not a
position, so a restart resumes at its successor in lexicographic order even when that reference is
gone.

An image tracked by two monitors sits in both rings, and the second ring to reach it **reuses the
verdict the first one obtained** instead of spending a window. That cache is in memory and losing it
costs one redundant `HEAD`, so overlapping monitors cost a host what their union costs rather than the
sum of their rings.

`cycleDuration` is therefore the lap **actually measured**, from `cycleStarted` to the cursor returning
where it started, rather than a value derived from the ring's size: two monitors covering 1000 images
each, disjoint, on `interval: 1m` lap in 2000 windows and not 1000, while the same two monitors
covering the *same* 1000 images lap in 1000, the second one carried by the verdict cache. Ring size
times the host's `interval` is the lap of a resource alone on its host, hence the best case. Measuring
folds in the sharing, restarts and a growing ring alike, which is what makes the guarantee the status
publishes — every tracked image checked once per `cycleDuration` — hold.

A copy queue needs no position of any kind: drained rather than cycled, it holds no cursor and
reports no lap.

Rings, queues and the windows they draw from all live in the leader-elected reconciler, which is why
a single budget per host is enforceable at all — see [architecture v3](./architecture.md).

### Quotas count per identity, clusters pace independently

An `interval` bounds what **one** controller sends to a host. A registry quota is attached to
whatever the registry uses to identify the caller, and which one applies depends on how the request
is made:

- **anonymous pulls** are usually counted against the **source IP**, and are the more common way
  this bites: Docker Hub's pull limit is the canonical example, and any two clusters egressing
  through the same NAT gateway, corporate proxy, or cloud NAT already share that quota, with no
  credential involved at all
- **authenticated pulls** are usually counted against the **account the credential belongs to**, so
  the same collision happens the moment two clusters are configured to reuse the same `auth`
  (`secretRef` or `provider`) against a host

Either way, `interval` and the quota are counted on different things as soon as several clusters
share the identity behind them: each cluster paces itself to one request per `interval`, and the
account or IP sees the sum.

Windows are counted per process ([Scheduling](#scheduling)), so they stay independent across clusters:
three clusters on `interval: 1m` may hit the host within the same second, three times per minute.

> [!WARNING]
> Sizing `check.interval` and `copy.interval` for a single cluster and then sharing the identity that
> counts against the quota (the same credential and/or the same egress IP) across `N` clusters
> consumes `N` times the intended rate. Either multiply the intervals of that host by `N`, or give
> each cluster its own credential or egress path so each gets its own quota. kuik sees one cluster
> only and cannot detect the aggregate, so this is a configuration prerequisite. For shared egress
> IP this is anyway a known limitation with or without kuik.

The symptoms are worth recognising, because one of them is misleading: a `QuotaExceeded` on a
**check** looks like an unavailable image, and an `ImageAlternative` reacts to it by routing pods
away from a healthy registry; on a **copy** it lands in `status.failedImageCopies` with reason
`QuotaExceeded`, which is explicit. A shared destination is unaffected: the deduplication of
[Multi-cluster](#multi-cluster-shared-destination-one-tag-per-cluster) leaves the second cluster
nothing to transfer.

## Authentication

`auth` is a discriminated union — exactly one of `secretRef` or `provider`, enforced at admission —
and the same schema everywhere credentials appear **on a custom resource**: `ImageAlternative`
entries, and `ImageMirror`'s `destination.manage` / `destination.pull`.

[`fallbackAuth`](#fallback-credentials) deliberately does not use it. Its entries carry `secretRef`
or `provider` directly, with no `auth` wrapper, because they serve kuik's own reads and are never
injected into a namespace — so the one field `auth` adds beyond the union, `injectPullSecret`, would
have nothing to mean there.

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

The enum is **closed** (`aws`, `gcp`, `azure`).

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
the cross-cloud case, and what makes cloud-provider registries short lived tokens usable.

**Where the Secret lands depends on `rewritePolicy`, and under `Always` it does not wait for a pod.**
Under `OnFailure` a rewrite only happens when an origin fails, so the need is discovered: the Secret
appears in a namespace once a pod there has actually been rewritten. Under `Always` the *need* is
known in advance — the resource's candidates are probed ahead of the original for every matched pod
— so the Secret is materialized in **every namespace the `namespaceSelector` selects**, pod or no
pod. An absent or empty
`namespaceSelector` selects **every namespace in the cluster**, and the Secret is synchronized in all
of them accordingly.

`podSelector` does not narrow this. It decides which pods are rewritten, not where credentials are
provisioned, so a cluster-wide `Always` resource that matches three pods still populates every
namespace. That is the price of `Always` having no first-pull race
([walkthrough 03, A.7](./walkthroughs/03-secret-syncer-reconciliation.md#a7-the-first-pull-race-onfailure-only)),
and a `namespaceSelector` is what bounds it.

`ImageMirror`'s `destination.manage` ignores the field entirely: those credentials are only ever
used by the controller. [`fallbackAuth`](#fallback-credentials) has nothing to ignore — it carries no `auth`
block at all ([Authentication](#authentication)) — and could not usefully have one: the
injected Secret is named after the identity of the resource that asked for it
([the name of an injected Secret](./architecture.md#the-name-of-an-injected-secret)), and a global
fallback credential belongs to no resource. It serves the controllers' own reads; injection is
declared on the CR that routes the image.

### One credential per read, two for the mirror

An `ImageAlternative` entry has a single `auth` and no separate pull credential, because the
controller's availability check and the kubelet's pull are both **read** operations against the same
registry: one credential fits both and only whether to inject it differs, hence the boolean.
`ImageMirror` splits `manage` and `pull` because there the two are privileges different in nature:
`pull` is the read the kubelet needs, `manage` is what kuik itself does to the destination — read,
write, and delete under `cleanup.enabled`.

### No `auth` at all

kuik injects nothing: the pod is expected to carry its own `imagePullSecrets`, or the kubelet's
credential provider handles it.

What kuik itself reads an image with follows **one order**:

1. the entry's own **`auth`**, when it declares one
2. the **pod's `imagePullSecrets`**
3. the matching **[`fallbackAuth`](#fallback-credentials)** entry, most specific first
4. **anonymous**

**The webhook stops at step 2.** Its active check probes only with credentials the kubelet will also
have, because its verdict is a prediction of the pull: an entry's `auth` is either injected or backed
by a node identity, and a pod's own `imagePullSecrets` are by definition what the node holds.
`fallbackAuth` is neither — it is never injected ([`injectPullSecret`](#injectpullsecret)) — so a
candidate answering only thanks to it would pass admission and then fail to pull. The reconciler's
background checks and copies use all four steps.

**The order is the same in every mode**; what
[`secretAccess.mode`](./architecture.md#two-modes-one-clusterrole-apart) decides is whether step 2
can succeed. Under `permissive` kuik may read those Secrets. Under `restricted` the API server
refuses it, except in the namespaces `secretAccess.namespaces` grants access to. A refusal is not an
error to report: kuik takes it as "no credential here" and moves on to step 3, exactly as it does
for a pod that declares no `imagePullSecrets` at all.

**Everything in these documents is written for `restricted`**, where step 2 usually yields nothing:
a check with no declared credential and no `fallbackAuth` match is anonymous. `permissive` — which
the chart installs by default, purely so that a fresh install works against a private registry with
nothing declared — only makes that step productive, and a check that finds nothing there degrades to
exactly the `restricted` case.

This order decides what kuik reads with, and nothing else. Whether a credential is also copied into
a pod's namespace is a separate question, answered per entry by
[`injectPullSecret`](#injectpullsecret).

> [!WARNING]
> A private image with neither `auth` nor a matching
> [`fallbackAuth`](#fallback-credentials) therefore looks perpetually unavailable under
> `restricted`, even though the kubelet can pull it — and under `permissive` its availability
> silently depends on a Secret nobody declared to kuik. A persistent anonymous 401/403 raises a
> `Warning` event pointing at that likely oversight. Declaring the credential once is what makes
> the check independent of the mode.

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
- an original matching an entry marked [`unavailable: true`](#unavailable) is **demoted to the end of
  the list** instead of sitting at the pivot. The operator has declared that source gone, so probing
  it first would spend an admission on a repository known to be dead. It is demoted, never dropped
- an `ImageMirror` contributes **one** candidate, `destination.path` joined with the full original
  reference, the tag carrying this cluster's identity
  (`registry.example.com/mirror/` + `docker.io/library/nginx:1.27` + `_cluster-a`, see
  [Multi-cluster](#multi-cluster-shared-destination-one-tag-per-cluster)), and none at all under
  `rewritePolicy: None`, for an image matching its `excludeImages`, or for an image already
  under its own `destination.path` ([Mirror loop prevention](#mirror-loop-prevention))
- an `ImageAlternative` entry marked [`unavailable: true`](#unavailable) contributes **no**
  candidate; it only ever served to match the image
- candidates are **deduplicated on (reference, resolved config), keeping the first occurrence**
- CRs of the same kind and policy are sorted **by name**, so overlapping configurations produce a
  deterministic candidate order

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

### `imagePullPolicy: Always` demotes a mirror

A container with `imagePullPolicy: Always` asks the runtime to re-resolve its tag on every start,
which is how a mutable tag is followed. An `ImageMirror` cannot promise that: its copy is a snapshot
of what was running when it was made, and it follows the upstream only under
[`driftPolicy: Sync`](#imagemirror), and then only as far as the last resync got. Serving such a
container from the mirror answers "give me the newest" with "here is the one I have".

So for that container, and only for it, an `ImageMirror`'s `rewritePolicy: Always` is **ignored**:
its candidate goes to the `OnFailure` mirror band at the end of the list, where it is still the
safety net if nothing upstream answers. `ImageAlternative` entries are unaffected — an alternative
is an upstream, and resolves the tag exactly as the original would.

[`webhook.demoteMirrorWithPullPolicyAlways: false`](#global-config) turns this off. The case it
exists for is the `AlwaysPullImages` admission plugin, which sets `imagePullPolicy: Always` on every
container in the cluster: the demotion would then apply everywhere and defeat what the mirror is
usually there for, which is keeping the cluster off a rate-limited upstream.

### Availability probing

Candidates are probed **sequentially, in list order**, with a manifest `HEAD` bounded by
`availabilityCheck.timeout`, so a fast mirror never beats a healthy higher-priority entry. A probe
answers either `Available` or one of the check reasons of the
[shared vocabulary](./observability.md#reasons) (`ManifestNotFound`, `Unauthorized`,
`QuotaExceeded`, `Unreachable`).

**Sequential describes one image's candidate list.** A pod is resolved as its set of **distinct
images**, probed concurrently: an image two containers share is resolved once, and the admission
costs the slowest image rather than the sum of them.

**An exhausted quota drops the candidate, however the registry says so.** A registry out of quota
for kuik's identity either refuses the request — a `429`, reported as `QuotaExceeded` — or answers
the `HEAD` normally and says so in its rate-limit headers (`ratelimit-remaining` and its
neighbours). Both are read as a failure: retaining a candidate kuik can still reach but the node
cannot leaves the pod in `ImagePullBackOff` when the runtime pulls it, which is worse than moving to
the next candidate.

`HEAD /v2/<name>/manifests/<reference>` is what every check uses, everywhere and without a knob:
the OCI Distribution spec mandates it, it is the cheapest request that answers the question, and a
registry answering it wrongly is a registry to fix rather than a case to configure around.

Concurrent admissions for the same image collapse into a single registry call, and
`activeCheckCache` short-circuits the whole resolution for its TTL, so a 50 replica rollout costs
one resolution. [`demoteKnownFailures`](#demoteknownfailures-reusing-what-the-loops-already-know)
additionally sends candidates an `ImageMonitor` or an `ImageMirror` reports failing to the end of
the list, where they are still probed if everything above them fails.

The first candidate to answer `Available` is the retained reference. When none answers, the pod is
left untouched (it still starts if the image is in the node's cache).

### Digest-pinned images

v2's mutating webhook skipped any container pinned by digest (`nginx@sha256:…`) outright. **v3
routes them like any other image**: matching, ordering and probing are unchanged, and the digest is
carried over to the candidate exactly as a tag is
([Alternatives matching](#alternatives-matching)).

What makes this safe is that a digest is content-addressed and repository-independent: it is
computed over the manifest bytes, not over the reference, so copying an image to another registry
preserves it. `registry.tld/mirror/docker.io/library/nginx@sha256:ab…` is therefore either the exact
same bytes as `docker.io/library/nginx@sha256:ab…`, or it does not exist at all. A candidate holding
a *different* image simply does not have that digest, the probe answers `ManifestNotFound` and the
candidate is dropped. Unlike a tag, there is no way for a pinned reference to silently resolve to different
content.

Two consequences on the mirror side:

- a digest-pinned image is copied as the **complete manifest index**. This stays true once
  per-platform selection ships: a filtered index has a digest of its own, so the copy would be
  unreachable by the very reference the pod declared. Platform selection and digest pinning will
  be mutually exclusive on a given image, and pinning wins
- when the original is unreachable and the controller sources the bytes from an `ImageAlternative`
  entry instead ([What an `ImageMirror` copies](#what-an-imagemirror-copies)), a digest-pinned image
  is the one case where "equivalent" is *verified* rather than asserted: fetching by digest either
  returns those exact bytes or fails, so the caveat below about a destination diverging from the
  original does not apply

`driftPolicy` and `ImageMonitor`'s `driftDetection` are tag concepts and ignore pinned references: a
digest cannot drift, so such images are never counted in `drifted`. A pinned reference is a
desired-state entry **keyed by its digest**, with a retention clock of its own, independent of what any
tag points at.

#### The anchor tag

A copy is always tagged, a pinned reference included, and the tag comes from the origin exactly as for
any other image: `registry.tld/mirror/docker.io/library/nginx:1.27_cluster-a@sha256:ab…` is a valid
reference, the runtime resolves it by digest and treats the tag as informational. That tag is what makes
the manifest reachable to a human and what the desired state expects, and the pod keeps pulling exactly
the bytes it pinned.

On top of it, a pinned reference gets an **anchor tag** derived from its digest,
`sha256-<digest>_<clusterID>` (the cosign convention), pushed at copy time. It is never a routing
reference — the pinned digest is — and it exists to keep that **digest** tagged whatever happens to the
other tag:

- `driftPolicy: Sync` repoints the origin-derived tag onto the upstream's new digest. Without an anchor,
  the digest a pod is pinning right now becomes untagged, and the registry's untagged-manifest GC
  reclaims it out from under that pod — kuik would not be deleting the safety net, the registry would,
  silently
- a reference pinned with no tag at all (`repo@sha256:D`) has nothing else to carry it

Nothing about it is conditional — not on `driftPolicy`, not on history, not on what a tag currently
points at — so the copy path has no state to consult and no ordering to get right. The mirror's
[`cleanup`](#imagemirror) deletes an anchor when its digest leaves the desired state and its retention
elapses, which is also what makes it carry the `_<clusterID>` suffix in a shared destination
([Multi-cluster](#multi-cluster-shared-destination-one-tag-per-cluster)). The exact mapping from a
reference to the tags pushed for it lives in
[walkthrough A.6](./walkthroughs/02-imagemirror-reconciliation.md#a6-compute-the-destination-reference).

### What an `ImageMirror` copies

A mirror copies the **origin** image — `kuik.enix.io/original-images` when the pod carries it, the pod
spec otherwise — to the single destination computed above, never one destination per alternative, which
would make `status.repositories` and the cleanup GC depend on routing decisions taken in the webhook.
An origin image already living under this mirror's own `destination.path` is not copied at all
([Mirror loop prevention](#mirror-loop-prevention)).

When the origin is unreachable at copy time, the controller may pull the bytes from any
`ImageAlternative` entry covering that image, skipping the entries marked
[`unavailable: true`](#unavailable), and push them to that same destination — a first copy and a
re-copy alike, so an image whose origin registry
disappeared for good stays re-copyable, which is the scenario alternatives exist for. An origin that
itself matches an entry marked [`unavailable: true`](#unavailable) is tried **last** among the
sources rather than first, exactly as it is demoted to the end of the candidate list at admission
([Candidate ordering](#candidate-ordering)): opening with it would spend a window of its host on a
repository the operator has declared dead. The destination is
the one derived from the origin in every case, never from the source actually read
([walkthrough A.8](./walkthroughs/02-imagemirror-reconciliation.md#a8-record-the-repository-choose-the-source-then-push)).
If no source answers, nothing is copied: the image gets a `status.failedImageCopies` entry with
reason `SourceNotFound`, which is what names it, and is counted in `status.images.missingSource`.

> [!IMPORTANT]
> Alternatives are asserted equivalent by the operator, not verified to be byte-identical, so a
> destination populated from an alternative can hold a digest that differs from the original upstream
> tag. `driftPolicy: Warn` / `Sync` surfaces it once the original registry is reachable again, and
> `Sync` converges on the original upstream — an argument for revisiting the `driftPolicy` default.
> This only concerns tags: a [digest-pinned image](#digest-pinned-images) fetched from an alternative
> is byte-identical by construction.

### Attribution

The `pods` gauges of [status v3](./status.md) all count **pods**, and none of them sums across CRs.
What is attributed sits one level down: **exactly one CR counts each rewritten or conceded
container**. A pod has many containers, so two CRs serving two of them both count that pod, and one
CR may count the same pod in `rewritten` and in `conceded` at once.

Where a count comes from still differs, and telling the two apart is what makes them readable:

- `pods.rewritten` and `pods.conceded` come off an attribution the webhook left on the pod, and from
  two different annotations: `pods.rewritten` from
  [`kuik.enix.io/rewritten-by`](./observability.md#annotations), `pods.conceded` from
  `conceded-rewrites.by` — a conceded container leaves `rewritten-by` altogether
  ([what conceding removes](./architecture.md#what-conceding-removes))
- `pods.tracked` and `pods.noAlternatives` carry no attribution: the first counts what the selectors
  retain, the second every CR that offered a candidate

`pods.tracked` counts the pods a CR's `podSelector` and `namespaceSelector` select. The overlap is
deliberate: it answers "does this CR watch this pod?". It is emphatically not a claim of ownership —
what a CR *did* is what `rewritten` reports, and the annotation is what settles it. It stays the
denominator the other three are read against **inside one CR**; only the sum across CRs breaks.

`pods.noAlternatives` overlaps for a different reason: no candidate won, so every CR that contributed
one counts the pod. It is read from
[`kuik.enix.io/no-alternatives`](./observability.md#annotations), which maps each container no
candidate could serve to the resources that offered one — so the count comes off the pod rather than
from replaying the matching, which a CR edited since admission would answer wrongly.

Status controllers read the original reference from `kuik.enix.io/original-images`, falling back to
the live container image for pods that were never rewritten and therefore carry no annotation.

## Global config

The global config is a **file of its own**, mounted from a ConfigMap and distinct from the custom
resources: it configures the operator, where the CRs describe what the operator should do. The three
processes read the same one and each holds its own copy.

It is **reloaded in place**, and that exists first for the `interval` and `timeout` of
[`registries`](#registries-pacing-what-kuik-pulls-from): retuning the pace of a registry that
rations should not cost a restart, since a restart already costs a full `interval` before the first
window opens. A reload that does not parse or does not validate is **rejected whole** — the
previously loaded config stays in effect, and the failure is logged and counted. A reload therefore
fails by keeping something that worked, never by falling back to a default nobody asked for.

What a reload does to the windows is specified in [Scheduling](#scheduling): the hosts whose config
moved are re-phased from the reload, the others keep their phase, and no ring cursor moves.

```yaml
# Identity of this cluster, appended to every tag an ImageMirror writes so that several clusters
# can share one `destination.path` and deduplicate blobs, see "Multi-cluster: shared destination,
# one tag per cluster". Required, short, and matching `^[a-zA-Z0-9][a-zA-Z0-9.-]*$` (the OCI tag
# alphabet without `_`, which is the suffix separator). Changing it orphans every tag written under
# the previous one
clusterID: cluster-a

# Optional metrics, off by default because they cost more series than the rest of the metric surface
# put together. See "observability v3"
metrics:
  copyDuration: false        # histogram of how long each copy took, per ImageMirror

# The destination of an ImageMirror is a registry kuik owns: it rations nothing, so it is not paced
# by the windows of `registries`. Its loops still read it whole, though, so what they need is a
# period rather than a rate. See "mirror: pacing the destination kuik owns"
mirror:
  destinationScan:
    interval: 1h             # Default: 1h - how often each ImageMirror re-reads its own destination:
                             # one pass does the self-check and the cleanup sweep together

webhook:
  # Default: true - A container with `imagePullPolicy: Always` asks for the newest content of its
  # tag, which a mirror cannot promise, so an ImageMirror's `rewritePolicy: Always` is ignored for
  # that container and its candidate is demoted to the end of the list. Set to false where the
  # mirror is wanted anyway, typically under the AlwaysPullImages admission plugin.
  # See "imagePullPolicy: Always demotes a mirror"
  demoteMirrorWithPullPolicyAlways: true
  availabilityCheck:
    timeout: 2s              # max time before considering a candidate as unavailable
    # Cache per webhook replica to avoid querying registry multiple time on burst
    # A single image used by 50 pods scheduled in a short period should result in 1 check per
    # replica, not 50
    activeCheckCache:
      ttl: 10s
    # Default: true - Reuse what the background loops already know: a candidate an ImageMonitor or
    # an ImageMirror reports failing is tried LAST instead of first. It is never dropped, so a
    # stale hint costs ordering and never availability.
    demoteKnownFailures: true

# How fast kuik reads from each registry host. Checks and copies run on windows counted from the
# start of the controller process, one image per window, so with `interval: 5m` a controller started
# at 13:32 takes its first image at 13:37. See "Scheduling"
registries:
  # Applies to every host, field by field: a host below overrides the fields it names and inherits
  # every other one from here
  default:
    check:
      interval: 1m            # one image of this host checked every minute
      timeout: 10s
    copy:
      interval: 3m            # one image pulled from this host every 3 minutes, on its own windows
      timeout: 0              # Default: 0 - no bound. A transfer takes what it takes, and a copy
                              # that runs long already shows by outlasting its own `interval`

  private-registry.tld:
    copy:
      interval: 30s           # local registry, no quota to spare it from; keeps `default.copy.timeout`
                              # and the whole of `default.check`

  docker.io:
    # Rate limited source: copy less often than default
    copy:
      interval: 10m

  public.ecr.aws:
    check:
      interval: 5m            # slower still: a ring of 300 images here comes back once a day

# Credentials the reconciler reads an image with when no CR declares any: its ImageMonitor checks and
# its ImageMirror copies. Never used by the webhook's active check, and never injected — see
# "Fallback credentials". KuiK is designed not to depend on a pod's imagePullSecrets: under
# `secretAccess.mode: restricted` it is refused that read in most namespaces (see "Authentication"),
# so this is how it gets credentials for a private registry nobody declared `auth` for. Entries
# match exactly as `ImageAlternative.alternatives` do
fallbackAuth:
- repositoryGroup: private-registry.tld/project1
  secretRef:
    name: project1-creds
- repositoryGroup: private-registry.tld/project2
  secretRef:
    name: project2-creds
- repositoryGroup: 123456.dkr.ecr.eu-west-3.amazonaws.com/acme
  provider:
    name: aws
    serviceAccountRef:
      name: kuik-ecr-access
- repositoryGroup: docker.io          # the whole host: a credential raises Docker Hub's pull quota
  secretRef:
    name: dockerhub-creds
```

### `mirror`: pacing the destination kuik owns

A **window** of [`registries`](#registries-pacing-what-kuik-pulls-from) rations somebody else's
quota and is counted per image: one request per `interval`, a rate. `mirror.destinationScan.interval`
is not the same quantity. A mirror's destination rations nothing — the operator owns it — but its two
reading loops traverse it *whole*, so what they need is a **period** between passes, not a rate
between requests. Taking a `check.interval` for it would re-read the entire destination every
minute; the two are separate fields because they measure different things.

One pass covers both destination loops together: the self-check `HEAD`s every desired reference
([walkthrough 02, B.2](./walkthroughs/02-imagemirror-reconciliation.md#b2-ask-precisely-one-reference-at-a-time)),
and with `cleanup.enabled` the sweep lists the tags of every repository in `status.repositories`
([C.3](./walkthroughs/02-imagemirror-reconciliation.md#c3-sweep-the-repositories)). The interval is
therefore what bounds the cost of the sweep, which grows with the inventory, and what the freshness
of a destination verdict is worth.

Two differences from the window model are deliberate:

- **the first pass runs at startup**, without waiting an `interval` — where a first window is
  deliberately a full `interval` away. That immediate pass is what covers the crash window of
  `A.8`, and a mirror has no quota of its own to protect
- **it holds no cursor and no ring.** A pass is whole or it is not, so there is nothing to resume and
  no lap to report; `status.selfChecked` timestamps the last completed one, and the destination
  never appears in `status.checks.registries`

Reconciles themselves stay event-driven, on pod and CR events, debounced. This interval does not
change that: it bounds only the parts of a reconcile that read the destination, so a reconcile firing
five seconds after a pass reuses its verdict rather than re-reading. The secret syncer's periodic
re-apply is unaffected and keeps running on the informer's resync interval
([walkthrough 03, B.4](./walkthroughs/03-secret-syncer-reconciliation.md#b4-someone-edited-a-managed-secret)),
at controller-runtime's defaults.

### `demoteKnownFailures`: reusing what the loops already know

The webhook probes candidates in order, and the background loops have often already answered the
same question. `demoteKnownFailures` reuses their published verdicts to **reorder** the candidate
list, sending what is known to be failing to the end. Three status lists feed it:

- [`ImageMonitor.status.unavailableImages`](./status.md#imagemonitor) — the origin reference, which
  is a candidate in its own right
- [`ImageMonitor.status.unavailableAlternatives`](./status.md#imagemonitor) — one particular
  alternative. Only populated with [`monitorAlternatives`](#imagemonitor) enabled
- [`ImageMirror.status.failedImageCopies`](./status.md#imagemirror) — an origin whose copy has not
  succeeded; the webhook demotes the mirror candidate it computes for that origin, the reference not
  being there to be served

**A demoted candidate is still probed.** Nothing is dropped: it moves to the end of the list and is
tried once everything above it has failed, active check in webhook have the final decision.

**The reverse does not hold.** A candidate a monitor reports *available* is never promoted, and
could not be: the monitor may have reached that verdict with a `fallbackAuth` credential the webhook
does not use ([No `auth` at all](#no-auth-at-all)), so its success says nothing about what a node
can pull. Only failures are reused, and only to reorder.

### `registries`: pacing what kuik pulls from

`registries` holds **pacing and nothing else**: how often kuik may read from a host, and how long it
waits for an answer. It is about registries that ration — a public registry with a pull quota — and
says nothing about credentials.

> [!IMPORTANT]
> The pacing applies to background checks only — those of an `ImageMonitor` and of an `ImageMirror`.
> The active check in the webhook always performs its request, whatever `interval` is configured,
> and is bounded by its own `webhook.availabilityCheck.timeout`.

`default` applies to every host **field by field**. A host entry overrides the fields it names and
inherits every other one, so `private-registry.tld` above, which names only `copy.interval`, keeps
`default.copy.timeout` and the whole of `default.check`. Anything else would make the three one-field
host entries of the example silently drop three settings each.

`timeout` bounds **the whole operation**, not one request: a check gets a budget for reaching a
verdict, whatever number of requests the registry's auth flow costs it.

**`copy.timeout` defaults to `0`, which means no bound at all.** How long an image legitimately
takes to transfer is not something an operator should have to predict, and a copy cut off half-way
costs the whole transfer without fixing anything — it is retried and cut off again. A copy that runs
long is already visible without a deadline: it outlasts its own `copy.interval`, which shows up as
windows going unused while a backlog is pending ([Scheduling](#scheduling)), and directly in
`kuik_mirror_copy_duration_seconds` where [`metrics.copyDuration`](#global-config) is enabled.
Setting a non-zero value is for operators who would rather abandon a copy than let it finish.

### Fallback credentials

`fallbackAuth` is a flat list, outside `registries`, and each entry matches images exactly as an
`ImageAlternative` entry does — [`repository`](#alternatives-matching) for one repository,
`repositoryGroup` for everything below a path at any depth, written **fully qualified, hostname
included**, and compared at path segment granularity. One matching syntax for the whole spec, and
one validation rule.

The two keys are separate because the two sets barely overlap: hosts worth pacing are the public
ones that ration, hosts worth authenticating are the private ones that do not. They meet only where
a credential buys a larger quota — Docker Hub, the entry above being exactly that case. Keeping them
in one map forced every credential to repeat the hostname the map key already carried.

Two rules differ from `alternatives`, both because `fallbackAuth` only ever *selects* an entry where
`alternatives` *produces* a reference:

- **the two forms may be mixed** in the list. `alternatives` forbids it because its entries have to
  describe the same set of images for a rewrite to carry the remainder over; nothing is rewritten
  here, so a specific repository and a broad group can sit side by side
- **the most specific match wins**: a `repository` beats a `repositoryGroup`, and a deeper group
  beats a shallower one. An image matching no entry is read anonymously

Those secrets serve the reconciler's checks and copies, and nothing else. They are never injected as
pull secrets, and for that same reason never used by the webhook's active check
([No `auth` at all](#no-auth-at-all)): a verdict obtained with a credential the node will not have is
worse than no verdict.

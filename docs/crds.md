---
description: Reference of the ImageAlternative, ImageMirror and ImageMonitor custom resources, their fields, validation rules and status.
---

# Custom Resource Definitions

kuik is configured with three **cluster-scoped** custom resources of the `kuik.enix.io/v1alpha1` API group:

| Kind | Purpose |
| ---- | ------- |
| [`ImageAlternative`](#imagealternative) | Routes images to alternative repositories holding the same images, as fallbacks or ahead of the original |
| [`ImageMirror`](#imagemirror) | Copies images to a registry you own, and routes to the copy |
| [`ImageMonitor`](#imagemonitor) | Checks that the images running in the cluster are still available upstream, and reports the ones that fail or drift |

Every field below is validated by the API server (schema, enums, CEL rules), so an invalid resource is rejected at `kubectl apply` with a message naming the rule it broke. The full schema is available with `kubectl explain imagealternatives.kuik.enix.io --recursive` and its two siblings.

## Common fields

### Selecting workloads

A resource applies to **pods**, never to images directly. `podSelector` and `namespaceSelector` are standard [label selectors](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#label-selectors), and an empty or absent selector matches everything. Restricting a resource to one namespace is done with the namespace's name label:

```yaml
spec:
  namespaceSelector:
    matchLabels:
      kubernetes.io/metadata.name: monitoring
```

Excluding a workload is expressed on the resources that would otherwise apply to it; there is no operator-wide skip list.

Three gates sit ahead of every resource and cannot be configured: a container whose reference kuik itself produced is never rewritten again, a container with `imagePullPolicy: Never` is never rewritten, and a static pod (the kubelet's mirror pod) is never rewritten.

### `rewritePolicy`

Where a resource's candidates sit relative to the original image when the webhook probes them:

| Value | Meaning | Kinds |
| ----- | ------- | ----- |
| `OnFailure` | Default. The original image is tried first, the resource's candidates are fallbacks | `ImageAlternative`, `ImageMirror` |
| `Always` | The resource's candidates are tried ahead of the original, to bypass a quota, latency or network cost | `ImageAlternative`, `ImageMirror` |
| `None` | Never routes. The mirror copies images without ever rewriting a pod | `ImageMirror` only |

When several resources cover one image, their candidates are merged in one list: `Always` mirrors, then `Always` alternatives, then the original, then `OnFailure` alternatives, then `OnFailure` mirrors, resources sorted by name within each band.

With [`webhook.availabilityCheck.demoteKnownFailures`](./configuration.md#keys) (on by default), the webhook then moves to the end of the list the candidates the background loops report failing:

- the original, while any `ImageMonitor` lists it in `status.unavailableImages`, whatever that monitor's selectors
- a mirror candidate, while its own `ImageMirror` lists the pod's image in `status.failedImageCopies`; another mirror of the same image keeps its place. A failed resync of a tag the mirror also lists in `status.driftedImages` does not count: the destination still serves the previous copy
- an alternative candidate, while any `ImageMonitor` lists its reference in `status.unavailableAlternatives` with `via` naming the `ImageAlternative` that offered it, whatever its `derivedFrom` and that monitor's selectors; the same reference offered by another `ImageAlternative` keeps its place

The moved candidates keep their relative order, after everything else, an original already sent last by an [`unavailable: true`](#imagealternative) entry included. A moved candidate is still probed and served when everything above it fails, and a candidate reported available is never moved up. A moved `Always` candidate is served as a fallback, after the original failed, so it is recorded with `policy: OnFailure`, as an `Always` mirror is for a container with `imagePullPolicy: Always`. An entry counts as long as the status lists it, and the webhook only reads these statuses. Three limits:

- an entry beyond the cap of its status list (`truncated`) is not seen, and its candidate keeps its place
- a mirror candidate whose copy is still waiting, not yet failed, keeps its place: the first pods of a new image probe it and fall back as without the setting
- `via` names only the first `ImageAlternative` offering an alternative, in candidate order: when several offer the same reference, only the candidate of that one moves

### What the webhook records on a pod

The webhook probes the candidates in order and places the first one that answers. It records its decision on the pod, in three annotations holding JSON objects keyed by container name. A container appears in at most one of them:

| Annotation | The container | Value |
| ---------- | ------------- | ----- |
| *(none)* | kept its image: the original answered first | |
| `kuik.enix.io/rewrites` | was rewritten | `{"by": "<kind>/<name>", "origin": ..., "rewrittenTo": ..., "policy": "Always" \| "OnFailure"}` |
| `kuik.enix.io/conceded-rewrites` | was rewritten, then another mutating webhook replaced the image, and kuik stood down | the entry that was in `rewrites`, unchanged |
| `kuik.enix.io/no-alternatives` | kept its image: no candidate answered | the resources that offered one, as `["<kind>/<name>", ...]` |

When the retained candidate's `auth` injects a pull secret, the pod's `imagePullSecrets` also gets `kuik-inject-<kind>-<name>`. The webhook exports two counters, `kuik_routing_rewrites_total{kind, name, policy}` and `kuik_routing_alternatives_exhausted_total{kind, name}`, listed with the reconciler's events and series in [Events, metrics and logs](./observability.md).

### `auth`

The credential kuik reads a registry with, wherever credentials appear on a resource: `ImageAlternative` entries, and an `ImageMirror`'s `destination.manage` and `destination.pull`. It is a union, and exactly one of `secretRef` or `provider` must be set:

```yaml
auth:
  secretRef:                # a docker-registry Secret
    name: quay-pull
  injectPullSecret: true    # default: true with secretRef
# --- or ---
auth:
  provider:                 # an ambient cloud identity
    name: aws               # aws | gcp | azure
    serviceAccountRef:      # optional: request a token for this ServiceAccount instead of the controller's identity
      name: mirror-pusher
  injectPullSecret: false   # default: false with provider
```

| Field | Description |
| ----- | ----------- |
| `secretRef.name` | Name of a `kubernetes.io/dockerconfigjson` Secret. It carries **no namespace** and always resolves in kuik's install namespace, so referencing a Secret requires being able to write it there |
| `provider.name` | `aws`, `gcp` or `azure` |
| `provider.serviceAccountRef.name` | ServiceAccount in kuik's install namespace whose token is requested, so a resource carries its own cloud role |
| `injectPullSecret` | Whether kuik copies a pull secret into the namespaces of the pods this credential serves, so the kubelet can pull the image. Defaults to `true` with `secretRef` and `false` with `provider`. A `provider` credential is not injected yet, whatever this field says. See [Injected pull secrets](./concepts/pull-secrets.md) |

Without any `auth`, kuik injects nothing: the pod is expected to carry its own `imagePullSecrets`, or the operator's [`fallbackAuth`](./configuration.md) to cover the registry for kuik's own reads.

### Repository paths

`repository`, `repositoryGroup` and `destination.path` are **fully qualified repository paths**: a registry host (with an optional port), then path components, with **no tag and no digest**. `quay.io/acme/foo` and `registry.local:5000/mirror/acme/foo` are valid; `acme/foo`, `quay.io/acme/foo:v1` and `quay.io/acme/foo@sha256:…` are rejected. The host is recognised as the container runtime does: it contains a dot, carries a port, or is `localhost`. A host alone (`docker.io`) is valid for `repositoryGroup` and `destination.path`, never for `repository`: a repository is one image with all its tags, `docker.io/library/nginx`.

Image references are normalized before any comparison, so `nginx:1.27` is matched as `docker.io/library/nginx:1.27`.

## ImageAlternative

Declares that several repositories hold the same images, so that a pod referencing one of them can be served from another.

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ImageAlternative
metadata:
  name: acme-foo
spec:
  podSelector: {}
  namespaceSelector: {}
  rewritePolicy: OnFailure    # OnFailure | Always
  alternatives:
  - repository: quay.io/acme/foo
  - repository: docker.io/acme-org/foo
  - repository: 123456.dkr.ecr.eu-west-3.amazonaws.com/repo/acme/foo
    auth:
      provider:
        name: aws
        serviceAccountRef:
          name: kuik-ecr-access
  - repository: registry.local:5000/mirror/acme/foo
    insecure: true
    unavailable: true
    auth:
      secretRef:
        name: local-registry
```

### Fields

| Field | Required | Description |
| ----- | -------- | ----------- |
| `spec.podSelector` | | Pods this resource applies to. Empty matches every pod |
| `spec.namespaceSelector` | | Namespaces this resource applies to. Empty matches every namespace |
| `spec.rewritePolicy` | | `OnFailure` (default) or `Always`, see [`rewritePolicy`](#rewritepolicy) |
| `spec.alternatives[]` | ✅ | Ordered list of equivalent repositories, at least one entry. Every entry uses the same form, `repository` or `repositoryGroup` |
| `spec.alternatives[].repository` | one of | Matches that exact repository, whatever the tag or digest |
| `spec.alternatives[].repositoryGroup` | one of | Matches every repository located under that path, at any depth |
| `spec.alternatives[].insecure` | | kuik probes and pulls this registry over HTTP. The nodes' container runtime must be configured for it separately. An image this entry matches is reached over HTTP too, when it is the most specific entry of this resource matching it and the resource applies to the pod; the image keeps its own credentials (the pod's pull secrets), never this entry's `auth`. An entry marked `unavailable` gives no transport |
| `spec.alternatives[].unavailable` | | Declares the source gone: the entry still matches images, so the resource applies to them, but it is never offered, never read as a copy source and never monitored. An original image matching such an entry is tried last |
| `spec.alternatives[].auth` | | Credential for this repository, see [`auth`](#auth) |

### Matching

An entry matches at path segment granularity, never as a string prefix: `quay.io/acme/foo` matches `quay.io/acme/foo:v1` but never `quay.io/acme/foo-bar:v1`. The tag or digest is always carried over to the candidate, and for a `repositoryGroup` the remainder of the path is too.

| Image | `repository: quay.io/acme/foo` | `repositoryGroup: quay.io/acme/foo` |
| ----- | ------------------------------ | ----------------------------------- |
| `quay.io/acme/foo:latest` | matches | no: the group itself is not a repository |
| `quay.io/acme/foo-bar:latest` | no | no |
| `quay.io/acme/foo/bar:latest` | no: deeper than the entry | matches, rewritten to `<other>/bar:latest` |
| `quay.io/acme/foo/bar/oni:latest` | no | matches, rewritten to `<other>/bar/oni:latest` |

Several `ImageAlternative` may match the same image; they add fallbacks to each other rather than shadowing each other. Digest-pinned images are routed like any other: the digest is carried over, and a candidate that does not hold it answers not found.

### Validation

Rejected at admission:

- an entry carrying both `repository` and `repositoryGroup`, or neither;
- a `repository` or `repositoryGroup` carrying a tag or a digest, or not fully qualified with a registry host, and a `repository` naming a host alone;
- a list mixing `repository` and `repositoryGroup` entries;
- an empty `alternatives` list;
- an `auth` carrying both `secretRef` and `provider`, or neither.

### Status

| Field | Description |
| ----- | ----------- |
| `status.pods.tracked` / `.rewritten` | Pods selected, and pods carrying at least one standing rewrite by this resource. Neither sums across resources |
| `status.containers.*` | Containers of the selected pods, partitioned into `untouched`, `rewritten`, `conceded` (another webhook overwrote kuik's rewrite), `stale` (edited after admission) and `noAlternatives` |
| `status.activeFallbacks[]` | Origin images this resource currently stands in for, with `rewrittenTo`, `pods` and `since`. Rewrites recorded `OnFailure` only, which includes an `Always` candidate moved last as a known failure ([`demoteKnownFailures`](#rewritepolicy)) |
| `status.noAlternatives[]` | Images no candidate could serve |
| `status.concededRewrites[]` / `status.staleRewrites[]` | Rewrites something replaced, with `replacedBy` |
| `status.truncated` | Per capped list, how many entries were left out, see [bounded lists](#status-conventions) |
| `status.conditions` | `Ready`, `FallbackActive`, `AlternativesExhausted`, `ListCapacityPressure` |

The same counts, the anomalies as series and the events on the pods are in [Events, metrics and logs](./observability.md).

## ImageMirror

Copies the images of the pods it selects to a destination registry, and routes to the copy according to `rewritePolicy`.

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ImageMirror
metadata:
  name: prod-mirror
spec:
  podSelector: {}
  namespaceSelector: {}
  excludeImages:
  - ghcr.io/foo-bar/**
  rewritePolicy: OnFailure    # OnFailure | Always | None
  destination:
    path: registry.tld/mirror/
    insecure: false
    manage:
      auth:
        secretRef:
          name: mirror-write-credentials
    pull:
      auth:
        secretRef:
          name: mirror-read-credentials
  cleanup:
    enabled: true
    retention: 168h
    deleteByDigest: false     # not safe on a destination shared by several clusters
  driftPolicy: Ignore         # Ignore | Warn | Sync
```

### Fields

| Field | Required | Description |
| ----- | -------- | ----------- |
| `spec.podSelector` | | Pods this resource applies to. Empty matches every pod |
| `spec.namespaceSelector` | | Namespaces this resource applies to. Empty matches every namespace |
| `spec.excludeImages[]` | | Globs of images to keep out of the mirror: no copy, no candidate, no tag at the destination. See [excluding images](#excluding-images) |
| `spec.rewritePolicy` | | `OnFailure` (default), `Always` or `None`, see [`rewritePolicy`](#rewritepolicy) |
| `spec.destination.path` | ✅ | Registry path the **full** original reference is appended to, hostname included: `registry.tld/mirror/` turns `docker.io/library/nginx:1.27` into `registry.tld/mirror/docker.io/library/nginx:1.27_<clusterID>`. The registry must accept deeply nested repository paths. Immutable: the tags written under a previous path could never be cleaned up |
| `spec.destination.insecure` | | kuik pushes to and reads this registry over HTTP |
| `spec.destination.manage.auth` | | Controller credential at the destination: pushes, self-check reads, tag listings and deletions. Needs read as well as write. Its `injectPullSecret` is ignored |
| `spec.destination.pull.auth` | | Credential the kubelet pulls the mirrored images with, injected in the namespaces that need it |
| `spec.cleanup.enabled` | | Default `true`. Deletes the destination tags no pod references any more once their retention elapsed. With `false` nothing is deleted and `retention` has no effect |
| `spec.cleanup.retention` | | Default `168h`. How long an unused tag is held before deletion, so a CronJob's image survives between runs. A Go duration |
| `spec.cleanup.deleteByDigest` | | Default `false`. Deletes a tag by deleting the manifest it points at, for a destination that refuses tag deletion. **Not safe on a destination shared by several clusters**, see [deleting by digest](#deleting-by-digest). No effect while `enabled` is `false` |
| `spec.driftPolicy` | | What to do when the upstream digest of a copied tag moves: `Ignore` (default, copy once), `Warn` (re-read the upstream periodically and report), `Sync` (re-read and copy again) |

The tag of every copy carries the operator's [cluster ID](./configuration.md#cluster-id) as a `_<clusterID>` suffix, so that several clusters can share one destination: the second cluster to need an image uploads nothing, and each cluster only ever creates, verifies and deletes its own tags, as long as it deletes by tag, the default. Under `cleanup.deleteByDigest`, a deletion removes every tag of the manifest, the other clusters' included: see [deleting by digest](#deleting-by-digest). The `ImageMirror` object is identical on every cluster.

A copy reads the origin first, whatever the `rewritePolicy` of the `ImageAlternative`s covering it. One copy serves every pod declaring the image: it reads the origin over plain HTTP when, in an `ImageAlternative` applying to any of those pods, the most specific entry matching the origin is `insecure`. An image no pod declares any more keeps the transport its pods had while an `ImageAlternative` still declares it `insecure`, whatever its selectors; after a restart it takes it from every `ImageAlternative`, whatever its selectors. It then sends the origin's credentials over plain HTTP, as the kubelet does for a registry configured for HTTP: the pull secrets of a pod declaring it the `insecure` declaration applies to (the pull secrets of the other pods never cross plain HTTP), then the matching `fallbackAuth` entry, never the entry's `auth`. The drift checks of `Warn` and `Sync` read the origin over the same transport. When the origin does not answer, it reads those alternatives in their candidate order, skipping the entries marked `unavailable`, on a copy window of each alternative's own host. A source that answers but fails the copy on its side, a `429` on a blob for instance, gives way to the next one the same way rather than being read again on its next window; a failure on the destination side keeps the source and enters `status.failedImageCopies` at once. A source failure enters it once every source was tried: with the reason of the last source that failed for another reason than lacking the image (`QuotaExceeded` for an origin answering `429` then an alternative lacking the image), or `SourceNotFound` when every one lacked it. An origin matching an `unavailable` entry is read last. The destination is derived from the origin whichever source the bytes came from.

### Excluding images

Each `excludeImages` entry is a glob matched **whole** against the normalized reference. A `:` after the last `/` splits a tag part from a repository part; `*` matches inside one path segment, `**` across segments.

| Pattern | Excludes |
| ------- | -------- |
| `ghcr.io/foo-bar/**` | everything under `ghcr.io/foo-bar/`, at any depth |
| `ghcr.io/foo-bar/*` | one level under it: `ghcr.io/foo-bar/app` but not `ghcr.io/foo-bar/team/app` |
| `docker.io/library/nginx` | that repository, whatever the tag or digest |
| `quay.io/acme/*-debug` | `quay.io/acme/foo-debug`, `quay.io/acme/bar-debug` |
| `**:latest` | every mutable `latest`, whatever the registry |

The mirror's own `destination.path` is excluded on top of this list, which is what keeps two mirrors from feeding each other.

> [!WARNING]
> Excluding an image, or narrowing a selector, also removes the copy already made: the tags enter `status.pendingDeletion` and are deleted once `cleanup.retention` has elapsed. `cleanup.enabled: false` is what stops it.

An image no selected pod runs any more, its last pod gone or a narrowed selector, is retained for `cleanup.retention`: it is copied again if it disappears from the destination, but an image not copied yet when it is retained is never copied, and leaves `status.failedImageCopies`.

### Destination requirements

The destination must conform to the OCI Distribution spec, accept arbitrarily nested repository paths, and refuse a manifest whose blobs it does not hold. With `cleanup.enabled`, it must also support tag deletion (`DELETE /v2/<name>/manifests/<tag>`) and run its own garbage collection of untagged manifests: by default kuik deletes tags only, never manifests or blobs. A registry that rejects tag deletion flips the `Ready` condition to `False` with reason `RegistryDeleteUnsupported`; on a destination the `ImageMirror` is the only writer of, [`cleanup.deleteByDigest`](#deleting-by-digest) works around it.

Once the destination refused a deletion as unsupported, the sweep stops deleting there. It tries a single deletion again 10 seconds later, then waits twice as long after each refused try, up to 1 hour, without waiting for the next destination pass; a pass in between deletes nothing; the tags due stay in `status.pendingDeletion`. `ImageDeletionFailed` fires at the first refusal only, not at the refused tries that follow. Once a try is accepted, the sweep deletes every tag due except a tag that the `deleteByDigest` guard keeps, `Ready` goes back to `True`, and a later refusal starts over at 10 seconds with a new event. A change of the `ImageMirror` spec forgets the refusal and tries at once, a refusal then starting a new series with its event; disabling cleanup forgets the refusal too. The wait is kept in memory: a restart tries at its first pass, with no event again for a refusal `Ready` already reports, unless `cleanup.deleteByDigest` or the spec changed since that refusal; then its first refusal is announced.

### Deleting by digest

`cleanup.deleteByDigest: true` is for a destination that refuses tag deletion, Harbor for instance (see [Harbor as an ImageMirror destination](./guides/harbor-destination.md)). The sweep and the cleanup of a deleted `ImageMirror` then delete a tag in 3 steps:

1. `HEAD` the tag to read the digest of its manifest (an index for a multi-platform image). A tag already gone counts as deleted.
2. Guard, in the sweep only: `HEAD` every other tag of this cluster in the repository that the mirror still expects, those waiting out their retention included. If one of them points at the same manifest, the tag is kept in `status.pendingDeletion` and tried again at the next pass, with no event: deleting the manifest would take that tag with it (`latest` and `v1.2` of the same image, or the anchor tag of a pinned pod). A deleted `ImageMirror` expects no tag any more, so the guard never holds its cleanup.
3. `DELETE /v2/<name>/manifests/<digest>`. A refusal is reported like a refused tag deletion: `ImageDeletionFailed`, and `Ready` `False` with reason `RegistryDeleteUnsupported`; the sweep then waits for its next try the same way, see [Destination requirements](#destination-requirements).

These `HEAD`s go to the destination only, and only when a deletion is due, at most once per tag and pass; an expected tag whose digest the mirror already knows (a pinned digest, or the one the last pass or copy saw) is not read at all, and holds the deletion even before its copy. While the sweep deletes, no copy of the mirror starts; when a copy is running, no new one starts and the deletions wait for it to end, then run at once: otherwise a copy could write a tag of the manifest between the guard and the deletion.

> [!WARNING]
> **Deleting by digest is not safe on a destination shared by several clusters**, and the guard does not change that. Deleting a manifest removes every tag pointing at it: the tags of the other clusters, the tags of another `ImageMirror` writing to the same repository, and tags kuik does not manage (pushed by hand or by another tool). Each other cluster whose tag goes:
>
> - fails to pull that image from the mirror until its next destination pass copies it again (`mirror.destinationScan.interval`); under `rewritePolicy: Always`, every new pull of it is affected;
> - copies the image from upstream again, spending upstream quota and traffic, and emits an `ImageRecopied` warning;
> - loses the image for good when it is gone upstream; pinned digests are the most exposed, since registries often garbage-collect old digests;
> - under `driftPolicy: Ignore`, copies whatever the upstream tag points to now, so its copy may move forward silently.
>
> Use it only on a destination path the `ImageMirror` is the only writer of: no other cluster, no other `ImageMirror`, no other tool and no hand-pushed tag.

### Deleting an ImageMirror

A deleted `ImageMirror` stops routing new pods and copying images at once, waits for the copies still running, then stays `Terminating` until kuik has released it:

1. While a live pod runs an image under `destination.path`, whatever its `imagePullPolicy`, it waits, keeping its [injected pull Secrets](./concepts/pull-secrets.md), which go with it. Since Kubernetes 1.35 the kubelet checks, at every container start, restarts included, that the pod's pull credentials match the ones that pulled the image ([KEP-2535](https://github.com/kubernetes/enhancements/issues/2535), on by default): without the Secret, or once the tag is deleted, a crashed or OOM-killed container of that pod could not start again. A recreated pod goes through the webhook again, which no longer offers the deleted mirror: roll those workloads (`kubectl rollout restart`) to release it. A Normal `WaitingForPods` event fires when this wait starts and each time the count of those pods changes.
2. With `cleanup.enabled`, it then deletes every tag of this cluster in `status.repositories`, whatever their retention, by digest under [`cleanup.deleteByDigest`](#deleting-by-digest). It stays `Terminating` while the `manage` credential cannot be read, or while the destination refuses the deletion: released, it would take with it the only inventory of the tags left behind. With `cleanup.enabled: false`, it is released, its tags left in place.

While held, the `DeletionBlocked` condition is `True`, its reason naming the current hold, its message the way out:

| Reason | Holds while |
| ------ | ----------- |
| `CopiesInFlight` | copies of the `ImageMirror` still run, within a copy timeout |
| `PodsRunningCopies` | live pods run its copies; the message gives their count and names up to 5 of them |
| `ManageCredentialUnreadable` | the `manage` credential cannot be read, with `cleanup.enabled` |
| `DestinationRefusedDeletion` | the destination refuses tag deletion, or manifest deletion under `cleanup.deleteByDigest`, with `cleanup.enabled`; `Ready` is also `False` with reason `RegistryDeleteUnsupported` |

The condition follows the hold at each try. It is not set while the destination cannot be reached or refuses the credential: the reconciler logs show it.

Never remove the `kuik.enix.io/mirror-cleanup` finalizer by hand: the tags of this cluster stay behind, and the pods still running the copies lose their pull Secret.

While held, it retries after 10 seconds, doubling up to 30 minutes, and sooner when a pod goes. A destination that never accepts the deletion holds the mirror for good: each try emits `ImageDeletionFailed`, and `Ready` goes `False` with reason `RegistryDeleteUnsupported`. Set `cleanup.enabled: false`, before deleting it or while it is held, to release it once no pod runs its copies and leave the tags in place; when the destination refuses tag deletion and the `ImageMirror` is its only writer, `cleanup.deleteByDigest: true` lets the cleanup through instead.

### Status

The copy side first, then the routing side, which is field for field an [`ImageAlternative`'s](#status).

| Field | Description |
| ----- | ----------- |
| `status.images.copy.*` | References held at the destination: `tracked` (= `running` + `standby` + `retained`), `available`, `unavailable`, `drifted`, `missingSource`, `orphanTags` |
| `status.driftedImages[]` | Tags whose upstream digest moved away from the copy, under `Warn` or `Sync` |
| `status.failedImageCopies[]` | Origins whose copy has not succeeded, with `reason` (`SourceNotFound`, `PushRejected`, `Unauthorized`, `QuotaExceeded`, `SourceUnreachable`, `DestinationUnreachable`), `since` and `lastAttempt` |
| `status.pendingDeletion[]` | Destination tags waiting out `cleanup.retention`, with `origin` when known and `unusedSince`. Not capped |
| `status.selfChecked` | End of the last full comparison against the destination |
| `status.repositories[]` | Destination repositories this mirror wrote to, for the cleanup sweep. Not capped |
| `status.checks.registries[]` | Health of the drift check rings, one per source host, under `Warn` or `Sync`. The destination never appears here |
| `status.pods`, `status.containers`, `status.activeFallbacks[]`, `status.noAlternatives[]`, `status.concededRewrites[]`, `status.staleRewrites[]` | The routing side, absent under `rewritePolicy: None` |
| `status.conditions` | `Ready`, `DestinationOutOfSync`, `FallbackActive`, `AlternativesExhausted`, `ListCapacityPressure`, and `DeletionBlocked` while a deleted `ImageMirror` is held, see [Deleting an ImageMirror](#deleting-an-imagemirror) |

## ImageMonitor

Checks the availability of the images of the pods it selects, and reports the ones that fail. It never contributes a routing candidate.

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ImageMonitor
metadata:
  name: cluster-images
spec:
  podSelector: {}
  namespaceSelector: {}
  unusedImageRetention: 168h
  monitorAlternatives: false
```

### Fields

| Field | Required | Description |
| ----- | -------- | ----------- |
| `spec.podSelector` | | Pods this resource applies to. Empty matches every pod |
| `spec.namespaceSelector` | | Namespaces this resource applies to. Empty matches every namespace |
| `spec.unusedImageRetention` | | Default `168h`. Keeps monitoring an image for this long after no pod declares it any more, so a CronJob's image stays checked between runs. A Go duration |
| `spec.monitorAlternatives` | | Default `false`. Also monitors the alternatives kuik would offer for each tracked image, from `ImageAlternative` entries only. A mirror destination is verified by the mirror's own self-check, and entries marked `unavailable: true` are never tracked |

A monitor tracks the **origin** reference of every container: the reference its spec declares, or, where a standing kuik rewrite put a different one there, the origin that rewrite recorded. It therefore never sees a mirror's reference in place of the origin it replaced.

The pods that count are the live ones (`Pending` or `Running`) the selectors select, static pods included. Init containers count, ephemeral containers never do. An image several pods declare counts once: `running` when a container runs that exact reference, `standby` when every container declaring it was routed elsewhere.

When no pod declares an image any more, it moves to `status.retainedImages` for `unusedImageRetention`, with the digest its most recently started container ran, and leaves it as soon as a pod declares it again. The monitor learns that an image went unused by watching its pods: an image whose last pod goes away while no reconciler runs is not retained.

Under `monitorAlternatives`, the monitor also tracks every candidate an `ImageAlternative` would offer for each origin it tracks, as the webhook would propose it for that container: only the `ImageAlternative`s whose selectors apply to the pod count, never an `ImageMirror` destination, never an entry marked `unavailable` (an origin such an entry names is still tracked as an origin), and never an entry naming the origin's own repository. A container the webhook never rewrites, under `imagePullPolicy: Never` or in a static pod, offers no alternative. An alternative is `running` while a container carries it in place of its origin through a standing rewrite, `standby` otherwise. A retained origin keeps the alternatives its pods were offered when a pod last declared it, as long as their `ImageAlternative` still offers them (an entry marked `unavailable` or removed, or a deleted `ImageAlternative`, drops them); they count as `retained` without entering `retainedImages`, and leave once the origin's retention elapses. After a restart, or once `monitorAlternatives` is turned off and on again, the monitor no longer knows what those pods were offered: the alternatives of a retained origin are then matched against every `ImageAlternative`, whatever its selectors, until its retention elapses. A reference that is both an origin and an alternative counts in both populations and is checked once. An `ImageAlternative` edit is followed without waiting for a pod to change, and turning `monitorAlternatives` off removes `images.alternatives`.

An alternative is checked on the rings like an origin, on a ring of its own host when no origin uses it. A check tries the `auth` of every entry offering it, in candidate order, then the pull secrets of the pods of its origins, then the matching `fallbackAuth` entry, then anonymously; an entry Secret that is missing or malformed is skipped for the next credential and leaves `Ready` as it is. An alternative several origins stand for is checked once, with the `auth` of each of their entries in turn. Each entry's `auth` goes only over its own transport: the check reads over HTTPS with the `auth` of the secure entries, then the shared credentials (the pull secrets of the pods, `fallbackAuth`); when the registry refuses them or does not answer over HTTPS, and an entry is `insecure`, it reads again over plain HTTP with the `auth` of the `insecure` entries alone. The shared credentials cross plain HTTP only when no secure entry offers the reference and it is no origin, or an origin read over plain HTTP that no secure entry offers (below). `images.alternatives.available` and `unavailable` count the alternatives by the verdict of their last check, never among the origins: an alternative failing never enters `unavailableImages` nor emits `AnonymousAccessDenied`. A failing alternative enters `status.unavailableAlternatives` once per origin it stands for, with `derivedFrom`, `reason`, `since`, and `via` naming the first `ImageAlternative` offering it in candidate order (the `Always` band first, then by name). `AlternativesUnavailable` is `True` with reason `ChecksFailed` while any alternative fails. An alternative whose failure becomes `Unauthorized` emits `AlternativeUnusable`; any other failure emits nothing: alert on [`kuik_image_alternative_unavailable`](./observability.md#anomalies). A restart reads the entries of `status.unavailableAlternatives` back with their `since`, and does not announce an `Unauthorized` one again; for an alternative beyond the cap of the list, `since` is stamped anew and `AlternativeUnusable` may be emitted again. Until the rings have checked every alternative again, `truncated`, `ListCapacityPressure` and `images.alternatives.unavailable` stay at least as the status left them.

Checks are paced per registry host by the operator's [`registries` configuration](./configuration.md#keys): one image per window, so every tracked image comes back once per lap, the `cycleDuration` reported in status. See [Registry pacing](./concepts/pacing.md). Retained images are checked like the others. A restart resumes each ring after the `cursor` its status holds.

A check sends a manifest `HEAD` to the image's registry with each credential in turn until one answers: the pull secrets of the pods declaring it, then the matching `fallbackAuth` entry, or one anonymous `HEAD` when none applies (see [Registry credentials](./configuration.md#registry-credentials)). It reads an origin over HTTPS, or over plain HTTP when, for a pod declaring it, the most specific entry it matches in an `ImageAlternative` applying to that pod is [`insecure`](#fields), whether or not the monitor monitors alternatives; that entry's `auth` is never sent, and the shared credentials go over plain HTTP unless a secure entry also offers the reference. A retained origin keeps the transport its pods had while an `ImageAlternative` still declares it `insecure`; after a restart it takes it from every `ImageAlternative`, whatever its selectors. A monitor that does not monitor alternatives picks up an edited `insecure` at its next report, not at once. A registry refusing an anonymous check with `Unauthorized` emits `AnonymousAccessDenied` on the monitor, once while the image stays unavailable: the image is likely private and no credential covers it. An image a `fallbackAuth` entry matches never emits it, even when that entry gives no credential: its missing or malformed Secret shows in `Ready` instead. When several monitors track an image, one check serves them all, over the transport of the monitor whose ring sent it: an origin one monitor reads over plain HTTP and another over HTTPS (their pods differ, so the `ImageAlternative`s applying to them differ) gets the verdict of whichever checked last, a mirror's drift check included (see [Known limits](./known-limits.md)). Only the monitor whose ring sent the request emits the event or reports a broken `fallbackAuth` Secret in `Ready`. `available` and `unavailable` count the images by the verdict of their last check, so they fall short of `tracked` by the images the ring has not reached yet. A restart forgets the available verdicts: they come back as the rings turn, within one `cycleDuration`. An image whose check fails enters `status.unavailableImages`, its `since` stamped once and kept while the failure lasts, across restarts too, and leaves it as soon as a check answers. A restart reads back only the entries the capped list holds: for an image beyond the cap, `since` is stamped anew and `AnonymousAccessDenied` may be emitted again. Until the rings have checked every image again, `truncated`, `ListCapacityPressure` and `images.origin.unavailable` stay at least as the status left them. `ImagesUnavailable` is `True` with reason `ChecksFailed` while any origin fails. No event announces it: read it from the status, or alert on [`kuik_image_origin_unavailable`](./observability.md#anomalies).

An `ImageMonitor` does not tell when a tag moves upstream under the pods that run it: Kubernetes exposes no reliable way to know which digest a container runs, since a container runtime may report a platform manifest of a multi-arch image or a config digest in `imageID` ([kubernetes#115199](https://github.com/kubernetes/kubernetes/issues/115199)). To be told when a tag moves, copy it with an [`ImageMirror`](#imagemirror) under `driftPolicy: Warn`, which compares two registry digests. The `retainedImages` digest is the one `imageID` reports: it is empty for an image the node had before any pull (preloaded, or loaded with `kind load` or `ctr import`), which reports only its config digest.

### Status

| Field | Description |
| ----- | ----------- |
| `status.images.origin.*` | Origin references: `tracked` (= `running` + `standby` + `retained`), `available`, `unavailable` |
| `status.images.alternatives.*` | The same gauges for the monitored alternatives, under `monitorAlternatives` |
| `status.retainedImages[]` | References kept for `unusedImageRetention`, with `unusedSince` and `digest`. Not capped |
| `status.unavailableImages[]` | Tracked origins whose last check failed, with `reason` (`ManifestNotFound`, `Unauthorized`, `QuotaExceeded`, `Unreachable`), `since` and `pods` |
| `status.unavailableAlternatives[]` | Monitored alternatives whose last check failed, one entry per origin they stand for, with `derivedFrom`, `via` naming the `ImageAlternative` (`ImageAlternative/<name>`), `reason` and `since` |
| `status.checks.registries[]` | One ring per registry host: `images`, `cursor`, `cycleStarted`, `cycleDuration` |
| `status.truncated` | Per capped list, how many entries were left out, see [bounded lists](#status-conventions). `images.origin.unavailable` and `kuik_image_origin_unavailable` still count every failing image. `images.alternatives.unavailable` and `kuik_image_alternative_unavailable` cover every failing alternative. After a restart, the series of an image beyond the cap comes back once its ring reaches it |
| `status.conditions` | `Ready`, `ImagesUnavailable`, `AlternativesUnavailable`, `ListCapacityPressure` |

## Status conventions

**`Ready` is the only condition that is `True` when things are well.** Every other condition names an anomaly and stays `True` as long as it lasts, so "is anything wrong with this resource?" is one query: a condition whose `status` is `True` and whose `type` is not `Ready`. `Ready` goes `False` when the resource cannot work as declared (`InvalidConfig`, `SecretNotFound`, `SecretMalformed`, `TokenRequestFailed`, `RegistryDeleteUnsupported`), never because of one image.

**Anomaly lists are bounded.** `unavailableImages`, `unavailableAlternatives`, `driftedImages`, `failedImageCopies`, `activeFallbacks`, `noAlternatives`, `concededRewrites` and `staleRewrites` carry a fixed cap on their number of entries; they are samples, and the matching metrics carry every affected image. The oldest entries are kept. `ListCapacityPressure` goes `True` from 80% of the cap, and `status.truncated` records, per list, how many entries were left out once it is reached. The operational inventories (`repositories`, `pendingDeletion`, `retainedImages`, `checks.registries`) are never capped: truncating them would lose correctness rather than visibility.

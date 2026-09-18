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
| `injectPullSecret` | Whether kuik copies a pull secret into the namespaces of the pods this credential serves, so the kubelet can pull the image. Defaults to `true` with `secretRef` and `false` with `provider`; with a provider and `true`, kuik materialises and renews a Secret from the provider's short-lived token |

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
| `spec.alternatives[].insecure` | | kuik probes and pulls this registry over HTTP. The nodes' container runtime must be configured for it separately |
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
| `status.activeFallbacks[]` | Origin images this resource currently stands in for, with `rewrittenTo`, `pods` and `since`. `OnFailure` only |
| `status.noAlternatives[]` | Images no candidate could serve |
| `status.concededRewrites[]` / `status.staleRewrites[]` | Rewrites something replaced, with `replacedBy` |
| `status.truncated` | Per capped list, how many entries were left out, see [bounded lists](#status-conventions) |
| `status.conditions` | `Ready`, `FallbackActive`, `AlternativesExhausted`, `ListCapacityPressure` |

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
  driftPolicy: Ignore         # Ignore | Warn | Sync
```

### Fields

| Field | Required | Description |
| ----- | -------- | ----------- |
| `spec.podSelector` | | Pods this resource applies to. Empty matches every pod |
| `spec.namespaceSelector` | | Namespaces this resource applies to. Empty matches every namespace |
| `spec.excludeImages[]` | | Globs of images to keep out of the mirror: no copy, no candidate, no tag at the destination. See [excluding images](#excluding-images) |
| `spec.rewritePolicy` | | `OnFailure` (default), `Always` or `None`, see [`rewritePolicy`](#rewritepolicy) |
| `spec.destination.path` | ✅ | Registry path the **full** original reference is appended to, hostname included: `registry.tld/mirror/` turns `docker.io/library/nginx:1.27` into `registry.tld/mirror/docker.io/library/nginx:1.27_<clusterID>`. The registry must accept deeply nested repository paths |
| `spec.destination.insecure` | | kuik pushes to and reads this registry over HTTP |
| `spec.destination.manage.auth` | | Controller credential at the destination: pushes, self-check reads, tag listings and deletions. Needs read as well as write. Its `injectPullSecret` is ignored |
| `spec.destination.pull.auth` | | Credential the kubelet pulls the mirrored images with, injected in the namespaces that need it |
| `spec.cleanup.enabled` | | Default `true`. Deletes the destination tags no pod references any more once their retention elapsed. With `false` nothing is deleted and `retention` has no effect |
| `spec.cleanup.retention` | | Default `168h`. How long an unused tag is held before deletion, so a CronJob's image survives between runs. A Go duration |
| `spec.driftPolicy` | | What to do when the upstream digest of a copied tag moves: `Ignore` (default, copy once), `Warn` (re-read the upstream periodically and report), `Sync` (re-read and copy again) |

The tag of every copy carries the operator's [`clusterID`](./configuration.md) as a `_<clusterID>` suffix, so that several clusters can share one destination: the second cluster to need an image uploads nothing, and each cluster only ever creates, verifies and deletes its own tags. The `ImageMirror` object is identical on every cluster.

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

### Destination requirements

The destination must conform to the OCI Distribution spec, accept arbitrarily nested repository paths, and refuse a manifest whose blobs it does not hold. With `cleanup.enabled`, it must also support tag deletion (`DELETE /v2/<name>/manifests/<tag>`) and run its own garbage collection of untagged manifests: kuik deletes tags only, never manifests or blobs. A registry that rejects tag deletion flips the `Ready` condition to `False` with reason `RegistryDeleteUnsupported`.

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
| `status.conditions` | `Ready`, `DestinationOutOfSync`, `FallbackActive`, `AlternativesExhausted`, `ListCapacityPressure` |

## ImageMonitor

Checks the availability of the images of the pods it selects, and reports the ones that fail or drift. It never contributes a routing candidate.

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ImageMonitor
metadata:
  name: cluster-images
spec:
  podSelector: {}
  namespaceSelector: {}
  unusedImageRetention: 168h
  driftDetection: true
  monitorAlternatives: false
```

### Fields

| Field | Required | Description |
| ----- | -------- | ----------- |
| `spec.podSelector` | | Pods this resource applies to. Empty matches every pod |
| `spec.namespaceSelector` | | Namespaces this resource applies to. Empty matches every namespace |
| `spec.unusedImageRetention` | | Default `168h`. Keeps monitoring an image for this long after no pod declares it any more, so a CronJob's image stays checked between runs. A Go duration |
| `spec.driftDetection` | | Default `true`. Reports the tracked tags whose upstream digest differs from the one running in the cluster |
| `spec.monitorAlternatives` | | Default `false`. Also monitors the alternatives kuik would offer for each tracked image, from `ImageAlternative` entries only. A mirror destination is verified by the mirror's own self-check, and entries marked `unavailable: true` are never tracked |

A monitor tracks the **origin** reference of every container: the reference its spec declares, or, where a standing kuik rewrite put a different one there, the origin that rewrite recorded. It therefore never sees a mirror's reference in place of the origin it replaced.

Checks are paced per registry host by the operator's [`registries` configuration](./configuration.md): one image per window, so every tracked image comes back once per lap, the `cycleDuration` reported in status.

### Status

| Field | Description |
| ----- | ----------- |
| `status.images.origin.*` | Origin references: `tracked` (= `running` + `standby` + `retained`), `available`, `unavailable`, `drifted` |
| `status.images.alternatives.*` | The same gauges for the monitored alternatives, under `monitorAlternatives`, without `drifted`: an alternative is checked, never compared to what runs |
| `status.retainedImages[]` | References kept for `unusedImageRetention`, with `unusedSince` and `digest`. Not capped |
| `status.unavailableImages[]` | Tracked origins whose last check failed, with `reason` (`ManifestNotFound`, `Unauthorized`, `QuotaExceeded`, `Unreachable`), `since` and `pods` |
| `status.unavailableAlternatives[]` | Monitored alternatives whose last check failed, with `derivedFrom` and `via` naming the `ImageAlternative` |
| `status.driftedImages[]` | Tracked tags whose running digest differs from the upstream one, with every `runningDigests[]` seen and its pod count |
| `status.checks.registries[]` | One ring per registry host: `images`, `cursor`, `cycleStarted`, `cycleDuration` |
| `status.conditions` | `Ready`, `ImagesUnavailable`, `AlternativesUnavailable`, `ImagesDrifted`, `ListCapacityPressure` |

## Status conventions

**`Ready` is the only condition that is `True` when things are well.** Every other condition names an anomaly and stays `True` as long as it lasts, so "is anything wrong with this resource?" is one query: a condition whose `status` is `True` and whose `type` is not `Ready`. `Ready` goes `False` when the resource cannot work as declared (`InvalidConfig`, `SecretNotFound`, `SecretMalformed`, `TokenRequestFailed`, `RegistryDeleteUnsupported`), never because of one image.

**Anomaly lists are bounded.** `unavailableImages`, `unavailableAlternatives`, `driftedImages`, `failedImageCopies`, `activeFallbacks`, `noAlternatives`, `concededRewrites` and `staleRewrites` carry a fixed cap on their number of entries; they are samples, and the matching metrics carry every affected image. The oldest entries are kept. `ListCapacityPressure` goes `True` from 80% of the cap, and `status.truncated` records, per list, how many entries were left out once it is reached. The operational inventories (`repositories`, `pendingDeletion`, `retainedImages`, `checks.registries`) are never capped: truncating them would lose correctness rather than visibility.

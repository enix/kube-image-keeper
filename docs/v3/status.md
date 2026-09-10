# status v3

We only persist aggregates, anomalies and information that could not be recomputed from informer (or too costly). This way status is human readable and show only usable information (e.g. which image is unavailable). We don't need to persist a large number of information that could be rebuilt on pod restart.

Status is only computed via informer on leader elected controller, nothing is updated directly in mutating webhook. Which process writes what, and why the webhook writes nothing, is in [architecture v3](./architecture.md); the events and metrics that complete it are in [observability v3](./observability.md).

Conditions follow one rule: **`Ready` is the only one that is `True` when things are well.** Every
other condition names an anomaly and stays `True` for as long as it lasts, so "is anything wrong with
this resource?" is a single query — a condition whose `status` is `True` and whose `type` is not
`Ready`.

## Conditions and their reasons

Every condition a v3 resource carries, and the reasons it may report:

| Kind | Condition | `True` means | Reasons |
| ---- | --------- | ------------ | ------- |
| all three | `Ready` | the resource is usable as declared | `IsReady`. When `False`: `InvalidConfig`, `SecretNotFound`, `SecretMalformed`, `TokenRequestFailed`, and `RegistryDeleteUnsupported` on an `ImageMirror` |
| `ImageAlternative`, `ImageMirror` | `FallbackActive` | a rewrite is standing in for an origin that failed | `OriginUnavailable` |
| `ImageAlternative`, `ImageMirror` | `AlternativesExhausted` | a container was left untouched, no candidate having answered | `AllCandidatesFailed` |
| `ImageMirror` | `DestinationOutOfSync` | the destination does not hold the desired state yet | `MissingImages` |
| `ImageMonitor` | `ImagesUnavailable` | a tracked origin fails its check | `ChecksFailed` |
| `ImageMonitor` | `AlternativesUnavailable` | a monitored alternative fails its check | `ChecksFailed` |
| `ImageMonitor` | `ImagesDrifted` | a tracked tag moved upstream | `UpstreamDigestMoved` |

**`Ready` answers a question about the resource, never about an image.** It goes `False` when the
resource cannot work as declared: a `secretRef` naming a Secret that is absent or not a
`dockerconfigjson`, a `provider` whose `TokenRequest` is refused, a configuration a loop cannot
honour, or — with `cleanup.enabled` — a destination that refuses tag deletion.

A failure on one image never touches it. An `Unauthorized` on one repository says a credential does
not cover that path, not that the resource is broken — kuik does not extrapolate from a single
request ([Reasons](./observability.md#reasons)) — so it lands in `unavailableImages`,
`unavailableAlternatives` or `failedImageCopies`, and in the condition that names that anomaly.

## ImageAlternative

```yaml
status:
  # Gauge on living pods computed with informers
  pods:
    tracked: 123       # Number of pods selected by `podSelector` and `namespaceSelector`. Overlaps
                       # between CRs by design, so never sum it across them (see "Attribution" in spec.md)
    rewritten: 12      # Number of pods effectively rewritten (either by `OnFailure` or `Always` policy)
    noAlternatives: 2  # Number of pods left untouched as no alternatives image was available
    conceded: 1        # Number of pods where another mutating webhook replaced what KuiK had placed
  # Store the list of fallback images (only with `rewritePolicy: OnFailure`)
  activeFallbacks:
  - image: quay.io/thanos/thanos:v0.42.2
    routedTo: registry.tld/mirror/quay.io/thanos/thanos:v0.42.2
    pods: 12
    since: "2026-07-10T06:40:00Z"
  noAlternatives:
  - image: quay.io/thanos/thanos:v0.42.2-debug
    pods: 2
    since: "2026-07-11T07:27:36Z"
  # Rewrites this CR made and another mutating webhook overwrote. `image`, `routedTo` and the
  # attribution come from the pods' `kuik.enix.io/conceded-rewrites` annotation; `replacedBy` is read
  # from the live container, where the annotation deliberately leaves it (see "What conceding
  # removes" in architecture.md), and `since` is carried forward like `activeFallbacks.since`, the
  # annotation being untimestamped.
  concededRewrites:
  - image: quay.io/oauth2-proxy/oauth2-proxy:v7.7.1              # origin, as the metric labels it
    routedTo: registry.tld/mirror/quay.io/oauth2-proxy/oauth2-proxy:v7.7.1_cluster-a
    replacedBy: internal.tld/oauth2-proxy:v7.7.1                 # what the pod actually runs now
    pods: 1
    since: "2026-07-11T11:02:00Z"
  conditions:
  - type: Ready                   # Valid config and could read secrets (if provided)
    status: "True"
    reason: IsReady
  - type: FallbackActive          # True = KuiK avoided a pull error by rewriting an alternative image
    status: "True"
    reason: OriginUnavailable
    message: "1 image routed to fallback (12 pods)"
  - type: AlternativesExhausted   # True = KuiK could not find an available image in alternatives
    status: "True"                # Pod may start if image is cached on node, else it result in a pull error
    reason: AllCandidatesFailed
    message: "1 image unavailable (2 pods)"
```

## ImageMirror

With rewritePolicy != None, we also have the same status as ImageAlternative in addition to the following:

```yaml
status:
  images:
    desired: 312               # images used in running pod + retained ones carrying an `origin`
    copied: 309                # images effectively copied to destination registry
    retained: 2                # tags pending deletion (if cleanup.retention > 0), origin-less ones
                               # among them are held then deleted, never copied again
    drifted: 0                 # with driftPolicy=Warn or Sync - image tag whose upstream digest moved
                               # away from the copied one. Sync queues them for a resync, Warn leaves
                               # the copy as it is and only reports
    # platformsMissing: 8      # Meaningless in v3.0: every platform of a multi-platform image is
                                # always copied (see the note on `platforms` in ImageMirror). Comes
                                # back once per-platform selection ships, to report a copy that
                                # missed a platform it should have had
    missingSource: 1           # no source can supply the image any more: the `failedImageCopies`
                               # entries whose reason is `SourceNotFound`. Those entries are what
                               # name the images this counts
  # Tags whose upstream digest moved away from the copy held at the destination (`Warn` and `Sync`,
  # never `Ignore`). The bounded list behind `kuik_image_drifted`, and the counterpart of
  # `driftedImages` on ImageMonitor — that one compares the upstream against what the *cluster* runs,
  # this one against what the *destination* holds. `ref` is the origin reference in both, so the two
  # join without translation. Under `Sync` an entry is transient (the resync clears it); one that
  # persists means the resync is failing, not that a tag moved
  driftedImages:
  - ref: docker.io/acme/app:prod
    upstreamDigest: sha256:bbbb…
    copiedDigest: sha256:aaaa…
    since: "2026-07-11T04:15:00Z"
  failedImageCopies:
  - ref: quay.io/acme/tool:1.4
    # What was observed on that one request:
    # SourceNotFound (404),
    # PushRejected (the destination refused this manifest)
    # Unauthorized (401/403)
    # QuotaExceeded (429)
    # SourceUnreachable / DestinationUnreachable (the endpoint did not answer at all)
    reason: SourceNotFound
    lastAttempt: "2026-07-10T06:12:00Z"
  # Destination tags no longer referenced by any pod, held for `cleanup.retention` before being
  # deleted (if cleanup enabled). Fed both by pod events and by the tag listing every destination
  # pass starts with, so tags that stopped being used while the controller was down are collected at
  # startup. `unusedSince` is stamped when the entry appears and never refreshed afterwards.
  # `origin` is the reference the image was copied from, known when a pod event created the entry
  # and absent for a tag found by listing (the destination layout is one-way, see walkthrough 02)
  pendingDeletion:
  - ref: registry.tld/mirror/ghcr.io/acme/report-job:v42_cluster-a
    origin: ghcr.io/acme/report-job:v42
    unusedSince: "2026-07-10T02:00:00Z"
  - ref: registry.tld/mirror/quay.io/acme/tool:1.3_cluster-a
    unusedSince: "2026-07-11T09:30:00Z"
  # The destination is written by KuiK and carries no quota to spare, so no window applies to its
  # self-check: it runs whole, once per `mirror.destinationScan.interval` — a period between passes,
  # not a rate between requests — so there is no cursor and no cycle to persist (walkthrough 02, B.3)
  selfChecked: "2026-07-10T06:12:00Z"   # end of the last full comparison against the destination
  # Persist the list of repositories tracked by this CR as it cannot be recomputed if controller restart,
  # it's written before first push and removed when no tag tracked by this CR remains *for this cluster*:
  # when several clusters share a destination each one keeps its own local view, and the GC only ever
  # considers tags carrying its own `clusterID` suffix (see multi-cluster in spec.md)
  # It's used for GC, the same way as for Flux Kustomization status.inventory: https://fluxcd.io/flux/components/kustomize/kustomizations/#inventory
  # Each tag could be retrieved by `GET /v2/<repository>/tags/list` (from OCI Distribution spec)
  # so no need to store each of them
  repositories:
  - registry.tld/mirror/ghcr.io/acme/report-job
  - registry.tld/mirror/quay.io/acme/tool
  - registry.tld/mirror/quay.io/prometheus/prometheus
  - registry.tld/mirror/quay.io/thanos/thanos
  # Health of the drift check schedule, and only that: present when `driftPolicy` is `Warn` or `Sync`,
  # absent under `Ignore`. Re-reading an upstream tag is a read of a *source* host, so it takes that
  # host's check windows and turns a ring of its own, exactly like an ImageMonitor's — one ring per
  # (resource, host), cursor persisted so the lap resumes at its successor on restart (see
  # "Scheduling" in spec.md). The *destination* has no entry here and never will: it is written by
  # KuiK, carries no quota to spare, and its verification is the whole-pass self-check above, which
  # has a period rather than a lap and so has nothing to report here
  checks:
    registries:
    - registry: quay.io                    # a source host this mirror re-reads, never the destination
      cursor: quay.io/thanos/thanos        # last tag re-read, the ring resumes at its successor
      cycleStarted: "2026-07-10T05:00:00Z"
      # measured lap: how often each mirrored tag of this host is re-read for drift, hence the delay
      # before a `Warn` is reported or a `Sync` is queued. Absent until a first lap completes, and
      # not derived from `images.copied * interval` — every ring of a host shares its windows with
      # the ImageMonitors tracking it
      cycleDuration: 96h
  conditions:
  - type: DestinationOutOfSync # True = the destination does not hold the desired state (yet)
    status: "True"
    reason: MissingImages
    message: "3 images not copied yet"
  - type: Ready                # Conf valid and working credentials
    status: "True"
    reason: IsReady
    # status: "False", reason: RegistryDeleteUnsupported when cleanup.enabled and the destination
    # registry rejects tag deletion (e.g. responds 405 to DELETE /v2/<name>/manifests/<tag>) — cleanup
    # cannot make progress until this is fixed, see "Destination registry requirements" in spec.md
```

## ImageMonitor

```yaml
status:
  # Origin images seen on pods
  images:
    tracked: 3241               # images tracked by this CR
    inUse: 3180                 # images associated for running pod
    retained: 61                # images no longer running but still monitored for `unusedImageRetention`
    available: 3226             # 11 short of `tracked`: those have not been checked yet, the
    unavailable: 4              # ring not having reached them since they entered it
    drifted: 2                  # image tag have digest different than the upstream one (only with driftDetection=true)
  # Alternatives kuik would offer for a tracked image, from ImageAlternative entries and ImageMirror
  # destinations alike (only with monitorAlternatives=true)
  alternatives:
    tracked: 214
    unavailable: 2
  # Store retained images (ref+date+digest) as we can't recompute this information from informer
  retainedImages:
  - ref: ghcr.io/acme/report-job:v42
    unusedSince: "2026-07-10T02:00:00Z"
    digest: sha256:aaaa…
  # Negative check result with reason
  unavailableImages:
  - ref: docker.io/foo/bar:1.2
    reason: ManifestNotFound
    since: "2026-07-08T14:00:00Z"
    referencedBy: 3
  # `via` names the ImageAlternative the alternative came from. Mirror destinations never appear
  # here: a mirror verifies its own destination and reports it in its own status (see ImageMonitor
  # in spec.md)
  unavailableAlternatives:
  - ref: ghcr.io/thanos-io/thanos:v0.42.2
    derivedFrom: quay.io/thanos/thanos:v0.42.2
    via: "ImageAlternative/thanos[2]"
    reason: Unauthorized
  # Images with a running digest that differs from the upstream one (e.g. tag `latest` or similar).
  # Pods referencing the same tag can be pulled at different times, so more than one digest can be
  # running for the same ref at once (skew); runningDigests lists each one seen with its own pod count
  driftedImages:
  - ref: docker.io/acme/app:prod
    upstreamDigest: sha256:bbbb…
    runningDigests:
    - digest: sha256:aaaa…
      referencedBy: 5
    - digest: sha256:cccc…
      referencedBy: 2
  # Health of the check schedule: one image checked per `interval` window of a registry, taken from
  # this resource's own ring of that registry (see "Scheduling" in spec.md)
  checks:
    registries:
    - registry: docker.io
      # last checked image, so the ring resumes at its successor on controller restart
      cursor: docker.io/library/nginx
      # datetime of the current lap start (cursor back to where it started)
      cycleStarted: "2026-07-10T04:00:00Z"
      # measured duration of the last completed lap: how often each image of this registry comes
      # back, and the freshness this CR guarantees. Absent until a first lap completes. Not derived
      # from `tracked * interval`, as every ring of a registry shares its windows
      # a value too high is a signal to lower the registry `interval`
      cycleDuration: 1426h40m              # 2140 images, docker.io `interval: 40m`, sole consumer
    - registry: quay.io
      cursor: quay.io/thanos/thanos
      cycleStarted: "2026-07-10T03:20:00Z"
      # measured well above the 183h30m this ring would lap in alone: another ImageMonitor tracks
      # images of quay.io and takes some of its windows
      cycleDuration: 240h                  # 1101 images, quay.io `interval: 10m`
  conditions:
  - {type: Ready, status: "True", reason: IsReady}                            # Conf valid and working credentials
  - {type: ImagesUnavailable, status: "True", reason: ChecksFailed}              # a tracked image is unavailable
  - {type: AlternativesUnavailable, status: "True", reason: ChecksFailed}        # an alternative of a tracked image is unavailable
  - {type: ImagesDrifted, status: "True", reason: UpstreamDigestMoved}           # digest drift detected
```

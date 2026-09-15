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
| all three | `ListCapacityPressure` | a capped list is approaching or over its limit | `ListNearCapacity` from 80% of the cap, `ListTruncated` once entries are actually left out |
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

## Bounded lists

Every anomaly list below carries a **fixed cap on its number of entries**, the same for every list,
so no status object grows without limit.

Three things follow, and all three are part of the contract.

**Only anomaly lists are capped.** `unavailableImages`, `unavailableAlternatives`, `driftedImages`,
`failedImageCopies`, `activeFallbacks`, `noAlternatives`, `concededRewrites` and `staleRewrites` are
samples: the
matching [metric series](./observability.md#anomalies-signal-by-presence) carries every affected
image, so a status that hits the cap loses visibility and nothing else.

The operational inventories are **not** capped — `repositories`, `pendingDeletion`,
`checks.registries`, `retainedImages`. Truncating those loses correctness rather than visibility: a
repository missing from `repositories` is one the cleanup sweep never visits again, hence a permanent
leak ([walkthrough 02](./walkthroughs/02-imagemirror-reconciliation.md#cross-cutting-invariants)).
Their size follows the workload, and one approaching what etcd accepts per object is a capacity
problem to solve elsewhere — a cap would only hide it.

**The oldest entries are kept.** Truncation drops the newest, ordering on the entry's `since`, which
is stamped once and never refreshed — so the order is stable from one reconcile to the next and the
list does not churn. What stays visible is what has been wrong longest.

**Nothing is dropped silently.** `ListCapacityPressure` goes `True` from 80% of the cap, before anything
is lost, and a `truncated` map records what was left out once it is:

```yaml
status:
  # Present only once a cap was reached, keyed by list name: entries it left out
  truncated:
    unavailableImages: 12
```

Alerting reads it from `kuik_status_list_entries` and its two neighbours, in
[observability v3](./observability.md#metrics).

## Attribution

**Exactly one CR counts each rewritten, conceded or stale container.** That is the unit attribution
works in, and the `containers` block below reports it without loss: its five states partition the
containers of the pods a CR selects, and three of them sum across CRs.

- `containers.rewritten`, `containers.conceded` and `containers.stale` come off the `by` field of an
  entry of [`kuik.enix.io/rewrites`](./observability.md#annotations) or of `conceded-rewrites`. A
  conceded container leaves `rewrites` for `conceded-rewrites`
  ([what conceding removes](./architecture.md#what-conceding-removes)); a stale one stays in
  `rewrites` but its live reference no longer matches the `rewrittenTo` recorded there
  ([when a record goes stale](./architecture.md#when-a-record-goes-stale)), so it leaves
  `containers.rewritten` for `containers.stale` — the two never count the same container
- `containers.noAlternatives` is read from
  [`kuik.enix.io/no-alternatives`](./observability.md#annotations), which maps each container no
  candidate could serve to the resources that offered one. Several CRs may have offered, so this one
  does overlap between them — the count still comes off the pod rather than from replaying the
  matching, which a CR edited since admission would answer wrongly
- `containers.untouched` is the remainder: containers of selected pods that no annotation names

The `pods` block answers a different question, and **neither of its two fields sums across CRs**:

- `pods.tracked` counts the pods a CR's `podSelector` and `namespaceSelector` select. The overlap is
  deliberate: it answers "does this CR watch this pod?". It is emphatically not a claim of ownership —
  what a CR *did* is what the `containers` block reports. It stays the denominator `pods.rewritten` is
  read against **inside one CR**
- `pods.rewritten` counts the pods carrying at least one container this CR rewrote whose rewrite still
  stands. It is the reach of a CR where the `containers` block is its extent: a pod with three
  rewritten containers counts once here and three times there, and neither number derives from the
  other

**The origin of a container is the reference its spec declares, except where a standing kuik rewrite
put it there**: a container named by `kuik.enix.io/rewrites` whose live reference still matches the
entry's `rewrittenTo` takes that entry's `origin` instead.

- `untouched` and `noAlternatives` — no reference was ever rewritten, so the spec still declares the
  origin ([where a container appears](./observability.md#where-a-container-appears-says-what-happened-to-it))
- `conceded` — the live reference is the other webhook's, and that is the origin now; the
  `conceded-rewrites.origin` field is kept for reporting (`concededRewrites[].image`), not for
  attribution ([what conceding removes](./architecture.md#what-conceding-removes))
- `stale` — the record no longer describes what the pod runs
  ([when a record goes stale](./architecture.md#when-a-record-goes-stale))

The same test — does a container of a **non-terminal pod** (`Pending` or `Running`; every mention of
"pod" below means this) still carry this exact reference — also decides whether that reference counts
as `running` or `standby` below.

## ImageAlternative

```yaml
status:
  # Gauges on living pods and their containers, computed with informers
  pods:
    tracked: 123       # Number of pods selected by `podSelector` and `namespaceSelector`. Overlaps
                       # between CRs by design, so never sum it across them (see "Attribution" above)
    rewritten: 12      # Number of pods carrying at least one container this CR rewrote, whose rewrite
                       # still stands. The reach of the CR, where `containers` below is its extent
  containers:
    tracked: 281       # = untouched + rewritten + conceded + stale + noAlternatives
    untouched: 265     # No kuik annotation names them: the original answered and nothing was done
    rewritten: 13      # Rewritten by this CR (either by `OnFailure` or `Always` policy)
    conceded: 1        # Another mutating webhook replaced what KuiK had placed
    stale: 0           # Edited after admission, the record no longer describing what they run.
                       # Counted out of `rewritten`, never alongside it
    noAlternatives: 2  # Left untouched as no alternatives image was available
  # Store the list of fallback images (only with `rewritePolicy: OnFailure`)
  activeFallbacks:
  - image: quay.io/thanos/thanos:v0.42.2
    rewrittenTo: ghcr.io/thanos-io/thanos:v0.42.2
    pods: 12
    since: "2026-07-10T06:40:00Z"
  noAlternatives:
  - image: quay.io/thanos/thanos:v0.42.2-debug
    pods: 2
    since: "2026-07-11T07:27:36Z"
  # Rewrites this CR made and another mutating webhook overwrote. `image`, `rewrittenTo` and the
  # attribution come from the pods' `kuik.enix.io/conceded-rewrites` annotation; `replacedBy` is read
  # from the live container, where the annotation deliberately leaves it (see "What conceding
  # removes" in architecture.md), and `since` is carried forward like `activeFallbacks.since`, the
  # annotation being untimestamped.
  concededRewrites:
  - image: quay.io/oauth2-proxy/oauth2-proxy:v7.7.1              # origin, as the metric labels it
    rewrittenTo: registry.tld/mirror/quay.io/oauth2-proxy/oauth2-proxy:v7.7.1_cluster-a
    replacedBy: internal.tld/oauth2-proxy:v7.7.1                 # what the pod actually runs now
    pods: 1
    since: "2026-07-11T11:02:00Z"
  # Rewrites this CR made and something replaced *after* admission, the webhook running on CREATE
  # only. Same four fields as `concededRewrites` and read the same way, the difference being when it
  # happened and what fixes it: rolling the workload sends the pod back through admission. An entry
  # appearing here is what emits `RewriteStale`, once — which is why the list is persisted rather
  # than recomputed at every reconcile (see "When a record goes stale" in architecture.md)
  staleRewrites:
  - image: quay.io/thanos/thanos:v0.42.2
    rewrittenTo: ghcr.io/thanos-io/thanos:v0.42.2
    replacedBy: quay.io/thanos/thanos:v0.43.0                    # what the pod actually runs now
    pods: 1
    since: "2026-07-11T15:40:00Z"
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

An `ImageMirror` copies, and — with `rewritePolicy` other than `None` — routes as well. Its status
holds both, in that order: the copy side, specific to this kind, then the routing side, which is
field for field an `ImageAlternative`'s.

```yaml
status:
  # ============ Copy side, specific to an ImageMirror ============
  # The references this mirror holds at its destination. `copy` is the population name, the same way
  # an ImageMonitor nests `origin` and `alternatives` (see ImageMonitor below)
  images:
    copy:
      tracked: 312             # = running + standby + retained
      running: 305             # a container carries this exact destination reference
      standby: 5               # copied and held, but no container carries it: under
                               # `rewritePolicy: OnFailure` the origin still answers, and under
                               # `None` the mirror copies without ever routing — `running` is 0 there
                               # by construction
      retained: 2              # no pod declares the origin it derives from any more, kept for
                               # `cleanup.retention` (if cleanup.enabled)
      # A whole pass covers every tracked reference, so unlike an ImageMonitor's ring these two
      # always add up to `tracked`
      available: 309           # held by the destination, as of the last self-check
      unavailable: 3           # not copied yet, or whose copy is failing
      drifted: 0               # with driftPolicy=Warn or Sync - image tag whose upstream digest moved
                               # away from the copied one. Sync queues them for a resync, Warn leaves
                               # the copy as it is and only reports
      # platformsMissing: 8    # Meaningless in v3.0: every platform of a multi-platform image is
                               # always copied (see the note on `platforms` in ImageMirror). Comes
                               # back once per-platform selection ships, to report a copy that
                               # missed a platform it should have had
      missingSource: 1         # no source can supply the image any more: the `failedImageCopies`
                               # entries whose reason is `SourceNotFound`. Those entries are what
                               # name the images this counts
      orphanTags: 3            # entries of `pendingDeletion` carrying no `origin`: destination tags
                               # the sweep found that no tracked reference accounts for. Counted in
                               # tags rather than references — an anchor is a tag — hence outside
                               # `tracked`, and hence the unit in the name
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
    since: "2026-07-09T22:41:00Z"        # first failure, stamped once
    lastAttempt: "2026-07-10T06:12:00Z"  # refreshed at every retry
  # Destination tags no longer referenced by any pod, held for `cleanup.retention` before being
  # deleted (if cleanup enabled). Fed both by pod events and by the tag listing every destination
  # pass starts with, so tags that stopped being used while the controller was down are collected at
  # startup. `unusedSince` is stamped when the entry appears and never refreshed afterwards.
  # `origin` is the reference the image was copied from, known when a pod event created the entry
  # and absent for a tag found by listing (the destination layout is one-way, see walkthrough 02).
  # Entries carrying one are counted in `images.copy.retained`, those without in
  # `images.copy.orphanTags`
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
      images: 41                           # size of the ring: the copied tags of this host it re-reads
      cursor: quay.io/thanos/thanos:v0.42.2  # last tag re-read, ring resumes at its successor
      cycleStarted: "2026-07-10T05:00:00Z"
      # measured lap: how often each mirrored tag of this host is re-read for drift, hence the delay
      # before a `Warn` is reported or a `Sync` is queued. Absent until a first lap completes, and
      # not derived from `images * interval` — this ring would lap in 41m alone, and shares quay.io's
      # windows with the ImageMonitors tracking it
      cycleDuration: 2h

  # ============ Routing side, the same fields as an ImageAlternative ============
  # Each one means exactly what it means there, and is documented there rather than twice. What
  # differs is only that this kind routes to its own destination. Under `rewritePolicy: None` an
  # ImageMirror copies without ever routing, so none of these are populated
  pods:
    tracked: 480
    rewritten: 455
  containers:
    tracked: 1104
    untouched: 620
    rewritten: 481
    conceded: 1
    stale: 1
    noAlternatives: 1
  # `rewrittenTo` is this mirror's own destination — where an ImageAlternative names one of its upstream
  # entries instead
  activeFallbacks:
  - image: quay.io/thanos/thanos:v0.42.2
    rewrittenTo: registry.tld/mirror/quay.io/thanos/thanos:v0.42.2_cluster-a
    pods: 12
    since: "2026-07-10T06:40:00Z"
  # The mirror had nothing to offer either: no source ever answered for this image, so it was never
  # copied (`images.copy.missingSource` above) and the origin is gone too
  noAlternatives:
  - image: quay.io/acme/gone:1.0
    pods: 1
    since: "2026-07-09T22:41:00Z"
  concededRewrites:
  - image: quay.io/oauth2-proxy/oauth2-proxy:v7.7.1
    rewrittenTo: registry.tld/mirror/quay.io/oauth2-proxy/oauth2-proxy:v7.7.1_cluster-a
    replacedBy: internal.tld/oauth2-proxy:v7.7.1
    pods: 1
    since: "2026-07-11T11:02:00Z"
  staleRewrites:
  - image: quay.io/thanos/thanos:v0.42.2
    rewrittenTo: registry.tld/mirror/quay.io/thanos/thanos:v0.42.2_cluster-a
    replacedBy: quay.io/thanos/thanos:v0.43.0
    pods: 1
    since: "2026-07-11T15:40:00Z"

  # ============ Conditions, both sides ============
  conditions:
  - type: DestinationOutOfSync # True = the destination does not hold the desired state (yet)
    status: "True"
    reason: MissingImages
    message: "3 images not copied yet"
  - type: Ready                # Valid configuration and working credentials
    status: "True"
    reason: IsReady
    # status: "False", reason: RegistryDeleteUnsupported when cleanup.enabled and the destination
    # registry rejects tag deletion (e.g. responds 405 to DELETE /v2/<name>/manifests/<tag>) — cleanup
    # cannot make progress until this is fixed, see "Destination registry requirements" in spec.md
  # The routing ones, identical to an ImageAlternative's and absent under `rewritePolicy: None`
  - type: FallbackActive
    status: "True"
    reason: OriginUnavailable
    message: "1 image routed to the mirror (12 pods)"
  - type: AlternativesExhausted
    status: "True"
    reason: AllCandidatesFailed
    message: "1 image unavailable (1 pod)"
```

## ImageMonitor

```yaml
status:
  # The two populations this CR tracks, each under its own name. `tracked`, `available` and
  # `unavailable` are properties of the reference itself; `retained` is inherited from the origin, an
  # alternative being tracked because its origin is, never for itself. `running` and `standby` are not
  # inherited: an alternative is `running` only where it is the one actually replacing its origin, and
  # an origin is `standby` precisely because one of its alternatives is `running`
  images:
    # Origin images declared by pods (see "Attribution" above for what counts as the origin)
    origin:
      tracked: 3241      # = running + standby + retained
      running: 3168      # a container carries this exact reference
      standby: 12        # a pod still declares it, but kuik routed the container elsewhere. Tracked
                        # and checked for exactly that: its return is what lets the fallback be lifted
      retained: 61       # no pod declares it any more, kept for `unusedImageRetention`
      # `available` + `unavailable` is 11 short of `tracked`: the ring has not reached those yet
      available: 3226
      unavailable: 4
      drifted: 2                # image tag have digest different than the upstream one (only with driftDetection=true)
    # Alternatives kuik would offer for a tracked image, from ImageAlternative entries
    # (only with monitorAlternatives=true)
    alternatives:
      tracked: 214
      running: 9         # the one actually standing in for its origin. Smaller than `origin.standby`
                         # above: some of those origins were routed to an ImageMirror destination
                         # instead, which `monitorAlternatives` never tracks (see ImageMonitor in
                         # spec.md)
      standby: 201       # checked candidates that were never served
      retained: 4        # the origin they derive from is `retained`
      # `available` + `unavailable` is 1 short of `tracked`, as for `origin` above
      available: 211
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
    pods: 3
  # `via` names the ImageAlternative the alternative came from. Mirror destinations never appear
  # here: a mirror verifies its own destination and reports it in its own status (see ImageMonitor
  # in spec.md)
  unavailableAlternatives:
  - ref: ghcr.io/thanos-io/thanos:v0.42.2
    derivedFrom: quay.io/thanos/thanos:v0.42.2
    via: "ImageAlternative/thanos"
    reason: Unauthorized
    since: "2026-07-08T09:12:00Z"
  # Images with a running digest that differs from the upstream one (e.g. tag `latest` or similar).
  # Pods referencing the same tag can be pulled at different times, so more than one digest can be
  # running for the same ref at once (skew); runningDigests lists each one seen with its own pod count
  driftedImages:
  - ref: docker.io/acme/app:prod
    upstreamDigest: sha256:bbbb…
    since: "2026-07-11T04:15:00Z"
    runningDigests:
    - digest: sha256:aaaa…
      pods: 5
    - digest: sha256:cccc…
      pods: 2
  # Health of the check schedule: one image checked per `interval` window of a registry, taken from
  # this resource's own ring of that registry (see "Scheduling" in spec.md)
  checks:
    registries:
    - registry: docker.io
      images: 2140                         # size of the ring: images of this registry it tracks
      # last checked image, so the ring resumes at its successor on controller restart
      cursor: docker.io/library/nginx:1.27
      # datetime of the current lap start (cursor back to where it started)
      cycleStarted: "2026-07-10T04:00:00Z"
      # measured duration of the last completed lap: how often each image of this registry comes
      # back, and the freshness this CR guarantees. Absent until a first lap completes. Not derived
      # from `tracked * interval`, as every ring of a registry shares its windows
      # a value too high is a signal to lower the registry `interval`
      cycleDuration: 35h40m                # 2140 images, docker.io `interval: 1m`, sole consumer
    - registry: quay.io
      images: 1101
      cursor: quay.io/thanos/thanos:v0.42.2
      cycleStarted: "2026-07-10T03:20:00Z"
      # measured three times the 18h21m this ring would lap in alone: another ImageMonitor and an
      # ImageMirror's drift ring take their share of quay.io's windows
      cycleDuration: 55h                   # 1101 images, quay.io `interval: 1m`
  conditions:
  - {type: Ready, status: "True", reason: IsReady}                            # Valid configuration and working credentials
  - {type: ImagesUnavailable, status: "True", reason: ChecksFailed}              # a tracked image is unavailable
  - {type: AlternativesUnavailable, status: "True", reason: ChecksFailed}        # an alternative of a tracked image is unavailable
  - {type: ImagesDrifted, status: "True", reason: UpstreamDigestMoved}           # digest drift detected
```

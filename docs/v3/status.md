# status v3

We only persist aggregates, anomalies and information that could not be recomputed from informer (or too costly). This way status is human readable and show only usable information (e.g. which image is unavailable). We don't need to persist a large number of information that could be rebuilt on pod restart.

Status is only computed via informer on leader elected controller, nothing is updated directly in mutating webhook.

## ImageAlternative

```yaml
status:
  # Gauge on living pods computed with informers
  pods:
    tracked: 123       # Number of pods this CR could apply to
    rewritten: 12      # Number of pods effectivly rewritten (either by `OnFailure` or `Always` policy)
    noAlternatives: 2  # Number of pods left untouched as no alternatives image was available
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
  conditions:
  - type: Ready               # Valid config and could read secrets (if provided)
    status: "True"
  - type: NoActiveFallback    # False = KuiK avoided a pull error by rewriting an alternative image
    status: "False"
    message: "1 image routed to fallback (12 pods)"
  - type: NoAlternatives      # True = KuiK could not find an available image in alternatives
    status: "True"            # Pod may start if image is cached on node, else it result in a pull error
    message: "1 image unavailable (2 pods)"

```

## ImageMirror

With rewritePolicy != None, we also have the same status as ImageAlternative in addition to the following:

```yaml
status:
  images:
    desired: 312               # images used in running pod + retained
    copied: 309                # images effectivly copied to destination registry
    retained: 1                # image pending deletion (if cleanup.retention > 0)
    drifted: 0                 # with driftPolicy=Sync - image tag with new digest that will be resynced
    platformsMissing: 8        # copied but image miss a platform (multi-arch)
    missingSource: 1           # no source available to copy image to destination (if not already copied)
  failedImagesCopy:
  - ref: quay.io/acme/tool:1.4
    reason: SourceUnavailable  # SourceUnavailable | QuotaExceeded | AuthFailed | PushFailed
    lastAttempt: "2026-07-10T06:12:00Z"
  # persist image no longer referenced for `cleanup.retention` before
  # removing them (if cleanup enabled)
  pendingDeletion:
  - ref: ghcr.io/acme/report-job:v42
    unusedSince: "2026-07-10T02:00:00Z"
  selfCheck:
    # last checked repository, so the ring resumes at the same position on controller restart
    cursor: registry.tld/mirror/quay.io/thanos/thanos
    # datetime of last cycle start (new ring iteration)
    cycleStarted: "2026-07-10T06:00:00Z"
    # repositories * interval: how often each repository of the destination comes back
    cycleDuration: 40m           # 4 repositories, default check `interval: 10m`
  # Persist the list of repositories tracked by this CR as it cannot be recomputed if controller restart,
  # it's written before first push and removed when no tag tracked by this CR
  # It's used for GC, the same way as for Flux Kustomization status.inventory: https://fluxcd.io/flux/components/kustomize/kustomizations/#inventory
  # Each tag could be retrieved by `GET /v2/<repository>/tags/list` (from OCI Distribution spec)
  # so no need to store each of them
  repositories:
  - registry.tld/mirror/ghcr.io/acme/report-job
  - registry.tld/mirror/quay.io/acme/tool
  - registry.tld/mirror/quay.io/prometheus/prometheus
  - registry.tld/mirror/quay.io/thanos/thanos
  conditions:
  - type: DestinationInSync    # Destination registry in desired state
    status: "False"
    reason: MissingImages
  - type: Ready                # Conf valid and working credentials
    status: "True"
```

## ImageMonitor

```yaml
status:
  # Images seen on pods
  images:
    tracked: 3241               # images tracked by this CR
    inUse: 3180                 # images associated for running pod
    retained: 61                # images no longer running but still monitored for `unusedImageExpiry`
    available: 3226
    unavailable: 4
    drifted: 2                  # image tag have digest different than the upstream one (only with driftDetection=true)
  # Images from alternatives matching a running pod (only with monitorAlternatives=true)
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
  unavailableAlternatives:
  - ref: ghcr.io/thanos-io/thanos:v0.42.2
    derivedFrom: quay.io/thanos/thanos:v0.42.2
    via: "ImageAlternative/thanos[2]"
    reason: Unauthorized
  # Images with running digest differ from upstream one (e.g. tag `latest` or similar)
  driftedImages:
  - ref: docker.io/acme/app:prod
    runningDigest: sha256:aaaa…
    upstreamDigest: sha256:bbbb…
    referencedBy: 7
  # Health of the check schedule: one image checked per aligned `interval` window, per registry
  # (see "Scheduling" in spec.md)
  checks:
    registries:
    - registry: docker.io
      # last checked image, so the ring resumes at the same position on controller restart
      cursor: docker.io/library/nginx
      # datetime of last cycle start (new ring iteration)
      cycleStarted: "2026-07-10T04:00:00Z"
      # tracked * interval: how often each image of this registry comes back
      # a value too high is a signal to lower the registry `interval`
      cycleDuration: 1426h40m              # 2140 images, docker.io `interval: 40m`
    - registry: quay.io
      cursor: quay.io/thanos/thanos
      cycleStarted: "2026-07-10T03:20:00Z"
      cycleDuration: 183h30m               # 1101 images, default `interval: 10m`
  conditions:
  - {type: AllImagesAvailable, status: "False"}        # a tracked image is unavailable
  - {type: AllAlternativesAvailable, status: "False"}  # an alternative of tracked image is unavailable
  - {type: NoImageDrift, status: "False"}              # digest drift detected
```

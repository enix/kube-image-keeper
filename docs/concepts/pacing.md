---
description: How kuik paces what it reads from the registries it pulls from, so that it never adds to the rate limits it protects you from.
---

# Registry pacing

kuik reads source registries in the background: an `ImageMonitor` checks the images it tracks, an
`ImageMirror` copies the images it owes and, under `driftPolicy: Warn` or `Sync`, re-reads the tags
it copied. Every one of these reads is paced per registry host, by
[`registries`](../configuration.md#keys), so that kuik never adds to the rate limit of a registry
that rations.

The webhook's active check is not paced: it always runs, bounded by
`webhook.availabilityCheck.timeout`. Nor is a mirror destination, which kuik owns: it is read
whole once per `mirror.destinationScan.interval`.

## One image per window

Each host has 2 series of windows, one for checks (`check.interval`) and one for copies
(`copy.interval`). A window takes one image.

Windows open at fixed times counted from the moment the reconciler takes its lease: leading from
13:32 with `interval: 5m`, it opens them at 13:37, 13:42, 13:47 and so on. The time an image takes never moves
the next window, and a window that finds nothing to do, or opens while the previous image is still
being read, is lost rather than saved for later. A host is therefore asked for at most one image
per `interval`, whatever happens.

The first window is a full `interval` away, so a reconciler that restarts in a loop sends nothing,
and a standby taking over never fires on the times of the leader it replaces.

## One budget per host

The windows belong to the host, not to the resource. Every `ImageMonitor` tracking images on
`docker.io` and every `ImageMirror` re-reading tags there take turns on the same check windows, and
every mirror pulling from `docker.io` takes turns on its copy windows. Adding a resource shares the
budget; it never raises it.

## Rings and laps

Each resource holds a **ring** per host: the images it reads there, in lexicographic order. An
`ImageMonitor` under `monitorAlternatives` reads its alternatives there too, a reference that is
both an origin and an alternative once. Each turn it gets takes the next image of its ring, so
every image comes back once per **lap**.

The status reports each ring under `checks.registries`: its size (`images`), the image last checked
(`cursor`), when the lap in progress started (`cycleStarted`), and how long the last one took
(`cycleDuration`). A restart resumes after the cursor, and the lap counts the downtime.

`cycleDuration` is measured, not computed: it is the freshness the resource guarantees, every
image checked at least once per `cycleDuration`. A resource alone on its host laps in
`images × interval`, 60 images on `interval: 1m` in an hour. Sharing the host, restarts and images
kept by `unusedImageRetention` all lengthen it. A lap that grows too long is a reason to lower the
host's `check.interval`.

## One request serves every resource

When 2 resources track the same image, kuik asks the registry once. The response, verdict and
digest, is kept, and the next resource to reach that image reuses it instead of spending a window.

2 monitors tracking the same 1000 images on `interval: 1m` lap in 1000 minutes, not 2000: the
registry sees the 1000 images once per lap, whatever the number of resources tracking them. An
`ImageMonitor` and an `ImageMirror` with `driftPolicy: Sync` on the same tags share the same
requests too.

A resource reuses only a response newer than its own previous read of that image, so a reused
answer is never older than its lap: the freshness its status reports still holds.

## Copies

Copies drain a queue rather than turn a ring: each `ImageMirror` queues the images it still owes
from a host, and the mirrors sharing that host take its copy windows in turn. An image whose copy
fails never holds back the images queued behind it: it is retried one `copy.interval` after the
window it failed in, then twice as long after each new failure, up to 30 minutes. Meanwhile its
windows go to the other images of the queue, then to the other mirrors of the host. The wait is
held in memory and lost on restart, as the kubelet's image pull backoff is.

A re-copy, an image whose manifest disappeared from the destination, goes first: it takes the next
copy window of its source host, ahead of the initial copies and of the other mirrors' turns, and on
each host it falls back to. Once it failed on every source, or the destination refused it, it is
retried in turn with the others, with the same wait, until it succeeds, so a re-copy that keeps
failing never starves the initial copies. A tag written back over a manifest still at the
destination is no re-copy and waits for its turn.

`copy.timeout: 0`, the default, lets a copy take as long as it needs: a copy that outlasts its
`copy.interval` already shows as copy windows going unused while images are pending.

## Spacing the requests

A window bounds the images kuik reads, not the HTTP requests one image takes: a check sends up to
3 (the ping, a token, the manifest `HEAD`), and a copy one per manifest and blob, a multi-arch
image several dozen in a row. A registry that rations requests per second refuses the rest with a
`429`. `requestInterval` spaces them: each request the reconciler sends to the source host waits
until `requestInterval` has passed since the previous one.

- **What waits**: every request to the source host itself, from checks and copies alike: the
  ping, a token asked to that host, manifests, child manifests and blobs.
- **What does not**: a request to another host (a blob redirected to a CDN, a token service such
  as `auth.docker.io`), every request to a mirror destination, and the webhook's active check.
- **No burst**: one request per gap, never several at once. The requests that wait go in the
  order they asked: once the one ahead of it has left or given up, a request leaves as soon as
  `requestInterval` has passed since the last request sent. The checks and copies of every
  resource on that host share one series.
- **Default ports**: the `registries` block is looked up with the host exactly as the image
  writes it, as for `check` and `copy`: an image on `registry.example:443` takes the
  `requestInterval` of a `registry.example:443` block, not of `registry.example`. The requests
  themselves queue together whether they name the default port or not, `:443` over HTTPS and
  `:80` over plain HTTP: a blob redirected to the source host with its port written out still
  waits its turn. When images write a host both ways, give both blocks the same
  `requestInterval`, or write the block as the images write the host: a spelling that resolves
  to `0` sends its requests at once, even while those of the other spelling wait.
- **One request at a time per copy**: a copy from a paced host reads and writes its blobs one by
  one instead of several in parallel, so a check of the same host waits behind at most one request
  of the copy before each of its own: its 3 requests wait up to 2 turns each. An index that nests
  another index is the exception: its copy can have 2 source requests in flight.
- **The wait counts against the timeouts**: it runs within `check.timeout` and `copy.timeout`. A
  request still waiting when its timeout ends is never sent, and the check or copy fails as that
  timeout does. `kuik_registry_requests_total` counts attempts, not HTTP requests: an attempt
  none of whose requests was sent is not counted, but a check whose ping was sent and whose
  `HEAD` gave up waiting is. The request that gives up leaves the queue, and the next one goes
  `requestInterval` after the last request sent: only requests sent count, so a timeout never
  lets the host burst.
- **Size `check.timeout` for the wait**: kuik does not check that it leaves room for
  `requestInterval`. Give a paced host a `check.timeout` of at least 6 times its
  `requestInterval` per credential tried, since a check tries each credential of the host in
  turn, up to 3 requests each, all within one `check.timeout`. Give it more for an index that
  nests another index, or to leave room for retries. A timeout too short for the wait fails the
  checks of that host as `Unreachable`.

The kubelet paces image pulls per node with a token bucket (`registryPullQPS`, `registryBurst`).
kuik spaces requests instead, with no burst on purpose: the burst a registry allows is left to the
nodes' own pulls.

For example, `public.ecr.aws` allows about 1 anonymous request per second, with a burst of about
10. A copy of a multi-arch image such as `busybox` sends some 60 requests, so it fails with
`QuotaExceeded` after about 10 of them. With:

```yaml
registries:
  public.ecr.aws:
    requestInterval: 1s
```

the same copy succeeds in about a minute. The `copy.timeout` of that host must allow it: the
default, `0`, sets no bound.

## Changing the pace

The config is reloaded in place. A reload that changes the `check.interval` of a host restarts the
check windows of that host only, the first one a full new `interval` after the reload, as a start
does; `copy.interval` does the same for its copy windows. Every other series keeps its times, a
changed `timeout` applies from the next image without moving any window, and no cursor moves.

A changed `requestInterval` applies at once, to the requests already waiting too: each one waits
the new gap after the last request sent, so a lower one lets them go sooner, a higher one later,
and `0` sends every waiting request at once. They keep their order. A copy already running keeps
its parallelism, one request at a time or several, until it ends or tries its next destination
credential.

Editing the config in a loop therefore never bursts a registry: each change pushes the next window
further away.

## Several clusters

An `interval` bounds what one cluster sends. A registry counts its quota per identity: the source IP
for anonymous pulls, the account behind the credential otherwise.

> [!WARNING]
> Clusters sharing an egress IP or a credential share the quota behind it: `N` clusters on
> `interval: 1m` send `N` requests per minute. Multiply the intervals of that host by `N`, or give
> each cluster its own credential or egress path.

## Watching the pace

The reconciler exports each ring's lap, size and the pace of each host, listed in
[Scheduling health](../observability.md#scheduling-health).

`kuik_check_cycle_duration_seconds / (kuik_check_ring_images * on(registry) group_left
kuik_registry_interval_seconds{operation="Check"})` is how much longer than its best case a lap
takes: above 1, the ring shares its host or loses windows.

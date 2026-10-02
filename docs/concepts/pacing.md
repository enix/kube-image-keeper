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

Each resource holds a **ring** per host: the images it reads there, in lexicographic order. Each
turn it gets takes the next image of its ring, so every image comes back once per **lap**.

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
`ImageMonitor` with `driftDetection` and an `ImageMirror` with `driftPolicy: Sync` on the same tags
share the same requests too.

A resource reuses only a response newer than its own previous read of that image, so a reused
answer is never older than its lap: the freshness its status reports still holds.

## Copies

Copies drain a queue rather than turn a ring: each `ImageMirror` queues the images it still owes
from a host, and the mirrors sharing that host take its copy windows in turn. An image whose copy
fails is retried on a later turn and never holds back the images queued behind it.

`copy.timeout: 0`, the default, lets a copy take as long as it needs: a copy that outlasts its
`copy.interval` already shows as copy windows going unused while images are pending.

## Changing the pace

The config is reloaded in place. A reload that changes the `check.interval` of a host restarts the
check windows of that host only, the first one a full new `interval` after the reload, as a start
does; `copy.interval` does the same for its copy windows. Every other series keeps its times, a
changed `timeout` applies from the next image without moving any window, and no cursor moves.

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

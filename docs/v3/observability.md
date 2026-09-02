# observability v3

[status v3](./status.md) defines what each resource reports about itself. This document covers the
three other channels — **pod annotations**, **events** and **metrics** — and the rule that decides
which one a given piece of information belongs to.

| Channel | Holds | Lifetime | Answers |
| ------- | ----- | -------- | ------- |
| Status | the current state of a resource: aggregates and the anomalies behind them | as long as the object | "what is wrong right now, and with what" |
| Annotations | what the webhook decided for one pod, container by container | as long as the pod | "what did kuik do to *this* pod, and why" |
| Events | transitions: the moment something changed | apiserver `event-ttl` duration (1h by default) | "what just happened, and to which object" |
| Metrics | the same aggregates over time, plus monotonic counters | the TSDB's retention | "how often, since when, at what rate" |

The dividing rule is that a datum goes where its **shape** fits, not where it is convenient. A
current value belongs in a status, which is recomputed and bounded — or on the pod it concerns, when
it concerns exactly one pod. A change of value belongs in an
event, which is emitted once and expires. A cumulative count belongs in a metric, which survives
restarts and sums correctly across replicas — a counter in a status could answer none of "since
when", "reset by what", "summed how".

Anomalies are the one thing that legitimately appears twice: as a bounded list in the status, so an
operator reading the object sees what is wrong, and as a metric, so alerting can fire on it without
parsing YAML.

## Reasons

Every check and every copy is a request about **one image**. That is the only thing kuik ever
observes, and every `reason` it reports — in a status entry, on an event, on a metric label — has to
be read in that light. One vocabulary covers all of them, so that a cause reads the same wherever it
surfaces:

| What was observed | Reason on a check | Reason on a copy | About |
| ----------------- | ----------------- | ---------------- | ----- |
| a `404` on that manifest | `ManifestNotFound` | `SourceNotFound` | that image |
| a `401` or `403` on that request | `Unauthorized` | `Unauthorized` | that image, and whatever path the credential covers |
| a `429` on that request | `QuotaExceeded` | `QuotaExceeded` | that image, and whatever the registry meters |
| the endpoint did not answer at all: DNS, connection refused, TLS, timeout | `Unreachable` | `SourceUnreachable` / `DestinationUnreachable` | that request, and whatever else the endpoint serves |
| the destination refused that manifest: size, media type, a repository policy | — | `PushRejected` | that image |

A check touches one endpoint where a copy touches two, and that is the whole of the naming
difference: when both sides can fail the same way, the reason names the side that did.

The *About* column is how far an observation reaches, and the answer is never further than the request
it came from. **kuik must not extrapolate.** A `401` usually says a credential is missing for one
project, not that the registry is closed — credentials are commonly scoped to a sub-path. A `404` on
every image of `quay.io/acme/` says those images are gone, while the rest of `quay.io` serves normally.
Even a quota can be metered per repository rather than per registry. kuik checks the images it was told
about and nothing else, so it has no basis for a verdict on everything it did not check.

**kuik states facts, the operator draws the conclusion.** "That registry is down", "that project was
deleted", "our account is throttled" are inductions over many signals — several images, several passes,
often several clusters — and they need a judgement kuik has no standing to make. What kuik owes in
return is signals that are cheap to induct from, which is what the metrics are for:
`kuik_registry_requests_total` counts, per host, the requests **kuik itself sent** and how each one
turned out. That is a fact about kuik's own traffic rather than a verdict on the registry, which is
exactly why counting is legitimate at a level where reporting a verdict is not. Nothing kuik reports
*per image* may pretend to more.

### Where a reason surfaces

The same word appears on up to three channels; what differs between conditions is only which of them
are worth using:

| Condition | Status | Metric | Event |
| --------- | ------ | ------ | ----- |
| a tracked image fails its check | `ImageMonitor.status.unavailableImages[].reason` | `kuik_image_unavailable{reason}` | none, deliberately — see [Why availability is not evented](#why-availability-is-not-evented) |
| a monitored alternative fails its check | `ImageMonitor.status.unavailableAlternatives[].reason` | `kuik_image_unavailable{source="Alternative", reason}` | `AlternativeUnusable`, and only when the cause has a remedy |
| a copy fails | `ImageMirror.status.failedImagesCopy[].reason` | `kuik_mirror_image_failed{reason}` | `ImageCopyFailed`, coalesced by prefix — see [below](#a-copy-failure-is-coalesced-never-generalised) |

An unavailable image and a failed copy are the same observation about the same kind of request; they
differ in what an operator can do about it, which is why one emits and the other does not. The reason
itself carries no such judgement.

## Annotations

These ride along in the **mutation the webhook returns**, the patch travels back in the `AdmissionResponse` and the API server applies it to the object it was already writing.

Five annotations. Four are JSON objects keyed by **container name** — a pod has many containers and
each one is decided independently — and the fifth is a JSON list of container names:

```yaml
metadata:
  annotations:
    # Original reference of each container whose image was replaced, so nothing ever has to be
    # un-computed from a rewritten reference
    kuik.enix.io/original-images: '{"prometheus":"quay.io/prometheus/prometheus:v3.13.1-distroless","thanos-sidecar":"quay.io/thanos/thanos:v0.42.2"}'
    # Which resource supplied the retained reference, as `<kind>/<name>`
    kuik.enix.io/rewritten-by: '{"prometheus":"ImageAlternative/prometheus","thanos-sidecar":"ImageMirror/prod-mirror"}'
    # Under which policy each rewrite happened
    #   Always:    a `rewritePolicy: Always` resource asked for the rewrite
    #   OnFailure: the original did not answer, rewritten to the first candidate that did
    kuik.enix.io/reason: '{"prometheus":"Always","thanos-sidecar":"OnFailure"}'
    # Rewrites kuik withdrew: another mutating webhook replaced the reference kuik had placed, and
    # kuik stood down rather than write over it. Holds the three things the pod no longer shows —
    # the origin, the reference kuik had placed, the resource that supplied it
    kuik.enix.io/conceded-rewrites: '{"oauth-proxy":{"from":"quay.io/oauth2-proxy/oauth2-proxy:v7.7.1","was":"registry.tld/mirror/quay.io/oauth2-proxy/oauth2-proxy:v7.7.1_cluster-a","by":"ImageMirror/prod-mirror"}}'
    # Containers no candidate could serve, left untouched. A list, not a map: there is no reference
    # to preserve and no resource to attribute, only the fact that kuik had nothing to offer
    kuik.enix.io/no-alternatives: '["config-reloader"]'
```

### Where a container appears says what happened to it

Four outcomes, four disjoint places to look:

- a container kuik **left alone because the original answered** appears nowhere. Nothing happened, so
  there is nothing to record — which is why a pod with no kuik annotation at all is the normal case
  under `OnFailure`, and why the status controllers fall back to the live container image for such pods
  ([Attribution](./spec.md#attribution)).

  This covers `Always` too, and deliberately. `Always` does not promise a particular candidate: it
  moves the resource's candidates *ahead* of the original in one list that is still probed in order
  ([Candidate ordering](./spec.md#candidate-ordering)), so landing on the original because everything
  before it declined is the same nominal outcome, reached by the same rule. That is why
  `activeFallbacks` stays `OnFailure`-only — it records "the original failed", a fact about the
  original, not "a preferred candidate failed" — and why nothing else records it either. What the
  candidates answered is not a property of the pod; an alternative that stopped answering is an
  `ImageMonitor` concern (`status.unavailableAlternatives`, `AlternativeUnusable`), and a destination
  that stopped answering is an `ImageMirror` concern (`status.failedImagesCopy`, `DestinationOutOfSync`)
- a container that was **rewritten** appears in the three maps: the reference it came from, the
  resource that supplied the new one, and under which policy
- a container **no candidate could serve** appears in `no-alternatives`, and in none of the maps. Its
  spec was not touched, so the original is still the live image and there is nothing to preserve; and
  no resource supplied anything, so there is nobody to attribute it to. It is recorded all the same,
  because "kuik tried and had nothing to offer" is what `pods.noAlternatives` and
  `kuik_no_alternatives_total` count, and without it the container would be indistinguishable from one
  kuik never looked at
- a container kuik rewrote and then **conceded** appears in `conceded-rewrites`, and in none of the
  other maps. Another mutating webhook replaced the reference kuik had placed and kuik stood down
  rather than write over it
  ([what conceding removes](./architecture.md#what-conceding-removes)), so the entry holds
  what the pod no longer shows anywhere: the origin, the reference kuik had placed, and the resource
  that supplied it. Being out of the other three maps is exactly what makes the container invisible
  to attribution, to the syncer and to the mirror — for all of them it is one kuik never touched,
  which is what it now is

### Why the record lives on the pod

`original-images` is the **only** way back to the origin reference. A mirror reference cannot be
un-computed — the destination layout is one-way, long tags being truncated and hashed
([walkthrough A.3](./walkthroughs/02-imagemirror-reconciliation.md#a3-resolve-the-origin-reference))
— and re-deriving it by replaying the matching would give the wrong answer as soon as a resource is
edited between admission and reconcile. Recording it verbatim removes the question. The same reason
keeps `from` on a conceded entry rather than dropping it with the rest: a rewrite kuik gave up still
had an origin, and it is the one part of the story the pod would otherwise hold no trace of.

`rewritten-by` is what makes attribution disjoint — one resource owns each rewritten container, so the
`pods` gauges of different resources never double-count ([Attribution](./spec.md#attribution)) — and it
is also what the secret syncer watches to learn that an `OnFailure` resource is being used for real
([walkthrough 03](./walkthroughs/03-secret-syncer-reconciliation.md)).

### `reason` here is not a reason from the table above

Two vocabularies share the word, at two different levels, and neither is a substitute for the other:

- the [shared vocabulary](#reasons) says why one **request** failed
- `kuik.enix.io/reason` says under which policy a container was **rewritten** — `Always` or
  `OnFailure`

The second is exactly the enum carried by `kuik_rewrites_total{reason}`, which is what makes the
annotation and the counter agree by construction rather than by convention — and what lets
[`Always` emit no event at all](#noise-is-not-configurable-it-follows-rewritepolicy) without losing
traceability.

### They die with the pod

Annotations are the shortest-lived channel of the four: no pod, no record. Like events they are
therefore **not an audit log** — a rollout replaces every pod and takes its decisions with it. What
survives a pod is the counter, which is why `kuik_rewrites_total` and `kuik_no_alternatives_total`
exist rather than a persisted history of rewrites.

## Events

> [!IMPORTANT]
> Events are **not an audit log**: the default retention set by the apiserver is an hour. Anything
> that has to be reconstructible later — every deletion in particular — must also be logged by the
> controller and counted as a metric. Exporting events to a log backend is the supported way to
> keep history.

### Three rules

**Emit on the object the reader will inspect.** What concerns a pod goes on the Pod, where
`kubectl describe pod` will surface it next to the `ImagePullBackOff` it explains. What concerns the
lifecycle of a resource goes on that resource.

It also decides where grouping is legitimate. A registry has no object in the cluster; the only thing
that can carry a registry-side failure is the pod using the image. So **pod events are never grouped
across pods**, however many are affected at once — fifty pods failing to pull produce fifty events,
one on each object whose owner will come looking, which is coverage rather than noise. Grouping
applies only to events on a resource, where many entries for a single cause pile up on one object and
bury everything else.

**Emit transitions, and emit the inverse when it matters.** An event fires when something becomes
true, not while it stays true. The inverse fires when the condition could have persisted and blocked
something — an image that stops answering gets its recovery, a resource that stops working gets its
return — so that following the stream tells you an incident ended without re-reading a status. It
does not fire when the condition resolves through ordinary operation: a drift that ends because the
cluster caught up, or a copy failure that ends because the next attempt worked, are not news, and the
event that succeeds already says so.

**Keep `Reason` stable and enumerable**, with the variable part in the message. A reason is what
alerting rules and event exporters match on, so it is API surface; and it should map to an action, so
that two conditions needing different remediation never share one. `Warning` means someone should
look, `Normal` means traceability, with nothing in between.

Two consequences of that. **Events are named after the consequence, reasons after the cause**, drawn
from the [single vocabulary above](#reasons) — a 401 is `Unauthorized` wherever it surfaces,
whether it made a resource unusable, a copy fail, or an alternative unofferable. Those three stay
distinct events because their consequences differ, and so do their remedies; collapsing them would
force a reader to decode a reason just to learn whether their resource is dead or merely diminished.

A corollary that decides more cases than it looks: **something that fires routinely in a valid
configuration is not an event.** If a supported way of running kuik makes a condition the steady
state, emitting on it teaches operators to ignore the stream; the condition belongs in a status and a
metric instead, where it stays visible and alertable without shouting.

### Catalogue

| Reason | Object | Type | Emitted when |
| ------ | ------ | ---- | ------------ |
| `ImageFallback` | Pod | Normal | The original was unavailable and an alternative candidate answered. The message carries the original reference, the retained one, and the resource that supplied it — which is what makes an inter-resource ordering debuggable |
| `NoAlternativeAvailable` | Pod | Warning | The original was unavailable and no alternative candidate answered. The pod is left untouched and may still start from the node's cache |
| `PullSecretInjectionFailed` | Pod | Warning | The syncer could not materialise the secret the webhook referenced |
| `RewriteConceded` | Pod | Warning | Another mutating webhook replaced the reference kuik had placed, and kuik stood down rather than write over it. The message carries the container, the origin, the reference kuik had placed, the resource it came from, and the image that won. It emits because it has a remedy: two components are disputing one field, and one of the two scopes has to move |
| `AmbiguousRewrite` | the resources involved | Warning | Two `rewritePolicy: Always` resources place a *different* candidate ahead of the original for the same image. Emitted once on the resources, not per pod |
| `ImageCopied` | `ImageMirror` | Normal | First copy of an image to the destination |
| `ImageRecopied` | `ImageMirror` | **Warning** | A manifest that had been copied was found missing and copied again. This is the most valuable event of the set: it means something outside kuik deleted from the destination while pods may be routed to it |
| `ImageResynced` | `ImageMirror` | Normal | `driftPolicy: Sync` moved a destination tag onto the upstream's new digest |
| `CopyOutOfDate` | `ImageMirror` | Warning | `driftPolicy: Warn` detected the same drift and left the copy as it is. The mirror now serves an older digest than the source, on purpose |
| `ImageCopyFailed` | `ImageMirror` | Warning | A copy failed, with the reason of `status.failedImagesCopy`. How it is emitted follows the **scope of that reason** — see below |
| `ImageDeleted` | `ImageMirror` | Normal | A tag was deleted after its retention elapsed |
| `OrphanTagFound` | `ImageMirror` | Warning | The sweep found a tag belonging to this cluster that no origin accounts for. A tag kuik retires normally carries the origin recorded when its last pod disappeared; one that carries none never went through that path. It is held for its retention and then deleted — the only thing kuik removes without knowing where it came from |
| `ImageDeletionFailed` | `ImageMirror` | Warning | The destination refused a tag deletion |
| `ImageUnrecoverable` | `ImageMirror` | **Warning** | An image is wanted, absent from the destination, and no source can supply it any more — it enters `status.images.missingSource`. Qualitatively different from a copy that failed: nothing will fix this one, and any pod still running it does so from a node cache that will not survive a reschedule |
| `AlternativeUnusable` | `ImageMonitor` | Warning | A monitored alternative cannot be offered as a candidate for a reason that has a remedy — today `Unauthorized`, a credential missing from `fallbackAuth` or rejected. Only remediable causes emit: an alternative that is merely gone stays in the status, per the section above |
| `ImageTagDrifted` | `ImageMonitor` | Warning | The **upstream** digest moved under a tag the cluster is running. Remediation is to follow it or to pin |
| `ImageClusterSkew` | `ImageMonitor` | Warning | Pods run the **same tag** with different `imageID`s — part of the cluster has not caught up with a tag that moved. Remediation is a rollout restart or a forced re-pull, where drift alone would call for deciding whether to follow the upstream |
| `ResourceNotReady` / `ResourceReady` | the resource concerned | Warning / Normal | `Ready` flipped. The message carries the condition's reason — `Unauthorized` for a credential that is missing, malformed or rejected, and whatever else makes a resource unusable. This is the transition worth watching above all others: a resource that is not ready does **nothing at all**, and does it silently |
| `TokenRefreshFailed` | the resource concerned | Warning | A provider token could not be renewed **while the previous one is still valid** — the window in which an operator can still act |

A mirror's first synchronisation emits a burst of `ImageCopied`. That is accepted: they are `Normal`,
they document the ramp-up, and suppressing them would mean the one moment with the most to say is the
quietest.

> [!NOTE]
> Skew is a strict sub-case of drift, not a parallel condition: two distinct digests cannot both equal
> one `upstreamDigest`, so a skewed reference is always a drifted one. In
> [status v3](./status.md#imagemonitor) it is the `driftedImages` entry whose `runningDigests` holds
> more than one element. The two events therefore layer two readings on the same entry —
> `ImageTagDrifted` says the upstream moved, `ImageClusterSkew` says the cluster has not caught up
> uniformly — and they call for different remediation, which is why they stay separate.

### Noise is not configurable, it follows `rewritePolicy`

There is no verbosity knob. Under `OnFailure`, a rewrite means the original failed, which is an
incident worth one event per pod. Under `Always`, a rewrite is the steady state — every pod of every
rollout, forever — so it emits **nothing**. Its traceability is the pod's own
[annotations](#annotations), which say what was rewritten and why, and the
`kuik_rewrites_total` counter, which says how often.

The distinction is already carried by `kuik.enix.io/reason`, so the two channels agree by
construction rather than by convention.

### Why availability is not evented

An image the cluster runs going unavailable upstream is a **state**, not news. It gets a
`status.unavailableImages` entry and a `kuik_image_unavailable` series, which is where an alert should
read it from — but it gets no event, deliberately.

Three reasons, the last of which settles it. It asks nothing of an operator: if the source is gone, it
is gone, and nothing in the cluster changes that. The moment it actually bites is admission, when a
pod can be placed on no candidate — and that already emits, on the Pod, where whoever is debugging
will look. And a **supported way of running kuik makes it the steady state**: mirroring precisely so
that a registry with aggressive garbage collection can drop what the cluster still uses. On a staging
cluster where images rotate quickly, upstream deletions are the normal course of events and the mirror
is doing exactly its job — an event per disappearance would fire constantly for something entirely
intended.

What stays evented is the actionable subset. `AlternativeUnusable` has a fix. `ImageTagDrifted`
and `ImageClusterSkew` say the running content is not what whoever wrote the manifest assumes, which
is a fact about the cluster rather than a health check. Everything else about availability is read
from the status or alerted from the metrics.

### A copy failure is coalesced, never generalised

`ImageCopyFailed` carries a reason from the [shared vocabulary](#reasons), which states what was
observed about the images kuik actually tried and nothing beyond them. An event stream is where that
restraint is hardest to keep: one cause commonly hits many images in a single pass, and the
temptation is to summarise it as a verdict on the registry.

**So failures are coalesced, but never generalised.** When the same reason hits several images in one
pass, one event is emitted for the **narrowest common prefix of the images actually affected**, and its
message says what was seen rather than what it might mean: *"12 images under `quay.io/acme/` failed
with Unauthorized"*, not *"quay.io is unauthorized"*. If the affected images share nothing beyond the
host, the prefix is the host — which is then a statement about those images, still not about the
registry. And a single failing image is simply its own event.

The prefix **names the set that failed; it is not a scope.** It is computed from the images observed to
fail, never from what that path is supposed to hold, so *"12 images under `quay.io/acme/`"* is equally
true whether 12 or 500 images live under it — and it says nothing whatsoever about the ones kuik did not
try. The **count** is the only measure of how far the failure reaches, which makes it the load-bearing
part of the message rather than a decoration on the prefix.

This keeps one cause from producing one event per image at the moment the stream matters most,
without turning an observation into a diagnosis.

## Metrics

### Per-image series are off by default

Exposing one series per tracked image (`kuik_image_available{image=…}`) reproduces in Prometheus the
problem the status was designed to avoid. Cardinality is images × states, but the real cost is
**churn**: every label value ever seen creates a series that stays in head memory and in the blocks
for the whole retention, long after the image is gone. A cluster whose CI pushes a unique tag per
build produces tens of thousands of dead series a month that way, and image references are long
label values, which inflates the index further.

So the same rule applies as to the status: **aggregates and anomalies, not an inventory.**

Each metric below is listed with the `HELP` text it should carry. Labels are shown inline; every
label value is drawn either from configuration or from an enumerated set, except on the anomaly
series at the end.

#### Aggregates — one series per resource and state, mirroring status v3 field for field

| Metric (gauge) | HELP |
| -------------- | ---- |
| `kuik_monitor_images{kind, name, source, state}` | Images an ImageMonitor is tracking, by where the reference came from (`InUse` for what pods carry, `Alternative` for what a routing resource would offer instead) and by state. States are not mutually exclusive and must not be summed |
| `kuik_mirror_images{kind, name, state}` | Images an ImageMirror accounts for, by state |
| `kuik_rewrite_pods{kind, name, state}` | Live pods a routing resource applies to, by what the resource did for them |

The `state` label repeats the field names of the corresponding status, so a dashboard and a
`kubectl get -o yaml` never disagree. It mixes two dimensions on purpose, exactly as the status does:
on `kuik_monitor_images`, `inUse` and `retained` partition why an image is tracked, while
`available`, `unavailable` and `drifted` report the outcome of its last check. Hence the warning not
to sum — `tracked` is the total, the others overlap it.

`source` is the third dimension of that metric, and it exists because the two populations are the
same measurement: an `ImageMonitor` tracks and checks references, and
[`monitorAlternatives`](./spec.md#imagemonitor) merely widens which ones. One series therefore covers
both — `kuik_monitor_images{state="unavailable"}` answers "what is failing" whatever its provenance,
and `sum by (source)` splits it — where two metrics forced every such query to be written twice. The
two mirror the two status blocks: `source="InUse"` carries the states of `status.images`,
`source="Alternative"` those of `status.alternatives`, which are fewer. So the label combinations are
sparse by construction — `{source="Alternative", state="drifted"}` never exists, and
`source="Alternative"` is absent entirely unless `monitorAlternatives` is on. That is a property of
the underlying status, not an artefact of merging.

`kuik_rewrite_pods` mixes them the same way, with one extra caveat: `tracked` names what a resource
*selects* where `rewritten` and `conceded` name what it *did*. So it overlaps the others within a
resource, and unlike them it also overlaps **across** resources — several CRs legitimately select the
same pod ([Attribution](./spec.md#attribution)). Aggregate `rewritten` and `conceded` across
resources freely; never aggregate `tracked`.

#### Scheduling health — is the configured pace keeping up

| Metric (gauge) | HELP |
| -------------- | ---- |
| `kuik_check_cycle_duration_seconds{kind, name, registry}` | Wall-clock seconds taken by the last completed check lap over a registry's images. Produced by an ImageMonitor for the images it tracks, and by an ImageMirror under `driftPolicy: Warn` / `Sync` for the source tags it re-reads. Absent until a first lap completes |
| `kuik_check_images_by_registry{kind, name, registry, state}` | Images a resource checks on a registry, by state: the images an ImageMonitor tracks there, and under `driftPolicy: Warn` / `Sync` the source tags an ImageMirror re-reads there. The size of the ring behind the lap above |
| `kuik_mirror_self_checked_timestamp_seconds{kind, name}` | Unix timestamp at which the last full comparison of the destination finished |
| `kuik_registry_interval_seconds{registry, operation}` | Configured pace at which kuik reads a registry for this operation, as currently loaded: the window between two requests for `Check` and `Copy`, the period between two whole passes for `Scan` (a mirror destination) |

The lap duration and the self-check timestamp are what an operator watches to decide whether the
configured pace still matches the workload: a lap that grows past what the freshness of a verdict is
worth, or a timestamp falling further behind `operation="Scan"` than one pass explains. The ring size
sits between them because it is the denominator that turns the first into something comparable — see
**expected lap length** below.

They are also where the `kind` label earns its keep, since the two ring series are produced by two
kinds each: a ring belongs to a **(resource, host)** pair
([Scheduling](./spec.md#one-budget-per-host-one-ring-per-resource)), and a mirror re-reading an
upstream tag holds one just as a monitor does. The series never covers a mirror's *destination* — that
is what `kuik_mirror_self_checked_timestamp_seconds` is for, and the reason the two are shaped
differently: a lap only means something where windows meter the work image by image, and nothing
rations a registry kuik owns. Its self-check has a **period** instead
([`mirror.destinationScan.interval`](./spec.md#mirror-pacing-the-destination-kuik-owns)), so what it
reports is when a whole pass last finished, not how long a lap took.

The last exposes **configuration**, and it is there so that PromQL can compute the values that
otherwise have to be hard-coded into alerting rules and then kept in sync with the YAML by hand:

- **budget saturation.** One copy is issued per window, so the ceiling is `1 / interval` for
  `operation="Copy"`, and `rate(kuik_mirror_copies_total[1h])` divided by it gives how much of the
  budget is actually being used. On its own that ratio means nothing — an idle mirror sits near zero
  and is perfectly healthy — so it is read *after* establishing there is a backlog, with
  `kuik_mirror_images{state="desired"} - kuik_mirror_images{state="copied"}`. With a backlog present:
  a ratio near 1 means the budget is the binding constraint and only a shorter `interval` will drain
  it faster; a ratio well below 1 means windows are going unused despite work pending, which is
  either copies failing (`kuik_registry_requests_total{operation="Copy"}` with a `result` other than
  `Ok`) or single copies outlasting their own window — which `kuik_mirror_copy_duration_seconds`
  settles, when enabled.
- **expected lap length.** One image is checked per window, so a full lap ought to take
  `ring size × interval`. Comparing the measured `cycle_duration` to that product surfaces lost
  windows — failures, restarts, contention — as a ratio above 1 rather than as a number nobody can
  interpret. Ring size is `kuik_check_images_by_registry`, which both kinds produce: `state="tracked"`
  for an `ImageMonitor`, `state="copied"` for an `ImageMirror`, whose ring holds the tags it has
  copied from that host. On a shared host the ratio also rises simply because the rings share the
  budget, which is the same signal read one level up.
- **thresholds that follow the config.** "Alert if a lap takes twice what was asked for" becomes
  expressible, instead of a literal that silently drifts the day someone edits `interval`.

> [!IMPORTANT]
> Two things this gauge does *not* say. It must reflect the **currently loaded** configuration, not a
> snapshot taken at start-up, or a hot reload leaves it lying. And it reports what this process was
> configured with, not what the registry actually permits: [quotas count per
> identity](./spec.md#quotas-count-per-identity-clusters-pace-independently), so clusters sharing
> quota identity face a lower effective ceiling than the sum of their intervals suggests.

#### Per registry — the granularity an incident calls for

| Metric | Type | HELP |
| ------ | ---- | ---- |
| `kuik_registry_requests_total{registry, operation, result}` | counter | Requests kuik sent to a registry, by operation (`Check`, `Copy`) and outcome (`Ok`, or the reason that request produced: `ManifestNotFound`, `Unauthorized`, `QuotaExceeded`, `Unreachable`, `PushRejected`) |

`kuik_registry_requests_total` is what answers "is docker.io rate-limiting us" without looking at a
single image: a rising `QuotaExceeded` result on one registry is the signal, and the `operation` label
says whether it is the cheap checks or the expensive copies that are being refused. Its `result` uses
the side-agnostic spellings of the [vocabulary](#reasons) — `ManifestNotFound` and `Unreachable`
rather than `SourceNotFound` / `SourceUnreachable` — because the `registry` label already names which
endpoint answered, so the reason has no side left to disambiguate.

#### Counters — cumulative, safe to sum across replicas

| Metric (counter) | HELP |
| ---------------- | ---- |
| `kuik_rewrites_total{kind, name, reason}` | Container images rewritten at admission, by the routing resource that supplied the reference and the policy that placed it (`Always`, `OnFailure`) |
| `kuik_no_alternatives_total{kind, name}` | Containers left untouched at admission because no candidate answered, counted once per routing resource that offered one. Several resources count the same container, so these series must not be summed |
| `kuik_mirror_copies_total{kind, name, reason}` | Images pushed to a destination, by why (`Initial`, `Recopy` after a manifest went missing, `Resync` after an upstream digest moved) |
| `kuik_mirror_tags_deleted_total{kind, name, reason}` | Destination tags deleted by an ImageMirror, by why they were removed (`Unused` once their retention elapsed, `Orphan` when the sweep found a tag no origin accounts for) |
| `kuik_secret_applies_total{result}` | Pull-secret applies performed by the syncer, by outcome (`Applied`, `Noop`, `Failed`) |

The first two counters (`kuik_rewrites_total` and `kuik_no_alternatives_total`) are the only ones
exported by the **webhook**: they count the rewrites it made, and the containers it could not rewrite
for want of an available candidate.

And `kuik_mirror_copies_total` separates `Recopy` from `Initial` for the same reason `ImageRecopied` is
a `Warning` and `ImageCopied` is not: they cost the same bytes and mean entirely different things.

`kuik_secret_applies_total{result="Noop"}` is worth exposing rather than skipping: the syncer applies
blind, so a healthy steady state is almost entirely no-ops, and a rate of `Applied` that stays high
means something is flapping.

#### Optional — copy duration

| Metric (histogram) | HELP |
| ------------------ | ---- |
| `kuik_mirror_copy_duration_seconds{kind, name}` | Seconds spent transferring one image to the destination, from the first blob request to the manifest being tagged |

Off by default, enabled with `metrics.copyDuration: true` in the [global
config](./spec.md#global-config). It answers two questions nothing else can:

- **which of the two ways a mirror falls behind is happening.** Windows going unused with a backlog
  pending is either failures — visible in `kuik_registry_requests_total` — or single copies
  outlasting their own window, which only a duration can show
- **whether `copy.interval` is set to something coherent** with the images actually being mirrored,
  rather than picked by guesswork. A p95 above the interval means the ceiling is fictional

It is opt-in because a histogram is the only thing in this document that multiplies series: one per
bucket per mirror, where every other metric here is a single gauge or counter. On a handful of
mirrors that is nothing; the default stays `false` so that the cost is a decision rather than a
surprise.

### Anomalies signal by presence

| Metric (gauge) | HELP |
| -------------- | ---- |
| `kuik_image_unavailable{kind, name, source, image, registry, reason}` | 1 while a tracked reference is failing its availability check. `source="InUse"` for an image the cluster runs, `source="Alternative"` for one a routing resource would have offered instead. Status side: `unavailableImages` and `unavailableAlternatives` respectively |
| `kuik_image_drifted{kind, name, image}` | 1 while the digest a resource accounts for differs from the upstream digest of that tag — the digest running in the cluster for an `ImageMonitor`, the digest held at the destination for an `ImageMirror`. `image` is the origin reference in both cases. Status side: `driftedImages` on either kind |
| `kuik_mirror_image_failed{kind, name, image, reason}` | 1 while an image cannot be copied to the destination |
| `kuik_image_cluster_skew{kind, name, image}` | Number of distinct digests running for one image reference, mirroring the length of its `runningDigests` in the status. Present only while pods disagree, so its value is always 2 or more |
| `kuik_rewrite_conceded{kind, name, image}` | Live pods carrying a container this resource had rewritten and another mutating webhook replaced. `image` is the origin reference. Status side: `concededRewrites` on the routing resource |

The `reason` label on the first and third is the [shared vocabulary](#reasons) — enumerated, hence
safe on a series, and identical to the one the corresponding status entry carries.

What unites these five is not their value but the fact that **the series exists only while the
anomaly does**: an alert fires on presence and resolves when the series goes away, without ever
comparing a number to a threshold.

Their values differ accordingly. Three carry the constant `1`, because presence is the whole message.
`kuik_image_cluster_skew` carries a count instead: an image running two digests and one running six
are the same condition but not the same urgency, and once the series exists there is no reason to
spend its value on a constant. Alerting is written the same way for all four. The division with the
status is the usual one — the metric says how many and since when, the status says which digests and
how many pods are on each. `kuik_rewrite_conceded` is also a count of affected pods instead of a
constant.

`kuik_image_drifted` is the one series both kinds produce, which is why its `image` label is the
**origin** reference on either — the destination reference would say the same thing in a form only one
kind understands, and it is derivable anyway from the mirror's `destination.path`. Keeping one
spelling makes the comparison that matters a plain join: an image drifted on an `ImageMonitor` *and*
on an `ImageMirror` means the cluster is behind and its fallback is too, where the monitor alone means
only the cluster is. An `ImageMirror` produces the series under `driftPolicy: Warn`, where drift is
detected and deliberately left, and briefly under `Sync` — a series that persists there means a resync
is failing rather than that a tag moved.

Either way, this requires the exporter to **delete the series from its collector** at the transition,
rather than merely stopping to update it. Removing it marks the series stale immediately; leaving it
in place makes the last value linger for the staleness window and alerts resolve late.

Each of the five has its bounded list in a status, per the rule at the top of this document — the
metric says how many and since when, the status says which digests, which pods, what replaced what.
`kuik_image_unavailable` answers to two of them, one per `source` value, which is why that label
belongs on an anomaly series as much as on the aggregate: without it, `unavailableAlternatives` would
be the one anomaly list in this document with no series at all.
What none of them has is a matching **aggregate gauge**, unlike the states in the first table, and
that would be redundant: `count(kuik_image_cluster_skew)` is the total, computed over a series that is
bounded by construction. The aggregates in the status exist because a status cannot run PromQL.

Their cardinality is the number of things currently wrong, which is small by definition and returns
to zero on its own. This is the deliberate exception to the rule above: an image reference in a label
is acceptable precisely because an anomaly list is bounded, where an inventory is not.

### Where a label may come from

The dividing line worth stating once, since every future metric will be an instance of it:

- **labels drawn from configuration** — resource names, registry hosts, enumerated reasons and states
  — are bounded by what an operator declared, and may appear on anything, including counters that
  live forever
- **labels drawn from content** — image references above all — are bounded by nothing, and may appear
  only on anomaly series, which disappear on their own

Four label names are reserved throughout, so that a query written against one metric reads the same
against another: `kind` and `name` always denote the Kubernetes resource a series is about, never a
category of anything else. Anything that classifies *why* something happened is a `reason`, and
anything that classifies *what state* something is in is a `state`. Anything that classifies **which
population of references** a series counts is a `source`, whose two values — `InUse` for what a pod
carries, `Alternative` for what a routing resource would offer instead — are the only ones it ever
takes.

`source` is what lets one metric cover two populations without a second metric name, and it is
reserved for exactly that: it never discriminates producers, which is `kind`'s job, nor outcomes,
which is `state`'s.

**Every enumerated value is PascalCase**, on a metric label exactly as on a condition reason or a
status entry — one spelling per cause, everywhere. A value that names a cause is taken from the
[single vocabulary](#reasons) rather than re-coined: `result="QuotaExceeded"` on
`kuik_registry_requests_total` is the same `QuotaExceeded` a check reports and an event carries, so a
`429` is greppable across the three channels without a translation table. Values that name something
other than a cause — `Initial` / `Recopy` / `Resync`, `Unused` / `Orphan`, `Applied` / `Noop` /
`Failed` — follow the same casing without joining that vocabulary. The one exception is `state`,
whose values are **field names of the corresponding status** and therefore keep the lowerCamelCase of
the YAML they mirror (`inUse`, `noAlternatives`); that is the whole point of the `state` label, and
the reason it is a distinct label name rather than another `reason`.

**Every metric about a resource carries `(kind, name)`**, including those a single kind can ever
produce. On `kuik_monitor_images` the `kind` label is constant, which costs no cardinality and can be
left out of a query — but it makes the join key identical across the whole surface. That matters as
soon as one resource shows up in two families, which is the normal case for an `ImageMirror` with a
`rewritePolicy` other than `None`: it both routes and copies, so comparing what it routed to what it
copied is a plain join rather than a `label_replace` to reconcile two spellings of the same identity.
It also lines the metrics up with how resources are named everywhere else — `rewritten-by` reads
`ImageAlternative/thanos`, events say `via <kind>/<name>`.

> [!WARNING]
> Because `name` alone is not unique across kinds, aggregate on **`(kind, name)`**, never on `name`.
> `sum by (name)` silently merges an `ImageMonitor` and an `ImageMirror` that happen to share a name.

### What this gives up

Two things, stated so nobody looks for them:

- there is **no per-image availability history**. "This image was unavailable 0.3% of the month" is
  not answerable; "this registry had N failed checks" is. Continuous history exists where cardinality
  is structural, and nowhere else
- anomaly dashboards are **tables, not graphs**. An info-metric that comes and goes plots poorly and
  reads well

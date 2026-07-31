# walkthrough: routing only (example 01)

This walkthrough takes [example 01](../examples/01-routing-only.yaml) and follows it through every
component that touches it: which controller does what, in which order, and which algorithm each
step needs. It is a companion to [spec v3](../spec.md) and [status v3](../status.md), written to
surface the implementation choices and the spec gaps a single concrete case exposes.

## The case

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ImageAlternative
metadata:
  name: docker-library
spec:
  alternatives:
    - imagePrefix: public.ecr.aws/docker/library/
    - imagePrefix: mirror.gcr.io/library/
    - imagePrefix: docker.io/library/
```

Everything about this CR is a default: no `podSelector` and no `namespaceSelector` (it applies to
every pod in the cluster), no `rewritePolicy` (so `OnFailure`), all three entries in **subpath
form**, all three registries public and unauthenticated. Nothing is ever copied to a registry: this
is pure routing, the v3 equivalent of a v2 `ClusterReplicatedImageSet`.

The relevant parts of the global config for this case:

```yaml
webhook:
  availabilityCheck:
    timeout: 2s
    activeCheckCache:
      ttl: 10s
    skipHints:
      enabled: true
      maxAge: 30m
registries:
  default:
    check: {interval: 10m, maxPerInterval: 20}
  public.ecr.aws:
    check: {interval: 30m}
  docker.io:
    perPrefixFallbackAuth:
      - prefix: /
        secretRef:
          name: dockerhub-creds
```

## 1. Validating webhook, on the CR itself

Runs once at `kubectl apply`, before any controller sees the object. Every check here is local to
the CR, so it is CEL rules plus a webhook for what CEL cannot express:

- each `imagePrefix` parses as `registry/path` and carries **no tag and no digest**
  (`docker.io/library/nginx:v1` is rejected: alternatives describe repositories, the tag comes from
  the pod)
- no trailing `:` (`docker.io/library/nginx:` is rejected, the single image form already means "any
  tag of this image")
- **form uniformity**: every entry ends with `/` or none does. All three do here, so the list is a
  valid subpath list. This one is naturally CEL-expressible:
  `self.alternatives.all(a, a.imagePrefix.endsWith('/')) || self.alternatives.all(a, !a.imagePrefix.endsWith('/'))`

What admission cannot check is **cross-CR overlap** (another `ImageAlternative` declaring
`docker.io/library/`, or the more specific `docker.io/library/nginx`). That needs a cluster-wide
view and would make admission outcomes depend on apply order, so per the spec it is resolved at
lookup time by specificity and reported in `status` instead.

## 2. Mutating pod webhook, the hot path

Given a pod created with `image: nginx:1.27`.

### Step A: pod-level gates

Skip mirror pods (static pod representations, kubelet would reject the mutation), skip pods matching
the global `skipLabels` / `skipAnnotations`, skip containers already listed in
`kuik.enix.io/original-images`, skip digest-pinned images (`@sha256:`) and containers with
`imagePullPolicy: Never`. All of this exists in v2 and is unchanged.

### Step B: normalization

`reference.ParseNormalizedNamed("nginx:1.27")` yields `docker.io/library/nginx:1.27`. This matters
a lot in this example: the user wrote `nginx:1.27`, and only after normalization does the reference
match the third entry. Matching, cache keys and status keys all use the normalized form.

### Step C: CR selection

All v3 kinds are cluster-scoped, so this is three cluster-wide informer-backed lists
(`ImageAlternative`, `ImageMirror`, `ImageMonitor`), against v2's four (two cluster-scoped kinds plus
two namespaced ones listed per pod namespace). Candidates are then filtered by `podSelector` (pod
labels, in the admission request) and `namespaceSelector`.

`namespaceSelector` is the notable new cost: it is a label selector on the `Namespace` object, so the
webhook needs a namespace lister, where v2's `spec.filter.namespace` was a regex on the name and
needed no extra lookup. Worth a fast path: a selector that is exactly
`matchLabels: {kubernetes.io/metadata.name: <ns>}` (the form used in
[example 04](../examples/04-x509-cert-exporter-namespaced-resource.yaml) to replace v2 namespaced
resources) can be answered from `pod.Namespace` alone, without touching the lister.

Here both selectors are empty, so the CR always applies.

### Step D: matching

v2 evaluated every CR's regex `imageFilter` against the image, so O(number of CRs x number of
upstreams) regex executions per container. v3's prefix semantics are strictly structural, which
allows a **segment trie** instead:

- build once (and invalidate on `ImageAlternative` watch events) a trie keyed by path segments of
  every `imagePrefix` across all CRs. Since the kinds are cluster-scoped there is a single global
  trie, with no per-namespace variant: `docker.io` -> `library` -> subpath node, owner
  `docker-library`, entry index 2
- lookup splits `docker.io/library/nginx` into `[docker.io, library, nginx]` and walks down,
  remembering the deepest node that matches. A **subpath node** matches only when exactly one
  segment remains below it, an **exact node** only when zero remain. That is what enforces the "one
  level only" rule from the spec (`quay.io/acme/foo/bar/oni` does not match `quay.io/acme/foo/`)
- **longest match wins**, so an exact `docker.io/library/nginx` entry in another CR beats this CR's
  `docker.io/library/`. Equal specificity in two CRs is broken by lexicographic CR name (a single
  flat name space now that there are no namespaced kinds), and both CRs report the conflict in their
  status
- lookup is O(number of segments), independent of the number of CRs. That matters precisely because
  this CR carries no selector, so every pod in the cluster goes through it

The winner is a **single CR** (lists are never concatenated), so the candidate set is exactly that
CR's three entries with the matched remainder (`nginx`) and the tag (`1.27`) reapplied:

```text
0. public.ecr.aws/docker/library/nginx:1.27
1. mirror.gcr.io/library/nginx:1.27
2. docker.io/library/nginx:1.27           <- the original
```

### Step E: policy applied to the ordering

`rewritePolicy` only decides **where the original sits in the probe order**. It never removes it
from the candidate list: for `ImageAlternative` the original is a member of `alternatives` by
construction (matching happens against those very entries), so it always remains a candidate.

| Policy | Probe order | Effect |
| ------ | ----------- | ------ |
| `OnFailure` (default) | docker.io, then public.ecr.aws, then mirror.gcr.io | the original is hoisted to the head. Docker Hub first, mirrors are pure fallback. Same behaviour as the v2 CR at priority 0 with upstream priorities 10/20/30 |
| `Always` | public.ecr.aws, then mirror.gcr.io, then docker.io | the list is used exactly as written. Prefer the ECR pull-through unconditionally, the "never hit Docker Hub rate limits" posture |

> [!IMPORTANT]
> Under `Always`, the **declared position of the original** in `alternatives` is significant. In
> this example the original comes last, so `Always` prefers the mirrors, which is the intent. Write
> the same CR with `docker.io/library/` **first** and `Always` becomes a no-op: the original is
> probed first, it answers, and nothing is ever rewritten. Put the registry you want preferred at
> the top of the list.

### Step F: availability probing

- **skip hints**: before probing, drop or downgrade candidates that `ImageMonitor` or `ImageMirror`
  recorded unavailable less than `maxAge` (30m) ago.
- **per-original cache**, keyed by `docker.io/library/nginx:1.27`, short-circuits the whole
  resolution. v2 hardcodes a 1s TTL, v3 exposes it as `activeCheckCache.ttl: 10s`, so a 50 replica
  rollout costs one resolution instead of 50
- **singleflight** dedupe on both the resolution and each individual availability check, so
  concurrent admissions for the same image collapse into a single registry call.
- `parallel.FirstSuccessful` probes all remaining candidates **concurrently** but returns the first
  success **in list order**, so a fast mirror never beats a healthy higher-priority entry. Worst
  case latency is one `timeout` (2s), not the sum of them.
- each probe is a manifest `HEAD` through go-containerregistry, returning a typed status
  (`Available`, `NotFound`, `Unreachable`, `InvalidAuth`, `QuotaExceeded`).
- **auth**: nothing in the CR, all three registries are public, so probes are anonymous, except on
  `docker.io` where the global `perPrefixFallbackAuth` supplies `dockerhub-creds`. That entry is not
  cosmetic: anonymous Docker Hub HEADs are the ones that get 429'd, and a 429 on the *check* would
  convince the webhook that the original is down and reroute the whole cluster to ECR.
  `injectPullSecret` is irrelevant here, no secret is ever synced into a user namespace for this
  example.

### Step G: rewrite and annotate

```yaml
kuik.enix.io/original-images: '{"nginx":"docker.io/library/nginx:1.27"}'
kuik.enix.io/rewritten-by:    '{"nginx":"ImageAlternative/docker-library"}'
kuik.enix.io/reason:          '{"nginx":"OnFailure"}'
```

If the original is healthy, the pod is left untouched. If all three candidates are unavailable there
is no mutation either, `reason` is `NoAlternatives`, and the pod still starts when the image happens
to be in the node's cache.

One good property of the "one CR owns the list" rule: a pod that directly references
`public.ecr.aws/docker/library/nginx:1.27` matches entry 0 of the *same* CR and gets the *same*
three candidates. Rerouting is idempotent and cannot chain across CRs into a loop.

## 3. ImageAlternative status controller

Leader-elected and informer-driven. Per [status v3](../status.md) it makes **no registry calls at
all**, and the webhook writes **no status**. The annotations from step G are the entire
communication channel between the two.

Per reconcile (triggered by pod events and CR changes, debounced because pod churn is high):

1. select pods with `podSelector` / `namespaceSelector` (empty here, so all of them)
2. for each container, run the same trie lookup on the value read from `original-images`, not on
   `spec.containers[].image` which may already be rewritten, and count the pod as `tracked` when
   this CR wins the lookup
3. read `rewritten-by` and `reason` to classify: `rewritten` (this CR is named) and
   `noAlternatives` (`reason == NoAlternatives`)
4. group rewritten containers by `(original image, routedTo)` into `activeFallbacks` with a pod
   count, and by original image into `noAlternatives`
5. `since` is **sticky**: carry the previous timestamp forward when the entry already exists, stamp
   `now()` only for new entries. Otherwise the field resets on every controller restart and becomes
   useless for alerting
6. derive `conditions` (`Ready`, `NoActiveFallback`, `NoAlternatives`) from the aggregates and patch
   status only when something actually changed

Steady state for this example: `tracked` is every pod in the cluster running a `docker.io/library/*`
image, `rewritten: 0`, `activeFallbacks: []`, all conditions green. During a Docker Hub incident:

```yaml
status:
  activeFallbacks:
    - image: docker.io/library/nginx:1.27
      routedTo: public.ecr.aws/docker/library/nginx:1.27
      pods: 12
      since: "2026-07-31T09:14:00Z"
  conditions:
    - type: NoActiveFallback
      status: "False"
      message: "1 image routed to fallback (12 pods)"
```

That condition flipping is the alert signal, and it is the main reason this controller exists at all
for a routing-only CR.

> [!NOTE]
> Implementation cost to watch: counting `tracked` naively means a full pod list per reconcile. With
> no selector on a large cluster that is expensive, so it needs an incremental reverse index
> (image -> pod refs) maintained from informer events rather than a recount.

## 4. ImageMirror controller

**Not involved.** No `destination`, so no copy loop, no `cleanup` GC, no `repositories` inventory,
no `selfCheck` ring, no push credentials, no secret injected into user namespaces.

The interesting part is what happens when an `ImageMirror` also matches these pods, which
[example 05](../examples/05-global-mirror-force-rewrite.yaml) does cluster-wide. See
[the open point](#open-point-composing-imagealternative-and-imagemirror) at the end of this page.

## 5. ImageMonitor controller

**Not involved.**

## Summary of the algorithms involved

| Stage | Algorithm | Status |
| ----- | --------- | ------ |
| CR admission | CEL form / tag / uniformity checks | new |
| Pod webhook match | segment trie, longest-prefix-wins, lexicographic tie-break | new, replaces regex filters |
| Candidate build | remainder and tag reapplication, policy-driven original placement | reworked, replaces the two-level priority system |
| Probing | `FirstSuccessful` + singleflight + two TTL caches + skip hints | exists, retuned |
| Status | informer-only aggregation over webhook annotations, sticky `since` | new, v2 wrote status from the webhook |

## Settled: what `rewritePolicy` does to the candidate list

Raising this example surfaced an ambiguity in the wording of `rewritePolicy`, since for
`ImageAlternative` the original image is always one of the `alternatives` entries: "always rewrite
image to first available in `alternatives` list" could be read as excluding the original from the
candidates. It does **not**. The decided semantics are the ones documented in step E:

- the original is never excluded, it stays a candidate under both policies
- `OnFailure` hoists it to the head of the probe order, `Always` leaves the list exactly as written,
  so the original keeps its declared position
- consequently, under `Always`, where the operator declares their own registry in the list is
  meaningful, and declaring it first makes the policy a no-op

`ImageMirror` is not affected by the same ambiguity: its `destination` is a separate field, so the
original is genuinely outside the list there.

> [!NOTE]
> The policy names are being reconsidered (`PreferFirst` / `PreferOriginal` instead of `Always` /
> `OnFailure`), because they describe this ordering behaviour directly: `PreferOriginal` says the
> original is probed first, `PreferFirst` says the first list entry wins, which makes the
> significance of the declared order self-evident. This walkthrough follows the current spec and
> keeps `OnFailure` / `Always` until that change lands.

## Open point: composing `ImageAlternative` and `ImageMirror`

> [!IMPORTANT]
> **How do `ImageAlternative` and `ImageMirror` candidates compose when both match a pod?**
>
> v2 had an explicit answer: signed `spec.priority` plus the default type order
> (Original, CISM, ISM, CRIS, RIS). v3 drops `priority` entirely, and the specificity rule
> (longest prefix, then CR name) is defined only *within* `ImageAlternative`. Combining example 01
> with the cluster-wide mirror of example 05 is therefore undefined: for a
> `docker.io/library/nginx:1.27` pod, is the candidate order the mirror first, the alternatives
> first, or interleaved? The only tool v3 offers today is `excludeImagePrefixes` on the mirror,
> which is a manual carve-out (the operator has to remember to exclude `docker.io/library/` from
> the global mirror) rather than a defined ordering.
>
> [Example 07](../examples/07-alternative-and-mirror-composition.yaml) installs both resources
> side by side: it derives the order v2 produced for that pod, and lists the three questions v3
> has to answer (candidate order, which images the mirror copies, and which CR is credited in the
> annotations and statuses).

A smaller adjacent question, not exercised by this example since it has no auth: now that all kinds
are cluster-scoped, `auth.secretRef` carries no namespace. In v2 a namespaced CR resolved it against
its own namespace. v3 needs to say that it resolves against the operator's namespace (and that
`injectPullSecret` copies from there into the pod namespace).

# kuik v3 — CRD redesign: decisions & open questions

> [!IMPORTANT]
> This document tracks questions and decisions about the spec for version 3 of kuik, which is defined in the [spec](./spec) directory.

---

> [!NOTE]
> Working design doc. Nothing here is final. Legend below marks how settled each item is.
>
> - ✅ **Decided** — agreed in discussion, unlikely to change.
> - 🟡 **Provisional** — leaning this way, but pending team / Solvik validation.
> - ❓ **Open** — still to answer (see the open-questions section).

## Guiding principles

These frame every decision below and should be the tie-breaker when a question is ambiguous.

- **Mission**: maximize the availability of Pod images **strictly within the Kubernetes cluster kuik
  runs on**. kuik is **not** a general-purpose image-movement tool. The docs were updated to state
  this firmly.
- **Routing is the base feature; copying and monitoring exist only to serve routing.** Copying
  improves availability *for routing*; monitoring provides the availability data *routing needs*.
- **Docs-first**: write the CRD docs with concrete examples before writing Go.
- **Three technical pillars** (historically planned as a 2.5 code split): routing, copying,
  monitoring. v3 realizes the split — but see D1: monitoring collapses into routing's status rather
  than becoming its own kind.

## The model

🟡 After the discussion, v3 lands on **two CRDs** (down from v2's five), arranged as a
desired-state / observed-state / reconciler triad:

| Piece | Role |
| --- | --- |
| **Router `spec`** | *Desired topology* — the ordered set of equivalent alternative locations for a set of images, with filters and priority. |
| **Router `status`** | *Observed reality* — which images have actually been seen, their availability per alternative, and freshness (last-checked time). **This status is the monitor.** |
| **Copier** | *Reconciler* — for an image the Router wants available at an active target but `status` says is missing, copy it there from an available alternative. Driven by Router status ⇒ inherently usage-driven. |

Dependency direction: **the Copier references/selects the Router; the Router knows nothing about
Copiers.** Delete every Copier and routing still works (with fewer available alternatives).

Typical flow:

1. Pod is admitted; webhook matches it against Routers.
2. Webhook builds the ordered `alternatives` list and picks the first *available* one (reading
   cached availability; recording observed images + availability into Router status off the hot
   path).
3. If the preferred (active) alternative is unavailable, the Copier — watching Router status — copies
   that image there from an available alternative. Next admission gets the fast path.
4. The Pod's original image is always the ultimate fallback; a not-yet-ready copy never blocks a Pod.

## Decisions

### D1 — CRD set & responsibilities 🟡

- **Two CRDs**: a routing CRD (routing + monitoring-via-status) and a copying CRD (Copier).
- **No separate Monitor CRD.** Availability monitoring is a property of the routing CRD's status
  (see D7). Rationale: routing already needs availability data on the hot path; the Copier needs the
  same data to know what to copy; so the data has exactly one natural home.
- Replaces v2's five kinds (CISM / ISM / CRIS / RIS / CISA).

### D2 — Routing: ordered list, absolute order ✅

- `spec.alternatives` is an **ordered list**. Implemented as a plain atomic CRD array — order is
  preserved by default; we must **not** mark it `x-kubernetes-list-type: map`/`set` (those apply
  merge/reordering semantics).
- **List order is absolute.** The Pod's requested image gets **no** special priority. The list is
  platform policy; the webhook tries alternatives top-to-bottom and uses the first available one.
  - Motivating use case: *"prefer the local harbor over docker.io even when the Pod pins docker.io"*
    (rate-limit avoidance). Only absolute order can express this.
  - "Maximize availability" = *find an available copy*, not *find the most-available copy*; order
    encodes the platform's preference **among** available copies.
- This **removes the signed negative/zero/positive per-CR priority-vs-original mechanism** from v2 —
  position in the list now carries that meaning.

### D3 — Priority (cross-CR only) 🟡

- **Drop intra-CR / per-entry priority entirely.** List order replaces it (D2).
- **Keep a single per-CR priority** for the case where two Routers match the same image and we need
  to say which one wins.
- **Cross-CR interleaving at equal priority is not a supported use case.** You cannot express
  "A's entry 1 > B's entry 1 > A's entry 2". If you need interleaving, the entries belong in one
  Router. Different intents ⇒ different priorities.
- When two equal-priority Routers match the same image, the router whose **matching prefix is more
  specific wins** — the **longest matching `alternatives` prefix by character length**, the same
  longest-prefix rule the Copier uses for overlapping destination prefixes (Q4). This is the common,
  *intended* overlap: a broad router (e.g. `docker.io/`) and a narrower one (`docker.io/library/`)
  both match, and the narrower one is meant to win — no priority juggling required. It is resolution
  by construction, not a warning-worthy conflict.
  - Rationale: precision is a natural, user-visible signal of intent ("I wrote a more specific rule
    *because* I want it to take precedence here"), and reusing the Copier's rule keeps one overlap
    model across both kinds.
  - **Only when the matching prefix is *identical*** (same priority *and* same matching prefix — e.g.
    two routers that both carry `docker.io/library/`) is the overlap a genuine **conflict**. Then fall
    back to a **deterministic tie-break** (proposed: `(kind, name)` lexical order) plus a
    `ConflictingRouter` status condition / event. This is the only case that warns; different-specificity
    overlaps are silent because they are resolved by design.

### D4 — Image matching: prefix swap ✅

Superseded the earlier glob idea (regex → glob → **plain prefix**; see Q6). No `*`/`**`.

- **Prefixes replace regex** for image matching (simplest form that still covers the common case).
- Each `alternatives` entry is a **prefix** that is both the matcher and the destination template:
  - The first entry that is a **prefix of the pod's image** matches; the remainder of the reference
    (repo path + tag/digest) is the captured suffix.
  - Routing/copying **swaps** that prefix for another alternative's prefix; the captured suffix is
    reattached unchanged.
  - **Tag / digest is always preserved** — we never remap tags across mirrors.
- Prefix match is on **path-segment boundaries** (so `docker.io/jp` does not match
  `docker.io/jpetazzo/...`), consistent with the Copier prefix rule (Q4).
- The prefix selects candidacy and captures the swap suffix; an **image-dimension filter can further
  refine** which matched images the Router governs (exclusions) — see D9.

Example — Pod requests `docker.io/jpetazzo/foo:v1`:

```yaml
alternatives:
- docker.io/jpetazzo/               # matches, suffix = "foo:v1"
- ghcr.io/jpetazzo/                 # → ghcr.io/jpetazzo/foo:v1
- harbor.enix.io/jpetazzo/mirrors/  # → harbor.enix.io/jpetazzo/mirrors/foo:v1
```

### D5 — Field naming ✅

- `replicas` → **`alternatives`** (term already used in the codebase/docs).

### D6 — Copier: usage-driven, subordinate to Router ✅ (usage-driven) / 🟡 (inversion)

- ✅ The Copier **only copies images actually scheduled on the cluster that match the filters**.
- 🟡 **Inverted relationship** (supersedes the earlier "Router references a Copier" idea): the Router
  declares the desired topology (including locations that don't exist yet); the Copier is a reconciler
  that says *"when an image a Router wants at an active target isn't available, copy it there from an
  available alternative, using these push credentials."*
  - This makes the mission **structural, not documentary**: the Copier is driven by Router status,
    which only ever contains admitted (used) images — you *cannot* copy an unused image. It also fits
    globbing (you can't enumerate a glob's tag space to pre-copy).
  - It generalizes v2's existing stale-mirror cleanup (`NotFound` → clear `mirroredAt` → re-mirror),
    which is already "routing observes unavailability → copying reacts."
  - Answers the "just move gitlab→harbor, consume only harbor" question: **yes** for images the
    cluster actually uses; **no** for bulk pre-migration of unused images (that's the general-mover
    use case kuik disavows). Setting up a copy incidentally sets up redundancy — which is fine given
    the mission.

### D7 — Monitoring folded into Router status 🟡

- Availability lives in the **Router status** (observed images + per-alternative availability +
  freshness), not a separate CRD.
- **Freshness knob**: skip the active check when an image was monitored within a configurable window
  — the honest, observable version of today's in-memory 1s TTL cache. Default **conservative**
  (seconds), because stale availability routes confidently to a mirror that just died.
- **Status writes stay off the admission hot path**: the webhook reads a cache; a reconciler owns the
  status flush.
- **Bound status growth** with an `UnusedImageExpiry`-style GC (v2 CISA's one real addition over RIS
  survives the merge).

### D8 — Scope: cluster-only for v3, namespaced reserved 🟡

- v3.0 ships **cluster-scoped** kinds only (the namespaced use case is weak for copying/monitoring;
  filters already carry a namespace dimension).
- The routing kind is a **privileged, platform-team resource**; document it as effectively
  cluster-admin-over-routing.
- A **namespaced routing kind is reserved for later**, when a real multi-tenant ask lands — it is the
  *sound* way to get per-namespace RBAC (see open question Q6).

### D9 — Image-dimension filter: post-prefix refinement 🟡

The docs-first migration exercise ([migration examples](./spec/migration-examples.md))
surfaced a real gap: with prefixes as the *only* image-scoping mechanism, there was no way to say
"mirror everything **except** cloudnative-pg" or "monitor everything **except** the mirror namespace"
— exclusions that v2 expressed with `imageFilter`. So:

- **The Router *does* use `spec.filter`'s image dimension** (reverses the earlier "image dimension is
  not used for routers"). It is **not a new field** — the unified `Filter` already carries an image
  dimension (`Filter.BuildImageFilter()`); v3 simply stops disabling it for routers.
- **It applies *after* prefix match, as a refinement, not the primary matcher.** A prefix still does
  the two structural jobs — decide candidacy and capture the swap suffix. Once a prefix matches, the
  **original image reference** is tested against the filter's image `include`/`exclude` (regex,
  default-allow when `include` is empty). Both must pass for the router to apply; otherwise the image
  falls through to the next router / keeps its original image.
- **Clean division of labour:** the **prefix is topology** ("where the same image lives" + the
  destination template); the **image filter is scope** ("which images this policy governs"). They are
  different axes, so having both is not redundant — it is the reason a single global-mirror Router can
  now carve out exceptions without a shadow Router.
- **Per-Router, not per-alternative.** This handles *whole-Router* exclusions (drop an image from the
  policy entirely). It deliberately does **not** resurrect v2's *per-upstream* `imageFilter`: excluding
  an image from *one* alternative but not others is unnecessary under first-available routing — a
  missing image at one alternative simply isn't available and routing skips it (the same reasoning
  that retired `inactive`, Q2, and made the bitnami per-upstream excludes vanish in the migration
  exercise). One image-scope axis per Router; intra-Router selectivity stays the availability check's
  job.
- Resolves migration-exercise gap **G3**; the global mirror (case 5) and monitor-all (case 6) now
  translate almost 1:1 from their v2 `imageFilter.exclude`.

## Open questions

### Q1 — CRD naming ❓

- Routing kind: `ImageRouter` (mechanism, punchy) vs an intent name like `ImageAvailabilitySet` /
  `ImageAvailabilityGroup` / `ImageHighAvailability` (truer now that it owns routing + monitoring +
  drives copying). Copier kind: `ImageCopier` / `ImageCopy`.
- `Cluster` prefix or bare? Convention: cluster-scoped kinds with a (future) namespaced sibling take
  the `Cluster` prefix (`ClusterRole`/`Role`); inherently cluster-only kinds don't (`Node`,
  `StorageClass`). Since a namespaced router is plausible (D8), leaning toward keeping the prefix to
  reserve the bare name — but mixing prefixed/bare within one API group reads oddly.
- **This is the one item to put to the whole team rather than decide unilaterally.**

Decision: `ClusterImageRouter` (It names what the user configures and observes, not an emergent property. Status data is not a reason to rename the kind after its primary action. The primary action is routing.)

### Q2 — `inactive` / source-only semantics & field name ❓

- The `inactive` flag (issue #547) currently means "matched, but excluded from routing alternatives."
  The inverted model needs it to **also** mean "still eligible as a copy **source**" — otherwise a
  "harbor active, gitlab inactive" topology gives the Copier nowhere to copy *from*.
- Pick a name that conveys "not a routing target, but retained for matching and as a copy source"
  (`sourceOnly`? `routeTarget: false`? something clearer than `inactive`).

Decision: since the first item in the list is always taken in priority, the inactive flag isn't useful. If a target should not be used for routing, the user should put it at the end so working alternatives will be considered first. If none are available, configuring the last one as "inactive" would not change anything (beside an additional http call, which is fine).

### Q3 — Cold-start / pinned-empty-target ❓

- Confirmed rule: the Pod's original image is always the ultimate fallback; a not-ready copy never
  blocks a Pod.
- Unresolved edge: a Pod that pins the *empty active target directly* (e.g. `harbor/...` when harbor
  is empty and the only other location is source-only) — that pull fails. Do we allow
  fallback-to-source-even-if-source-only as a last resort, or document it as unsupported?

Decision: based on the decision taken on Q2, this question is now irrelevant.

### Q4 — Copier ↔ Router binding & credentials ❓

- Does a Copier reference **one** Router or **select many**? (A selector lets one "populate harbor
  from wherever" Copier serve several Routers; a 1:1 ref is simpler for RBAC.)
- Where do credentials live — push creds for the destination, pull creds for the sources?

Decision: a Copier binds by **destination prefix** (e.g. `harbor.enix.io/jpetazzo/mirrors/`), **not**
by referencing a Router. It owns that push namespace: it watches every Router status and picks up copy
jobs whose desired-but-unavailable destination falls under its prefix, copying from any
currently-available alternative for that image. Rationale: the destination prefix is the real unit
credentials attach to, so binding there is natural; Copiers become reusable across any number of
Routers with no N:M refs to manage and no dangling-ref problem; Router and Copier meet implicitly at
the destination — same "meet at the data, not the identity" principle as the inversion.

Credentials:

- **Pull (from source): the originating Router's creds** for that source alternative — the Router
  already holds them for availability checks, so no duplication.
- **Push (to destination): the Copier's own creds, falling back to the originating Router's creds.**
  Caveat: the Router's creds usually carry only *pull* scope (all availability checks need), so the
  fallback will often lack push rights — a clear auth error surfaces when it does; it is not silent.
- The prefix is a **responsibility scope, not a copy trigger** — jobs still originate from Router
  status (usage-driven preserved); a broad prefix (`harbor.enix.io/**`) does *not* make a Copier a
  general mover.

### Q5 — Status hot-path details ❓

- Exact freshness default and units.
- Mechanism for the webhook→status handoff (in-memory cache + periodic reconciler flush?), and how it
  interacts with the existing `checkCache` / `alternativeCache`.
- Concrete GC field (name, default) for unused-image expiry.

### Q6 — Glob grammar details ❓

- Confirm `**` (repo path, multi-segment) vs a single-segment `*` token, and whether the constrained
  form allows `*` at all.
- Does the wildcard ever span the registry host? How is "any registry, this repo" expressed (if at
  all)?
- Case sensitivity; behavior on digest-pinned refs (`@sha256:...`) — carried over from v2's skip?

Decision: don't use `*` nor `**`, just write a prefix

### Q7 — `imagePullPolicy` interaction ❓

- v2 has special handling: `Always` pulls the original first unless
  `routing.honorPrioritiesOnAlwaysImagePullPolicy`; `Never` is skipped unless
  `routing.rewriteOnNeverImagePullPolicy`. Does absolute-order routing (D2) change any of this, and
  do the config toggles carry over as-is?

Root cause (why the special case exists): the routing/availability model assumes **tag → content is
stable across locations**. Mutable tags (`:latest`, re-pushed tags) break that — the alternatives can
hold different bytes, so "use any available" silently serves whatever version a mirror happens to
hold. `imagePullPolicy: Always` is Kubernetes' signal for "this tag may move, always re-pull", and
k8s *defaults* `:latest`/untagged to `Always`, so the policy catches the common mutable case for free.

Decision:

- **Default: do not rewrite `Always` containers with a tag-based ref** — leave the original image so
  the kubelet reaches the upstream source of truth (v2's behaviour, re-expressed for the no-priority
  model: "routing disabled for `Always` by default" rather than "priority not honored"). Serving
  stale bytes is a correctness bug; correctness wins by default.
- **Digest-pinned refs (`@sha256:`) are immutable → route them even under `Always`.** The digest
  guarantees identical content everywhere. (Revisit v2's blanket *skip* of digest refs — a digest is
  the ideal mirror candidate, not something to skip.)
- **Opt-in to route `Always` anyway, at two altitudes:**
  - Global config `routing.rewriteOnAlwaysImagePullPolicy` (rename of
    `honorPrioritiesOnAlwaysImagePullPolicy` — "priorities" no longer exists; parallels
    `rewriteOnNeverImagePullPolicy`).
  - **Per-Router override** — tag mutability is a property of a specific image set, and a Router *is*
    a per-image-set object, so the Router is the natural altitude to declare "my tags are immutable,
    route `Always` for me." Finer and more correct than the global-only flag.
- **`Never`: keep skipped by default** (the node must already hold the image under its original name;
  rewriting to a mirror name it lacks would break it), keep the `rewriteOnNeverImagePullPolicy`
  opt-in.
- **One decision covers both pillars:** because the Copier only acts on images routing wanted at a
  mirror but found unavailable, the `Always`-bypass default *automatically* prevents the Copier from
  freezing a mutable tag into a mirror — no separate rule needed (and there is no clean way to keep a
  mutable tag fresh in a mirror anyway: polling upstream for tag moves defeats rate-limit avoidance).
- **Documented limitation:** `imagePullPolicy` is a proxy, not a guarantee. The gap is a non-`latest`
  tag re-pushed with `IfNotPresent`, which can serve stale content — arguably a misconfiguration.
  State plainly: *kuik assumes tags are immutable; use `Always` or digest pins for mutable content.*

Considered alternative / evolution — **digest-based freshness validation** ❓ (open, likely v3.x):

Instead of proxying mutability through `imagePullPolicy`, verify *content identity*: store each
alternative's digest in status and route to an alternative only when its digest matches the
**authoritative** one. More principled than the policy proxy and lets even mutable tags (`:latest`)
benefit from mirrors *safely* — it can subsume Q7 (route `Always` iff the digest matches, else hit
upstream). Not adopted for v3.0 because of the following, which must be resolved first:

- **Reference must be the *authoritative* alternative, not "the first in the list."** Under D2 the
  first alternative is the most-preferred-*for-availability* location (e.g. the local harbor mirror) —
  precisely the one that can be stale. Freshness needs the upstream where the tag is published, which
  is a *different axis* from the availability ranking. Requires an explicit "source of truth" marker
  (or a rule like "authority = the upstream/last entry").
- **Cost is near-free where already probed, amortized elsewhere.** The availability check is already a
  manifest `HEAD` carrying the digest (`Docker-Content-Digest`), so capturing digests for probed
  alternatives adds no calls. The only extra cost is probing the authoritative source when
  first-available routing stops early — amortized to one check per D7 freshness window.
- **Multi-arch + platform filtering breaks naive index-digest equality.** Faithful copies preserve
  digests, but the Copier's `Mirroring.Platforms` filter produces a smaller, different index → a
  correct mirror looks "stale." Compare the **per-platform manifest digest the node will pull**, not
  the index digest — or accept that platform-filtered mirrors can't use strict equality. Main
  implementation hazard.
- **Freshness optimizer, not an availability mechanism.** It narrows the mutable-tag race to the
  freshness window (doesn't close it), and it can only validate while the authority is reachable —
  when upstream is down (the prime availability scenario) it degrades to "serve the last-known mirror
  copy anyway" (stale > nothing). Frame it accordingly.
- **Reopens Copier scope.** When the authority's digest moves, the mirror becomes "invalid", which
  should trigger the Copier to **re-copy/refresh** it — i.e. the Copier deliberately chasing mutable
  tags, the thing the Q7 default avoids. More availability, more upstream polling, more scope.
- Digest-pinned refs (`@sha256:`) need no check — already immutable (consistent with the decision
  above).

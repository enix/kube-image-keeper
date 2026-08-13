# ImageMirror — reconciliation walkthrough

## Part A — The copy loop

### A.1 Select the pods

The controller watches **all** pods in the cluster (no server-side filtering: the CR's selectors may change at any time, and other CRs need the same data). For a given `ImageMirror`, a pod is in scope when it matches **both** `podSelector` (pod labels) and `namespaceSelector` (namespace labels). Absent or empty selectors match everything, so a CR with no selectors covers the whole cluster.

Only **live** pods count. A pod that is deleted leaves the scope immediately — but the images it referenced do not necessarily leave the desired state (see Cleanup, C.1).

### A.2 Extract the image references

From each in-scope pod, collect the image of every container: `containers`, `initContainers`, and `ephemeralContainers`. Keep the container name alongside each ref — it is the key used by the pod annotations, and it is needed to attribute a rewrite.

Also collect `status.containerStatuses[].imageID` (the digest actually running). It is not needed for copying, but it is the baseline for drift detection and it costs nothing to read here.

### A.3 Resolve the origin reference

The webhook may have rewritten the pod, in which case the image currently in the spec points at a mirror, not at the image the user asked for. One rule resolves this:

> **If the pod carries `kuik.enix.io/original-images` with an entry for this container, that entry is the reference to use. Otherwise, use the ref as observed.**

That's the whole step. Nothing is derived by reverse-engineering a mirror ref: the destination layout is a **one-way function** (long tags get truncated and hashed to fit the 128-character limit, A.6), so reading it backwards is not generally possible — and not needed, since the annotation carries the answer verbatim.

The same image pulled from two different registries by two different pods produces two distinct origin refs, hence two mirrored entries. They are not merged.

### A.4 Apply the exclusions

Two filters:

1. **`excludeImages`** — globs matching what the operator does not want mirrored (huge images, images already close to the nodes). Matched against the normalized reference, as in [the spec](../spec.md#excluding-images-from-a-mirror).
2. **Own destination** — any ref already prefixed by this CR's `destination.path` is **not a copy candidate**. Non-configurable, non-overridable: this is what makes `mirror/mirror/…` structurally impossible, even if an annotation was lost.

A mirror ref appears in a pod because the webhook put it there. A mirror ref written by hand into a manifest is excluded like any other — hence not counted, and the cleanup may eventually remove the tag it relies on.

### A.5 Build the desired state

```
desired = { origin refs of in-scope live pods }
        ∪ { refs inside the retention window that carry an origin }
```

The second term is what keeps a CronJob's image mirrored between two runs (see C.1). A tag the sweep found carries no origin, so it stays out of this set: it is held for its retention, then deleted (C.2). The set is recomputed from informers and persisted state — never accumulated incrementally.

**Everything is copied, for every architecture**: a verbatim copy of the full index and all its children. A verbatim copy **preserves the digest**, which is what keeps the rest simple — pinned refs resolve identically on the mirror, drift comparison is a straight digest equality, and the cross-cluster fast path (A.8) can recognise an already-pushed manifest.

### A.6 Compute the destination reference

For an origin ref, the destination ref is:

```
<destination.path> + <full origin ref, hostname included> + "_" + <clusterID>
```

Example: `quay.io/thanos/thanos:v0.42.2` → `registry.tld/mirror/quay.io/thanos/thanos:v0.42.2_cluster-a`

Three rules:

- **The origin hostname is kept.** `docker.io/acme/app` and `ghcr.io/acme/app` may be unrelated projects; merging them into one destination repository would make their tags fight — silent corruption.
- **Characters illegal in a repository path are normalized** deterministically: a port `:` becomes `_` (`localhost:5000/foo` → `localhost_5000/foo`), short forms are expanded (`nginx` → `docker.io/library/nginx`).
- **The tag carries the cluster identity**, not the path: clusters sharing a registry write into the *same* repository and own only their own tags, which is what [multi-cluster](../spec.md#multi-cluster-shared-destination-one-tag-per-cluster) buys and specifies. The **128-character tag limit** and the deterministic truncation that keeps long CI-style tags within it are specified there as well — and that truncation is exactly why nothing may depend on reading a destination tag backwards.

**Digest-pinned refs usually need no special tag.** Since copies are verbatim, the destination manifest has the *same digest* as the source. The rewritten ref is therefore simply `…/repo:v1.15.1_cluster-a@sha256:594cee…`: the `name:tag@digest` form is valid in the reference grammar and accepted by runtimes, which resolve by digest and treat the tag as informational. The **digest** guarantees exact content; the **tag** keeps the manifest tagged and out of reach of the registry's untagged-manifest GC.

That last part is the invariant to hold onto: **a referenced digest must always carry at least one of our tags** — otherwise the registry's untagged-manifest GC is free to reclaim it. The rule that guarantees it is unconditional, and depends only on *how the image is referenced in the cluster*:

| Reference in the cluster | Destination tag(s) pushed |
|---|---|
| by tag (`repo:v1.15.1`) | the origin-derived tag (`v1.15.1_<clusterID>`) |
| by digest (`repo:v1.15.1@sha256:D` or `repo@sha256:D`) | an **anchor tag** `sha256-<D>_<clusterID>`, pushed alongside the manifest at copy time |
| both | both |

Nothing is conditional on history, on `driftPolicy`, or on what a tag currently points to — so there is no "check before acting" and no ordering constraint to get right. A ref pinned with no tag at all falls out of the same rule rather than being a special case. The cost is one extra tag per pinned image in use, which is also a convenience when inspecting the registry: an `sha256-…_cluster-a` tag shows at a glance which digest that cluster pins.

### A.7 Queue the work and pace it

Copying is **a work queue per source registry, drained** — not a ring to spin. Unlike checking (which never ends: everything must be re-verified forever), copying terminates by nature: once everything desired is on the destination, there is nothing to do until a new image shows up. So:

- The queue lives **in memory**, fed by pod events and by the desired-state recomputation. No cursor, no persistence, nothing to resume.
- Entries already known to be handled — repositories listed in `status.repositories`, or refs found present at the destination — are not enqueued.
- Consumption is paced by the source registry's **copy windows** (`copy.interval` in the global config), counted from the start of the process and held in memory too. A restart re-phases them and the first copy waits a full `interval`, so the source sees at most one image per `interval` however often the controller restarts. Nothing to persist, and far cheaper than an etcd write per copy.
- **Re-copies come first.** A manifest that disappeared from the destination is an active availability hole — pods may be routed to it right now — whereas an initial copy is a background task.

### A.8 Record the repository, then push

**Write the repository into `status.repositories` before the first push into it.** The registry offers no way to enumerate the repositories a client has populated (the OCI spec only guarantees per-repository tag listing), so this list is the only thing that will let the cleanup loop find this repository again. A crash after pushing but before recording would create a repository no loop ever visits — a permanent leak.

**The reverse crash order needs no bookkeeping.** If the controller dies after recording but before copying, the self-check finds the desired image missing at the destination and re-copies it; the cleanup finds no tag of ours in that repository and retires the entry. Both loops already handle it, so the copy path carries no crash-recovery logic of its own.

Then perform the copy:

1. **Resolve source credentials**, in order: explicit `auth` on the entry → the pod's `imagePullSecrets` *if* the controller has been granted read access in that namespace → the registry's `perPrefixFallbackAuth` (longest matching prefix) → anonymous.
2. **Multi-cluster fast path:** `HEAD` the manifest **by digest** in the destination repository. If another cluster already pushed it, the copy reduces to a `PUT` of this cluster's tag — a few KB, zero blob transferred. Blobs are linked per repository, so clusters sharing a repository share them natively.
3. **Otherwise copy verbatim**: the index and all its children, digest preserved end to end.
4. **Tag it** as computed in A.6, and record the origin on the pushed manifest as OCI annotations (`kuik.enix.io/source-ref`, `kuik.enix.io/source-digest`). These make the artifact self-describing for a human running `crane manifest`, whatever mangling the tag went through.

### A.9 Report

- **Success** → `ImageCopied` event on the CR; `status.images.copied` reflects the new count on the next status write.
- **Failure** → an entry in `status.failedImagesCopy` with a stable reason (`SourceUnavailable`, `QuotaExceeded`, `AuthFailed`, `PushFailed`) and `lastAttempt`. The image stays in the desired state and will be retried; nothing is removed because a copy failed.
- **No source available at all** → counted in `status.images.missingSource`. This is the "we cannot protect this image" signal.
- Status writes happen **on transitions**, not per copy.

## Part B — The self-check loop

The destination registry is the source of truth for "what is copied". The self-check loop is what keeps that truth aligned with the desired state, and it is what makes the mirror trustworthy: a mirror that silently lost images is worse than no mirror.

### B.1 What it compares

- **Desired**: the same set as A.5 (origin refs of in-scope pods ∪ retained refs carrying an origin).
- **Observed**: the destination, interrogated **one precise reference at a time**.

The comparison always runs **forward**: for each desired ref, compute the destination ref (A.6) and ask about *that*. No destination ref is ever parsed backwards, so hashed/truncated tags are handled like any other.

### B.2 Never enumerate, always ask precisely

The OCI Distribution spec guarantees only `GET /v2/<name>/tags/list` per repository. The self-check therefore issues `HEAD /v2/<repo>/manifests/<ref>` for each desired ref, which needs read access on that repository only.

For pinned images the check is done **by digest**, which is an exact check: if the digest is present, the content is provably identical.

### B.3 Traverse the whole desired state, unpaced

Intervals exist to stay inside the quotas of the registries kuik **pulls from** ([Scheduling](../spec.md#scheduling)). A destination is written by kuik and belongs to the operator, so nothing about it is paced: the self-check waits for no window, holds no cursor and reports no cycle. Every reconcile compares the whole desired state, and the freshness of a verdict is the reconcile cadence rather than a lap time.

What bounds the cost is the size of the desired state, one `HEAD` per desired reference (B.2), against a registry that is usually on the same network as the cluster. There is nothing to resume after a restart: the first reconcile checks everything, which is also what covers the crash window of A.8.

### B.4 Handle each divergence

| Observation | Meaning | Action |
|---|---|---|
| Manifest absent | Something deleted the copy (external GC, purge, retention policy on the registry) | **Re-copy, with priority over initial copies** — pods may be routed here right now, this is an active availability hole. Emit `ImageRecopied` (a warning: it reveals an infrastructure problem) |
| Manifest present, **this cluster's tag** missing | Another cluster's tag holds the manifest; ours was removed | Just `PUT` the tag again — no blob transfer |
| Manifest present but incomplete | An earlier copy was interrupted mid-push | Re-copy the missing parts |
| Upstream tag now resolves to a different digest | Only meaningful with `driftPolicy` ≠ `Ignore` | See B.5 |

When a re-copy is needed, the source is the origin ref recorded in the desired state. It is also the self-check that covers the crash window of A.8 (repository recorded, copy never performed): the image is simply seen as missing and copied.

### B.5 The drift leg (source side)

`driftPolicy` decides what the mirror does when the *upstream tag* moves under it (typical of mutable tags like `latest`):

- **`Ignore`** (default) — nothing. The mirror is a snapshot of what was running; upstream mutations, accidental or malicious, do not propagate.
- **`Warn`** — re-check the source tag's digest on the **source** host's check windows (this is a read of an upstream, so it is paced like any other) and compare it against the copied manifest's digest (a straight equality, since copies are verbatim). Surface the divergence (`ImageTagDrifted`, `status.images.drifted`) without touching the copy. Detection without mutation.
- **`Sync`** — same detection, plus a re-push so the destination tag follows upstream. Emits `ImageResynced` (normal — this is Sync's steady state, not to be confused with `ImageRecopied`).

**`Sync` needs to know nothing about pinning.** Repointing `v1.15.1_cluster-a` from D to D′ cannot orphan D, because a pinned digest never depended on that tag: it carries its own anchor tag from copy time (A.6). So the resync path is simply "push D′, repoint the tag" — no check, no ordering constraint, no special case.

Pinned references remain desired-state entries **keyed by digest**, independent of what any tag points to, with their own retention clock: `Sync` advances the tag, it never shortens the life of a digest still in use.

### B.6 Report

`DestinationInSync` reflects whether observed matches desired. A failing credential or an invalid configuration flips `Ready` instead — the two conditions answer different questions ("is the mirror complete?" vs "can the mirror work at all?").

---

## Part C — The cleanup loop

Cleanup runs only when `cleanup.enabled` is set, and every step below is biased toward *not* deleting something that might still be needed.

### C.1 Detect that a reference became unused

Two paths feed `status.pendingDeletion`:

1. **Pod events** — when the **last** live pod referencing an origin ref disappears, the controller records the destination tag it computed for it, with `unusedSince = now`.
2. **The tag sweep** — every reconcile begins by listing the tags of each repository in `status.repositories` (C.3), keeps this cluster's tags, and records any of them that falls outside the expected set of the desired state.

The sweep is the garbage collector, and it is what makes `pendingDeletion` recomputable: it reads the destination rather than remembering what was running, so a tag that fell out of use while the controller was down is picked up at the first reconcile after startup. Losing the entries costs a restarted retention clock, never a leaked tag.

Entries are keyed by the **destination tag** — the thing that eventually gets deleted — with the origin ref alongside it when a pod event supplied one. Nothing is un-computed from a tag: A.6 is one-way, so a swept tag is compared against expected tags computed forward, and a swept entry carries no origin.

`unusedSince` is stamped once, when the entry appears. A reconcile that finds the entry already there leaves its clock untouched, otherwise the retention would never elapse.

If a pod referencing the image comes back (a CronJob's next run), the desired state yields that same destination tag again, the entry leaves `pendingDeletion` and the image returns to the plain desired state.

**Crash-recovery is conservative by construction:** a reference that disappears while the controller is down is stamped at the first reconcile that notices it — later than reality, which *lengthens* the retention. The error always falls on the safe side.

### C.2 Wait out the retention

During `cleanup.retention`, an entry carrying an origin ref is still:

- part of the desired state,
- verified by the self-check loop,
- **re-copyable** if it disappears from the destination.

A swept entry carries no origin, so it is held, then deleted: its image sits outside the desired state, hence outside the self-check and the copy queue.

This is what lets a CronJob whose period is shorter than the retention keep its image mirrored between runs, and what prevents a destination-registry outage from being interpreted as "these images are gone, purge the state". Only an **effective deletion performed by this controller** removes an entry from the desired state — never an observed absence.

### C.3 Sweep the repositories

Because there is no way to ask the registry "what did I put here?", the sweep is driven by `status.repositories`. For each repository in the inventory:

1. `GET /v2/<repo>/tags/list` — the one discovery primitive the OCI spec guarantees.
2. **Filter to this cluster's tags** (`_<clusterID>` suffix). Tags belonging to other clusters are not ours to reason about, and must never be touched.
3. **Diff forward against the desired state.** For each desired ref, compute the destination tag it should have; any of our tags outside that expected set enters `pendingDeletion` (C.1) if it is not listed there already, and is deleted once its `unusedSince` is older than `retention`.

   The expected tags of an entry are computed exactly as at copy time (A.6): the origin-derived tag for a tag reference, the `sha256-<D>_<clusterID>` anchor for a digest reference, both when the image is referenced both ways. Since that mapping is unconditional, nothing has to be remembered about what was pushed earlier. When an entry leaves the desired state and its retention elapses, its tags leave the expected set together and are deleted, after which the manifest becomes untagged and normal registry GC applies (C.5).
4. **Delete by tag** — using the OCI 1.1 tag-deletion API where the registry supports it. Never delete blindly by digest: deleting a manifest by digest removes *every* tag pointing at it, which on a shared repository would destroy other clusters' references, and even on a single cluster would take down `v1` when you meant `latest`.

The repository grain is what makes this exact: listing a repository's tags re-enumerates everything, including refs whose last reference vanished while the controller was down. An inventory of individual refs would miss exactly those.

### C.4 Retire the repository entry

A repository is removed from `status.repositories` **only when no tag of this cluster remains in it**. Another cluster may still have tags there — that is fine and expected; each cluster's inventory is a purely local view. Repository R can stay inventoried on cluster A while cluster B has already emptied its own.

### C.5 Leave the last mile to the registry

Deleting the last tag pointing at a manifest leaves it **untagged**: it disappears from `tags/list` but still occupies storage until the registry collects it. That reclamation belongs to the registry's own garbage collector (distribution's GC, Harbor's "delete untagged"), which sees all clusters and is the natural coordinator. The controller never deletes a manifest by digest — a manifest may carry tags from other clusters, and only the registry has the global view needed to decide it is truly unreferenced.

---

## Cross-cutting invariants

Worth keeping in mind while implementing any of the three loops:

- **Persist before acting.** Repository recorded before the first push; `unusedSince` recorded before the retention clock is trusted. In both cases a crash then wastes work, instead of losing safety.
- **Not everything in the status weighs the same.** One entry cannot be recomputed and losing it does real damage: `repositories` (the registry cannot be asked which repositories we populated, and a repository nobody sweeps is a permanent leak). Two more are kept for continuity, and losing them only costs redundant work, a restarted clock or a gap in reporting: `pendingDeletion` (the tag sweep of C.1 finds the unused tags again, retention restarting from that moment) and `failedImagesCopy` (rebuilt at the next attempt). Everything else in the status — the `images` aggregates, the conditions — is derived and recomputed on every reconcile, as are the things that never reach etcd at all: what is in use, what is copied, check verdicts, the copy queue, the window each registry is paced by.
- **Every computation runs forward**, from the desired state toward the destination. The destination layout is one-way; no loop reads a mirror ref backwards, so tag truncation is harmless.
- **The origin ref comes from the pod annotation**, never from un-computing a mirror ref.
- **The destination is the source of truth** for what is copied and for what is left to delete, never an internal list.
- **Deletion is always narrower than it looks**: by tag, only our tags, only after retention, only performed by us.
- **Every loop degrades gracefully**: longer intervals → slower; registry down → retries; controller restarted → recomputes from the desired state. No loop has to complete a full pass to remain correct.

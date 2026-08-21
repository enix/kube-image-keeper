# 0001 — v2 reuse analysis and rewrite decision

**Date:** 2026-08-21 · **Status:** decided

## Decision

v3 is a **rewrite of the domain, in the existing repository** — not an incremental
evolution of the v2 code, and not a from-scratch scaffold in a new repo.

Concretely:

- the v2 domain code (API types, filtering, priority system, mirror state machine,
  webhook CR-collection logic) is replaced, not migrated;
- `internal/registry` (+ `credentialprovider`), the webhook's probing spine
  (caches / singleflight / `parallel.FirstSuccessful`), `SecretOwnerReconciler` and the
  per-registry config-merge pattern are lifted and completed;
- all infrastructure (Makefile, lefthook, CI workflows, Dockerfile, Helm skeleton,
  website pipeline, envtest/e2e harness, semantic-release setup) transfers as-is.

Basis: full read of the v3 spec ([PR #629](https://github.com/enix/kube-image-keeper/pull/629):
`spec.md`, `status.md`, the three walkthroughs, `open-questions.md`) against an inventory
of the v2 codebase (6,431 production Go LOC, 4,199 test LOC).

## The numbers

| Bucket | ~LOC | % of production code |
| ------ | ---- | -------------------- |
| Reusable as-is | ~1,500 | ~23% |
| Adaptable (keep structure, re-bind to v3 types) | ~1,700 | ~26% |
| v3-obsolete | ~3,000 | ~47% |
| Misc / unclassified | ~230 | ~4% |

(Excluding the 845 generated lines of `zz_generated.deepcopy.go`, the obsolete share is
~39% — still the largest bucket.)

## Why evolving v2 does not work

It is not the volume, it is that **every axis of the domain model changes at once**:

- **API**: 5 CRDs → 3 (`ImageAlternative`, `ImageMirror`, `ImageMonitor`), all
  cluster-scoped, namespaced variants dropped. `api/kuik/v1alpha1` is ~95% obsolete
  (survivors: the availability enum, `Cleanup`, a few small shapes).
- **Filtering**: unified `spec.filter` regex include/exclude → native `LabelSelector`s
  plus segment-granular `imagePrefix` matching (a path-segment trie). The trie has **no
  v2 ancestor**; `internal/filter` is almost entirely dead (only `pod_filter.go`
  survives, and only if the operator-level `skipLabels`/`skipAnnotations` are kept —
  the spec does not currently mention them, to be clarified).
- **Candidate ordering**: two-level signed/unsigned priorities + kind order
  (Original/CISM/ISM/CRIS/RIS) → `rewritePolicy` bands (`Always` / original pivot /
  `OnFailure`) with name sort. The v2 comparator and the 4-kind collection loop are dead.
- **Mirror state machine**: the v2 reconciler (~700 LOC) revolves around
  `status.matchingImages[].mirrors[].mirroredAt`. v3 inverts the model: the destination
  registry is the source of truth, persisted state shrinks to `status.repositories` +
  `pendingDeletion`, cleanup works by tag listing filtered on the `_<clusterID>` suffix.
  Different program.
- **Digest-pinned images**: v2 skips them at 5 explicit `strings.Contains(image, "@")`
  sites; v3 routes them. An invariant inversion, not a patch.
- **Auth**: v3 explicitly forbids what v2 does (borrowing pod `imagePullSecrets` as
  controller credentials), resolves `secretRef` in a single namespace, and adds ambient
  cloud identity (`aws`/`gcp`/`azure`) — zero cloud SDK in today's `go.mod`.

Migrating incrementally would mean fighting the old model at every commit while keeping
the five v2 CRDs alive through the transition. Migration cost > rewrite cost.

## What we lift, and its known gaps

### `internal/registry` + `credentialprovider` (~1,030 LOC + 458 LOC of tests)

Best lift candidate: near-zero coupling to v2 types (one import — the
`ImageAvailabilityStatus` enum, a mechanical re-point), battle-tested TLS/insecure
handling, keychain resolution (longest-path-wins docker keyring), deliberate removal of
429 from transport retries so rate limits surface as `QuotaExceeded`, header capture for
rate-limit detection.

Four gaps to close before it serves v3:

1. **`DeleteImage` deletes by digest** — under OCI that destroys *every* tag pointing at
   the manifest, including other clusters'. Exactly the anti-pattern the spec forbids
   ("delete by tag, never by digest"). Must be replaced by tag-scoped deletion (OCI 1.1)
   before any v3 use.
2. **`CopyImage` always rebuilds a filtered index**, which changes the digest. v3's
   `platforms.mode: All` requires a **verbatim** copy (digest preserved end to end —
   what enables multi-cluster blob sharing and digest-pinned routing). Needs a bypass
   branch; `Auto` (platforms from node labels) does not exist yet.
3. **Missing primitives**: pre-copy `HEAD`-by-digest existence check, tag listing
   (`remote.List` is used nowhere in v2), keeper/anchor tags, cloud provider auth.
4. **`HeaderCapture` is mutated on the client** — safe in v2 only because every caller
   builds a fresh client per check; not concurrency-safe if a client is shared across
   v3's concurrent probes.

### Webhook probing spine (~340 of 835 LOC of `pod_webhook.go`)

The TTL caches + singleflight + `parallel.FirstSuccessful` structure is described almost
verbatim by the spec's "Availability probing" section (`activeCheckCache`, "a 50 replica
rollout costs one resolution", "first success in list order"). The
collect-sort-dedup-probe *structure* survives intact; only the comparator and the CR
collection change. Also reusable: `original-images` annotation handling, container walk
with normalization, `ensureSecret` injection (simplified by v3's single source
namespace), the SSA patching patterns.

### Other keeps

- `SecretOwnerReconciler[T]` — generic finalizer + labelled-secret GC, zero v2
  semantics; re-register for the new kinds.
- Per-registry config merge (`registries.default` overlaid by `registries.<host>`) —
  the v2 shape matches the v3 global config exactly; add the new fields.
- Cleanup retention arithmetic (`deleteAfter = retention - since(unusedSince)` +
  min-requeue accumulation) — v3 keeps `cleanup.enabled`/`cleanup.retention` verbatim.
- Mirror-loop-prevention concept (`getAllMirrorPrefixes` + prefix exclusion) — v3 keeps
  and generalizes it; the namespace-bucketed map collapses to a flat set.
- `internal/parallel`, `internal/info`, `internal/config` histogram, envtest suite
  bootstrap, the whole infra/tooling layer.

## Genuinely greenfield (no v2 ancestor — budget as such)

- the `imagePrefix` path-segment trie (single-image vs subpath forms, admission-time
  form validation via CEL);
- epoch-aligned scheduling windows per registry host (v2 pacing is relative to the last
  check, not aligned; check rings with persisted cursors, copy queues);
- cloud ambient auth (`aws`/`gcp`/`azure` + `serviceAccountRef` token exchange);
- cleanup GC by tag listing + `_<clusterID>` suffix filtering, `status.repositories`
  inventory;
- pre-copy `HEAD`-by-digest fast path, keeper/anchor tags, `clusterID` tag suffixing
  including the 128-char truncate-and-hash rule;
- a **validating** webhook (v2 has none — only the mutating pod defaulter);
- the secret syncer (blind server-side apply, `(CR, namespace)` pairs,
  `ValidatingAdmissionPolicy` guard) — an entirely new design;
- the three status controllers (informer-driven, leader-elected, no registry calls from
  the webhook path);
- digest-pinned routing (remove the 5 explicit skips).

## Why the same repository

- The docs versioning is *already designed* for it: v2 docs will live on the `2.x`
  maintenance branch, exactly the mechanism `sync-docs` implements.
- `.releaserc.json` / semantic-release already handle maintenance branches, matching
  "v2 enters maintenance".
- Makefile, lefthook, CI workflows, Dockerfile, Helm chart skeleton, envtest/e2e harness
  transfer identically — hundreds of lines and weeks of tuning we do not replay.

A fresh Kubebuilder scaffold in a new repo would throw all of that away for the comfort
of a blank page.

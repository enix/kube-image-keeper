# 0002 — development pipeline and milestones

**Date:** 2026-08-21 · **Status:** draft

How we intend to organize the v3 build. This captures the proposed pipeline plus the
amendments coming out of the reuse analysis ([0001](./0001-v2-reuse-analysis.md)).
It will evolve as the first milestones land.

## Pipeline

- **Phase 0 — spec review.** Sweep the spec for ambiguities, contradictions and holes
  before planning; every unresolved question at this stage becomes a blocked PR later.
  The structural question (evolution vs rewrite) is **settled**: rewrite of the domain in
  the existing repo, with explicit lifts — see [0001](./0001-v2-reuse-analysis.md).
  Remaining known holes are tracked in the spec PR's `open-questions.md`, plus one found
  during the reuse analysis: are the operator-level `skipLabels` / `skipAnnotations`
  kept in v3? The spec does not mention them, and `internal/filter/pod_filter.go` only
  survives if they are.
- **Phase 1 — architecture document.** CRDs and their Go API types first (they are the
  contract everything else builds on), internal packages, and the lift list: taken from
  v2 as-is / adapted / rewritten. Section by section, 0001 already provides that list.
  Light ADR format for structural decisions, filed in this folder.
- **Phase 2 — milestones** (see below).
- **Phase 3 — issue generation.** Strict template: context + spec link, exact interface
  signatures involved, acceptance criteria, test plan, dependencies (`blocked-by: #N`),
  estimate. Context for generation = architecture doc **plus** spec, never the spec
  alone. Dependencies drive the loop: an agent only picks up unblocked issues.
- **Phase 4 — the loop.** Per issue: agent A writes failing tests (committed on the
  branch), agent B implements until tests + CI pass, agent C reviews the PR, a human
  merges. Human merge stays mandatory at least until mid-project — it is the natural
  throttle and the quality gate.

State machine on GitHub labels (`ready`, `in-progress`, `needs-review`, `blocked`),
driven with the `gh` CLI.

## Milestones

Order follows the dependency graph, not thematic grouping:

0. **Scaffolding** — branch `v3` (done), notes folder (done), CI green on the branch,
   empty-but-installable chart. Most of this is lifted, not built.
1. **API types / CRDs** — the three kinds, status types, CEL validation rules,
   generated deepcopy + manifests. Very early: test-writer agents need them.
2. **Cross-cutting contracts** *(amendment vs the original plan, which put the webhook
   here)*: the `imagePrefix` segment trie and the auth model (`secretRef`/`provider`
   union, single-namespace resolution, `perPrefixFallbackAuth`). All four consumers
   (webhook, mirror, monitor, secret syncer) depend on both — they are more structuring
   than any single component.
3. **Webhook** — mutating (routing: matching, candidate ordering, probing, annotations)
   and validating (form checks the CEL rules cannot express).
4. **Reconcilers** — mirror (copy / self-check / cleanup loops), monitor, status
   controllers, secret syncer.
5. **Registry layer completion** — the lifted `internal/registry` plus the gaps listed
   in 0001 (tag-scoped delete, verbatim copy, HEAD-by-digest, tag listing, keeper tags,
   cloud auth). Starts alongside milestone 2; listed here because the mirror reconciler
   is its consumer.
6. **Helm / packaging / docs** — chart with real CRDs, `ValidatingAdmissionPolicy` for
   the secret syncer, configuration reference, migration notes from v2.
7. **e2e and hardening** — Kind-based e2e over the walkthrough scenarios (the spec's
   walkthroughs 01–03 are effectively e2e test scripts already), multi-cluster shared
   destination scenario, chaos cases (registry down, controller restart mid-copy).

## Working agreements

- Conventional commits, enforced by conform (unchanged from v2).
- Every behaviour change ships with tests and docs in the same PR (unchanged).
- Decisions land here as numbered notes before the code that implements them.

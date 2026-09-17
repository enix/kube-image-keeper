# 0002 — development pipeline and milestones

**Date:** 2026-08-21 · **Amended:** 2026-09-17 · **Status:** active

How the v3 build is organised. Amendments are marked inline and dated.

## Pipeline

- **Phase 0 — spec review.** Sweep the spec for ambiguities, contradictions and holes
  before planning; every unresolved question at this stage resurfaces later. The
  structural question (evolution vs rewrite) is **settled**: rewrite of the domain in the
  existing repo, with explicit lifts — see [0001](./0001-v2-reuse-analysis.md). Done with
  the merge of the spec (PR #629, 2026-09-16); the open questions are in
  `docs/v3/open-questions.md`.
- **Phase 1 — architecture document.** CRDs and their Go API types first (they are the
  contract everything else builds on), internal packages, and the lift list: taken from
  v2 as-is / adapted / rewritten. Section by section, 0001 already provides that list.
  Structural decisions are filed in this folder.
- **Phase 2 — milestones** (see below).
- **Phase 3 — the build.** *(Amended 2026-09-17:)* milestone by milestone, one task at a
  time, driven by Paul with an agent in front of him: what to do, what to commit, when to
  open and merge a pull request. Progress is tracked in the shared artefact. See
  [0003](./0003-agent-orchestration.md). Tests are written as natural-language cases,
  one per behaviour, and those cases are the `It` and `Entry` strings of the Ginkgo
  specs ([0004](./0004-test-framework.md)), so the reviewed English lands in git next to
  the assertion it describes. The tree of cases is written and reviewed first, with
  `task test-outline` (`DIFF=<ref>` shows only what changed), and the bodies come after.

## Milestones

Order follows the dependency graph, not thematic grouping:

0. **Scaffolding** — the clean tree in `enix/kube-image-keeper`, notes folder, CI green,
   empty-but-installable chart. Most of this is lifted, not built. Done
   (2026-09-15).
1. **API types / CRDs** — the three kinds, status types, CEL validation rules,
   generated deepcopy + manifests. Very early: everything else builds on them.
2. **Cross-cutting contracts** *(amendment vs the original plan, which put the webhook
   here)*: the `repository` / `repositoryGroup` segment trie and the auth model
   (`secretRef`/`provider` union, single-namespace resolution, `fallbackAuth`). All four
   consumers (webhook, mirror, monitor, secret syncer) depend on both — they are more
   structuring than any single component.
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
   walkthroughs 01–03 are effectively e2e test scripts already, hence ready-made
   natural-language cases), multi-cluster shared destination scenario, chaos cases
   (registry down, controller restart mid-copy).

## Working agreements

- Conventional commits, enforced by conform (unchanged from v2).
- Every behaviour change ships with tests and docs in the same PR (unchanged).
- Decisions land here as numbered notes before the code that implements them. A note is
  amended in place while its status is `active`, with the amendment marked inline and
  dated (see [`README.md`](./README.md) for the status semantics); only a `decided` note
  is frozen and requires a superseding note.

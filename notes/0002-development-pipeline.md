# 0002 — development pipeline and milestones

**Date:** 2026-08-21 · **Amended:** 2026-09-09 · **Status:** active

How we intend to organize the v3 build. This captures the proposed pipeline plus the
amendments coming out of the reuse analysis ([0001](./0001-v2-reuse-analysis.md)).
It will evolve as the first milestones land; amendments are marked inline and dated.

## Pipeline

- **Phase 0 — spec review.** Sweep the spec for ambiguities, contradictions and holes
  before planning; every unresolved question at this stage resurfaces later, either as an
  issue that cannot be validated or as a blocked PR.
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
  signatures involved, acceptance criteria, test plan, dependencies (native GitHub
  issue dependencies), estimate. Context for generation = architecture doc **plus** spec, never the spec
  alone. *(Amendment, 2026-09-09:)* the acceptance criteria and test plan take one
  specific form — **natural-language test cases**, written in English in the issue body,
  one case per behaviour, stating context, action and expected result plainly enough that
  a reviewer who does not read Go can tell whether the case is right. That is the whole
  point of writing them in prose: natural language is cheaper to review than code, and
  once the cases are pinned down, turning them into tests is transcription rather than
  design. **Human validation of the issue is the quality gate**, carried by the `ready`
  label: no issue is started before a human has read its cases and declared it valid.
  It is the only human serialization point upstream of the loop, batched per milestone,
  and it is what moves the review of test intent ahead of the code instead of after it.
  Dependencies drive the loop: an agent only picks up issues that are both unblocked and
  validated.
- **Phase 4 — the loop.** Per issue: agent A transcribes the issue's test cases into
  failing specs (committed on the branch), agent B implements until tests + CI pass,
  agent C reviews the PR, a human merges. *(Amendment, 2026-09-09:)* A transcribes, it
  does not invent — the cases were settled and reviewed in phase 3. Ginkgo/Gomega is the
  single test framework (see [0004](./0004-test-framework.md)), and that is what makes
  the transcription faithful: each case's text becomes the string of its `It`, or its
  `Entry` in a `DescribeTable`, so the reviewed English lands in git, next to the
  assertion it describes, and stays greppable. A spec whose `It` string no longer matches
  any case in the issue is a visible discrepancy rather than a silent one. Human merge
  stays mandatory at least until mid-project — it is the natural throttle and the last
  quality gate.

State machine on GitHub labels (`ready`, `in-progress`, `needs-review`, `blocked`),
driven with the `gh` CLI. `ready` is applied by a human, never by an agent: it is the
phase 3 gate.

### Accepted trade-off: the reviewed cases live in the issues

They therefore live outside git — not versioned alongside the code they constrain, not
greppable from a clone, and an issue edited after validation leaves no trace in any diff.
Two things make that acceptable rather than careless. The transcription *does* land in
git: the `It` and `Entry` strings are the cases, in the repository, at the assertion. And
validation precedes all work, so nothing is ever built on an unreviewed case; a case
edited after the fact invalidates an issue, not a merged PR.

The alternative was reviewed case files committed in the repo, with status frontmatter,
stable case IDs and a CI check tying IDs to tests in both directions. Rejected on two
counts. It puts a second human gate *inside* the loop, when the point of the phase 3 gate
is that there is exactly one and it sits upstream. And every mechanism it needs in order
to be trustworthy rather than decorative — an unforgeable `reviewed` flag, a normative
ID-placement convention, a check scope that does not fail the whole repo on cases that are
reviewed but not yet implemented — is itself something to build, calibrate and maintain,
in exchange for traceability that grep over `It` strings already approximates. This is a
deliberate speed choice; if case drift starts producing real bugs, that design is the way
back.

## Milestones

Order follows the dependency graph, not thematic grouping:

0. **Scaffolding** — the `enix/kuik-v3` repository on its `main` branch (done), notes
   folder (done), CI green on the branch, empty-but-installable chart. Most of this is
   lifted, not built.
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
   walkthroughs 01–03 are effectively e2e test scripts already, hence ready-made
   natural-language cases for their phase 3 issues), multi-cluster shared
   destination scenario, chaos cases (registry down, controller restart mid-copy).

## Working agreements

- Conventional commits, enforced by conform (unchanged from v2).
- Every behaviour change ships with tests and docs in the same PR (unchanged).
- Decisions land here as numbered notes before the code that implements them. A note is
  amended in place while its status is `active`, with the amendment marked inline and
  dated (see [`README.md`](./README.md) for the status semantics); only a `decided` note
  is frozen and requires a superseding note. This note is an `active` one, and the
  2026-09-09 amendments above were applied that way.

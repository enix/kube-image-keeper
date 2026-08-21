# v3 working notes

Engineering journal for the v3 development effort. This folder keeps a trace of how we
organize the work: analyses, structural decisions, process notes, and anything worth
remembering that does not belong in the user-facing documentation.

These notes are **internal working material**: they live outside [`docs/`](../docs/) on
purpose, because everything under `docs/` is published to the website. Nothing here is
rendered anywhere; write for future contributors, not for end users.

The v3 specification itself is not here either — it lives in the
[v3 specification pull request](https://github.com/enix/kube-image-keeper/pull/629)
(`docs/v3/` on the `spec/v3` branch) until it is merged.

## Conventions

- One topic per file, numbered: `NNNN-short-slug.md` (next number = highest existing + 1).
- Every note starts with its title as an H1, then a **Date** / **Status** line.
  Status is one of `draft`, `active`, `decided`, `superseded by NNNN`.
- Structural decisions (the "ADR-light" format): state the decision up front, then the
  context and the alternatives that were rejected and why. A decision is amended by a new
  note that supersedes the old one, not by rewriting history.
- Notes are updated as understanding evolves; decisions are append-only.

## Index

- [0001 — v2 reuse analysis and rewrite decision](./0001-v2-reuse-analysis.md)
- [0002 — development pipeline and milestones](./0002-development-pipeline.md)
- [0003 — agent orchestration and guardrails](./0003-agent-orchestration.md)

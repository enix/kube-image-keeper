# v3 working notes

Engineering journal for the v3 development effort: analyses, structural decisions,
process notes, and anything worth remembering that does not belong in the user-facing
documentation.

These notes are **internal working material**: they live outside [`docs/`](../docs/) on
purpose, because everything under `docs/` is published to the website. Nothing here is
rendered anywhere; write for future contributors, not for end users.

The v3 specification is not a note either: it lives in [`docs/v3/`](../docs/v3/) and is
reviewed in the [v3 specification pull request](https://github.com/enix/kube-image-keeper/pull/629).

## When to write a note

The default home of a decision's "why" is the **commit body** of the change that
implements it. A note is warranted only when at least one of these holds:

- the decision constrains work beyond the change that implements it (several future
  changes or milestones);
- it rejects an alternative that someone will plausibly propose again;
- it has no single commit to live in (process, architecture, spec interpretation,
  tooling that spans the repository).

Notes record deliberation that actually happened. Never invent alternatives to fill the
template: if nothing was weighed, there is nothing to record beyond the commit.
Rationale in [0005](./0005-decision-log-filter.md).

## Conventions

- One topic per file, numbered: `NNNN-short-slug.md` (next number = highest existing + 1).
- Every note starts with its title as an H1, then a **Date** / **Status** line.
  Status is one of:
  - `draft` — work in progress, not yet a reference; may be incomplete, contradicted, or
    dropped. Edit freely in place.
  - `active` — the reference in force; the shape is settled but the details are still
    expected to move (e.g. pending calibration or upcoming work). Edit in place.
  - `decided` — frozen. Never edited beyond typo fixes; changed only by a new note that
    supersedes it.
  - `superseded by NNNN` — kept for history, no longer authoritative; `NNNN` is the note
    to read instead.
- **Decision notes are short**: the decision in one sentence up front, *Why* in two to
  four bullets, *Rejected* as one line per alternative, under 25 lines in total. A
  decision is amended by a new note that supersedes the old one, not by rewriting it.
- **Analyses** (`draft` / `active`) may be long, but open with a summary of at most five
  lines so a reader can stop there.
- The index below states **each decision in one line**, not just its title. Read the
  index first and open a note only when it matters for the task at hand; never load the
  whole folder.

## Index

- [0001 — v2 reuse analysis and rewrite decision](./0001-v2-reuse-analysis.md): v3
  rewrites the domain in the existing repository; `internal/registry`, the webhook's
  probing spine, `SecretOwnerReconciler` and the config-merge pattern are lifted, the
  rest is replaced.
- [0002 — development pipeline and milestones](./0002-development-pipeline.md): spec
  review, then architecture, then the build milestone by milestone; milestones ordered by
  dependency, API types first, e2e last.
- [0003 — how the work is driven](./0003-agent-orchestration.md): Paul drives every task
  with an agent in front of him and tracks progress in the shared artefact; nothing runs
  unattended, he alone commits, opens and merges.
- [0004 — Ginkgo everywhere as the single test framework](./0004-test-framework.md):
  Ginkgo/Gomega is the only test framework, `DescribeTable` for tables, no
  `[]struct{}` + `t.Run`.
- [0005 — notes only for decisions that outlive their commit](./0005-decision-log-filter.md):
  the commit body is the default; a note needs the filter above and stays short.
- [0006 — Task replaces the Makefile](./0006-taskfile.md): commands live in
  `Taskfile.yaml`; the `Makefile` is a shim forwarding to `task` for kubebuilder and the e2e
  suite.
- [0007 — Helm is the only deployment path](./0007-helm-only.md): the chart deploys kuik in
  dev, CI and production; `config/` is controller-gen output only; the kubebuilder Helm plugin
  is rejected.
- [0008 — Chart values: root defaults, one block per process](./0008-chart-values-per-process.md):
  pod settings at the root of `values.yaml` apply to the three processes; each process
  block repeats them empty and a value set there replaces the root one for that process.

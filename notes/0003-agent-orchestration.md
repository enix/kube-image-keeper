# 0003 — agent orchestration and guardrails

**Date:** 2026-08-21 · **Status:** draft

Operational side of phase 4 of the pipeline ([0002](./0002-development-pipeline.md)):
how the agent loop actually runs, what keeps it from going off the rails, and how the
TDD split is enforced mechanically rather than by convention. Written before the loop
exists; will be amended once the first issues have gone through it.

## Where the loop runs

Development happens on a **fork** of the official repository, not on
`enix/kube-image-keeper` — if an agent misbehaves, the blast radius is the fork.
Work merges back to the official repo in reviewed batches (per milestone, or per
release cut).

Two orchestration modes, used in sequence:

1. **Supervised local loop (first 3–4 issues).** Claude Code driven interactively or
   headless (`claude -p`), one issue at a time, picked from `gh issue list
   --label ready`. The point of this stage is not throughput: it calibrates the issue
   template, the agent prompts, and the CI gates while a human is watching every step.
   Issues that flounder here mean the template is wrong — fix the template, not the
   agent.
2. **Automated loop (once the supervised runs pass cleanly).** GitHub Actions with
   [claude-code-action](https://github.com/anthropics/claude-code-action), triggered by
   the `ready` label. History then lives in the issues and PRs themselves, which is
   also what makes the loop resumable and auditable.

The label state machine from [0002](./0002-development-pipeline.md) (`ready`,
`in-progress`, `needs-review`, `blocked`) gains one terminal state: **`needs-human`**
(see guardrails).

## The TDD split, enforced

Per issue: agent A writes failing tests, agent B implements until tests and CI pass,
agent C reviews the PR, a human merges. The split only means something if B cannot
"fix" the tests to make them pass:

- **Interfaces are frozen before A starts.** The exact signatures live in the issue
  (phase 3 template); A tests against them, B implements them. If B discovers the
  interface is wrong, the move is `needs-human` + a comment, never a silent test edit.
- **B is mechanically barred from touching tests.** A CI check on implementation PRs
  fails if the diff modifies any `*_test.go` file (or `test/e2e/`) relative to A's
  test commit. Convention does not survive an agent under pressure to go green;
  a failing check does.
- **CI is the real reviewer.** Agent C reviews for design and spec conformance, but
  correctness pressure comes from the harness: build, lint, `make test` (envtest),
  conventional commits, and the no-test-edits check. All of it must exist before the
  loop starts — it is part of milestone 0, not something added when the first PR
  misbehaves.

## Guardrails

The fork is the outer containment; these are the inner ones:

- **Branch protection** on the fork's `main` and `v3`: PR required, CI green required,
  no force-push, no direct commits from the agent token.
- **Minimal-scope token** for agents: contents + issues + pull-requests on the fork
  only. No org scopes, no workflow-file write.
- **Bounded retries.** An agent gets N attempts (start: 3) at making CI pass on an
  issue. After N failures it labels the issue `needs-human`, comments what it tried,
  and moves on. Insisting is the failure mode that burns budget and produces the
  worst code.
- **Human merge** stays mandatory at least until mid-project ([0002](./0002-development-pipeline.md)) —
  it is the throttle and the last quality gate. Agent C never merges, and never
  reviews its own kind's work on the same issue.

## Issue calibration

Autonomous success is mostly determined before the agent starts:

- **One issue ≈ one PR of 200–500 changed lines.** Larger means the issue should have
  been split in phase 3; agents flounder on vague or oversized issues far more than on
  hard-but-scoped ones.
- **`CLAUDE.md` is the agents' only shared memory.** Groomed as part of milestone 0:
  conventions, commands, where the spec and these notes live, the PR template, the
  label state machine. Every agent starts cold; this file is what makes runs
  consistent.

## Timeline

Target: **alpha in September, beta in October, stable in stride.** Feasible given the
reuse basis from [0001](./0001-v2-reuse-analysis.md) (~half the production code lifted
or adapted, all infrastructure transferred) **if** the first week goes entirely to
phases 0–3. That week looks slow — no feature code lands — but it is what makes the
following three weeks fast: the loop's throughput is a function of issue quality, not
agent count. If the schedule slips, cut alpha scope to the critical path (webhook
routing + `ImageAlternative`), not the process.

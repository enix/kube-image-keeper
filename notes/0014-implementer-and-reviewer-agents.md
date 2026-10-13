# 0014 — One implementer and fresh reviewers per pull request

**Date:** 2026-10-13 · **Amended:** 2026-10-13 · **Status:** active

**Decision:** the session that owns a pull request launches one `implementer` agent, the
only writer in the worktree, and resumes it for every component and findings batch. A fresh
read-only `reviewer` reviews each `feat` commit as it lands, and another the whole branch
before the pull request opens. The implementer applies review findings and decides spec
readings without approval. *(Amended 2026-10-13: the outline, checked by `spec-reviewer`, is
reported and not waited on; the maintainer stops the session if needed.)* The work stops
only for a test change that opens a gap with `docs/v3/`, a fix in a shared package, or
anything outside the worktree.

**Why:**

- A trial on one epic pull request: the whole-branch review found the most valuable bugs,
  those between components that no single-commit review could see.
- A fresh reviewer starts from zero and does not inherit the implementer's view.
- Hooks hold the boundaries: the implementer cannot push, rewrite history or reach GitHub
  or a cluster; the reviewer only reads.

**Rejected:**

- A separate fixer agent for review findings: 2 writers in one worktree.
- A final `spec-reviewer` pass on the code: the whole-branch `reviewer` covers conformance;
  `spec-reviewer` keeps the outline.
- Showing every review-round test change to the maintainer: it stalled the pull request on
  fixes the maintainer approved anyway; only a gap with `docs/v3/` goes to the maintainer.

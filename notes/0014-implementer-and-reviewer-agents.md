# 0014 — One implementer and fresh reviewers per pull request

**Date:** 2026-10-13 · **Amended:** 2026-10-13, 2026-10-28 · **Status:** active

**Decision:** the session that owns a pull request launches one `implementer` agent, the
only writer in the worktree, and resumes it for every component and findings batch. A fresh
read-only `reviewer` reviews each `feat` commit as it lands, and another the whole branch
before the pull request opens. The implementer applies review findings and decides spec
readings without approval; the outline, checked by `spec-reviewer`, is reported and not
waited on. The work stops only for a test change that opens a gap with `docs/v3/`, a fix in
a shared package, or anything outside the worktree.

**Why:**

- A trial on one epic pull request: the whole-branch review found the bugs between
  components, which no single-commit review saw; a fresh reviewer starts from zero.
- Hooks hold the boundaries: *(Amended 2026-10-28:)* the implementer cannot push, rewrite
  history, write to GitHub (it reads with `gh ... view`) or reach a cluster; the reviewers
  run only git reads (`review-guard.sh`).

**Rejected:**

- A separate fixer agent (2 writers in one worktree); a final `spec-reviewer` pass (the
  whole-branch `reviewer` covers it); every test change shown to the maintainer.

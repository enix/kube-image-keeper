# 0018 — Every test commit is red, a bug fix included

**Date:** 2026-10-28 · **Status:** decided · Supersedes in part [0012](./0012-e2e-specs-last-in-a-pr.md)

**Decision:** every spec is written before the code that turns it green. A bug fix is no
exception, whatever finds it: its regression spec comes first, in a `test(<scope>)` commit
where it fails (with a stub when it needs a symbol only the fix defines), then the
`fix(<scope>)` commit. `task check-commits` runs the specs of each `test` commit there and
fails when they all pass. The e2e cases join the outline from the start as empty `PIt`
specs, checked by `spec-reviewer`; their bodies still come in one last `test` commit (0012).

**Why:**

- The rules allowed "fix first, spec after" for a bug a review finds in code the pull request
  does not own; it was read as covering bug-fix pull requests, which were written that way.
- A spec written after its fix was never seen failing: nothing shows it would catch the bug.
- Outlining the e2e cases first decides what the end-to-end run must prove before the code,
  without paying for a Kind run on every change.

**Rejected:**

- Keeping the fix-first exception for bugs found in code already on `main`: nothing stops
  writing the spec first there too.
- Writing the e2e bodies from the start: the suite runs once at the end anyway.

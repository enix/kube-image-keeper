# 0013 — The e2e-ready label gates the E2E check

**Date:** 2026-09-30 · **Status:** decided

**Decision:** once the PR is reviewed, autosquashed and force-pushed, the author adds the
`e2e-ready` label. E2E is one required check that fails without the label or while the PR
holds `fixup!` commits, passes without running when no path the suite depends on changed,
and runs the suite otherwise.

**Why:**

- e2e specs come last in a PR ([0012](./0012-e2e-specs-last-in-a-pr.md)), and E2E never ran on pull requests.
- The suite runs once, on the branch as it merges: a run before the autosquash would be
  repeated by the force-push on the same content. A failure is fixed with `fixup!` commits,
  which pause the suite until the next autosquash.

**Rejected:**

- Running only the e2e specs a PR adds: setup dominates the time; it misses regressions.
- Running e2e once every other check is green: it still runs on every push during review.
- A separate "E2E ready" gate job, or a job-level `if:`: a skipped required check passes.
- A `paths:` filter on `pull_request`: a required check that never triggers blocks forever.
- Caching a pass by file tree to skip the rerun: more fragile than refusing `fixup!` commits.
- GitHub merge queue: kept for later, as it changes how PRs are merged.

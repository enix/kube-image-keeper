# 0019 — A fix commit names the pull request that introduced the bug

**Date:** 2026-10-28 · **Status:** decided

**Decision:** every `fix` commit ends its body with `Introduced-by: #<PR>`, the pull
request that introduced the bug. The author finds it with `git log`, `git blame` and `gh`;
when they cannot, they ask the user, and if the user does not know either they write
`Introduced-by: unknown` and say so. `hack/check-commit-rules.sh` enforces it, run by
`task check-commits` and by the Conform workflow on every pull request; Dependabot's
commits are exempt, they fix no bug. The `release-notes` skill reads it: a fix of a
feature no release shipped is left out of the notes, a fix of a released one names the
version that brought the bug.

**Why:**

- The release notes are written with a skill from the beta on; they must tell a fix of a
  released version from a fix of work no user ever ran.
- Typing such fixes `feat` to keep them out of the changelog was tried earlier and was hard
  to apply consistently; one trailer on every fix is simpler and keeps the history.
- Checked in CI, it holds for every contributor, not only for the sessions.

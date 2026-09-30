# 0012 — e2e specs are written last in a PR

**Date:** 2026-09-30 · **Status:** decided

**Decision:** a PR adds its e2e specs in a final `test` commit, once it is otherwise ready
(automated review addressed, unit and envtest specs green, the feature believed to work).
Their corrections are `fixup!` commits, autosquashed once like review fixes. This is an
exception to "a test commit before each feat commit", which still holds for unit and
envtest specs.

**Why:**

- A Kind run takes minutes: iterating on e2e specs while the PR still moves wastes that
  time on every change.

**Rejected:**

- e2e specs only before a release: a failure found then spans every PR of the week and is
  hard to attribute; it blocks the weekly alpha behind fix PRs; and `main` has no e2e
  safety net between releases.

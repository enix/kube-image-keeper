# 0020 — The spec stays as written until 3.0 GA

**Date:** 2026-10-28 · **Status:** decided · Supersedes in part [0010](./0010-spec-as-is-until-the-alpha-runs.md)

**Decision:** `docs/v3/` stays as it is written until 3.0 GA, not only until an alpha
runs. A spec doubt or a divergence between the code and the spec is recorded outside the
repository, on the list of amendments kept per release, and never edited into `docs/v3/`
meanwhile. After GA, the amendments are folded into `docs/v3/`. The rest of
[0010](./0010-spec-as-is-until-the-alpha-runs.md) stands: an ambiguity is implemented in
its most literal reading, and `auth.provider` stays aside.

**Why:**

- 0010's trigger has passed: the alphas run, and the beta is out, yet the spec is still the
  reference the sessions, the reviewers and CodeRabbit check the code against.
- Editing the spec release by release would mix what 3.0 targets with what it ships, while
  the amendments list already records each divergence with its release.

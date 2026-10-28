# 0015 — Release acceptance: the tooling acts, the agent judges

**Date:** 2026-10-12 · **Status:** decided; scenario drafting superseded by [0016](./0016-acceptance-plays-the-use-cases.md)

**Decision:** An agent (the `release-acceptance` skill) accepts a release on a cluster where kuik runs. It runs the user stories of `test/acceptance/scenarios.md`, drafted per release by the `acceptance-scenarios` skill and validated by the user. The program under `test/acceptance/` only acts and collects: `setup` applies the YAML fixtures, `apply` and `delete` change test objects, `check` and `inspect` print what the cluster holds, `cleanup` removes everything. It asserts nothing: the agent explores and judges against `docs/v3/`. One guard in code checks every write, so the tasks run without asking; only cleanup asks, once the user has accepted the results.

**Why:**

- An assertion only finds the bug its author thought of. Acceptance must find the others: a status that contradicts an event, a counter off by one, a log line nobody expected.
- Code that creates, checks and deletes is an e2e suite on a real cluster; Kind covers it.
- YAML fixtures show a reviewer what a run creates; they decode strictly, so an API break shows.

**Rejected:**

- `sigs.k8s.io/e2e-framework` with Gomega assertions ([#706](https://github.com/enix/kube-image-keeper/pull/706), first form): fixed verdicts, cleanup by the code.
- Fixtures built in Go: typed, but a reviewer reads code to know what a run creates.
- The Bash script before it: untyped, a CRD change breaks it silently.

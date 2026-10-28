# 0016 — Release acceptance plays the documented use cases

**Date:** 2026-10-28 · **Status:** decided

**Decision:** The scenarios of `test/acceptance/scenarios.md` are the user stories kuik sells, the use cases of [`docs/use-cases/`](../docs/use-cases/), each played end to end on a real cluster with real registries; the `release-acceptance` agent explores off-script variants from them. Every precise behaviour (an event, a reason, an annotation, a counter), a bug fix's included, is checked by the Kind e2e suite or envtest, never by acceptance. Supersedes the per-release scenario drafting of [0015](./0015-acceptance-tooling-acts-the-agent-judges.md); its tooling stays.

**Why:**

- Acceptance covers what Kind cannot reproduce: real registries and their quirks (Harbor, Docker Hub quotas, ECR, private registries, corporate CAs), the real install (GitOps, upgrade from the previous release, several replicas, existing admission policies), coexistence with real workloads, operations (real metrics, logs, restarts).
- A precise behaviour is checked faster and on every pull request by e2e or envtest; on a shared cluster it costs a manual run per release and finds nothing new.

**Rejected:**

- One scenario per behaviour a release changes (what the `acceptance-scenarios` skill drafted): it duplicated e2e and envtest.

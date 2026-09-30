# 0011 — The envtest label goes on the suite

**Date:** 2026-09-30 · **Status:** decided

**Decision:** supersedes the section "Fast feedback: Ginkgo labels" of
[0004](./0004-test-framework.md#fast-feedback-ginkgo-labels). Every envtest suite, the
webhook one included, carries `Label("envtest")` on its `RunSpecs`; `task test-short`
runs `go test -ginkgo.label-filter='!envtest'` on every package but e2e, without the
envtest binaries. `task test`, CI and `task test-e2e` are unchanged.

**Why:**

- No spec honoured `-short`: `test-short` ran the envtest suites like `task test`.
- The webhook's in-memory specs read Namespaces and Secrets through the API server, so they
  are not unit specs.
- Ginkgo skips `BeforeSuite` when the filter leaves a suite empty: a suite-level label is
  enough to keep envtest out of `test-short`.

**Rejected:**

- Spec-level labels in the webhook suite with a lazy envtest bootstrap, as 0004 planned: its
  in-memory specs still need the API server.
- A fake client for those specs: they would stop testing against a real API server, which
  kubebuilder scaffolds the webhook suite on.

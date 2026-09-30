# 0011 — envtest suites skip themselves in short mode

**Date:** 2026-09-30 · **Status:** decided

**Decision:** supersedes the section "Fast feedback: Ginkgo labels" of
[0004](./0004-test-framework.md#fast-feedback-ginkgo-labels). Every envtest suite, the
webhook one included, starts its `func TestX` with
`if testing.Short() { t.Skip("envtest suite") }`; `task test-short` runs `go test -short`
on every package but e2e, without the envtest binaries. `task test`, CI and
`task test-e2e` are unchanged.

**Why:**

- `go test` caches a run only when every flag is in its cacheable list: `-short` is, a
  `-ginkgo.*` flag is not.
- The webhook's in-memory specs read Namespaces and Secrets through the API server, so they
  are not unit specs: the whole suite is skipped.

**Rejected:**

- Spec-level labels in the webhook suite with a lazy envtest bootstrap, as 0004 planned: its
  in-memory specs still need the API server.
- A fake client for those specs: they would stop testing against a real API server, which
  kubebuilder scaffolds the webhook suite on.

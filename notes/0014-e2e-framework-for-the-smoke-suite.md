# 0014 — e2e-framework for the smoke suite on existing clusters

**Date:** 2026-10-09 · **Status:** decided

**Decision:** Amends section "Decision" of [0004](./0004-test-framework.md) for one suite.
The smoke suite `test/smoke/` (run against a cluster where kuik is already installed) runs on
`sigs.k8s.io/e2e-framework` instead of Ginkgo, with a client built from an explicit
`--kubeconfig` and `--context` only. It still asserts with Gomega (`NewWithT`), and Ginkgo
stays the runner of every other suite.

**Why:**

- It is built for clusters it does not create (setup and teardown per feature, filters, any
  kubeconfig); our e2e suite builds, loads and installs kuik on Kind.
- `Assess("...")` carries the natural-language case, so 0004's traceability holds.
- Kubebuilder's Ginkgo comes from envtest, not from a choice against it
  ([kubebuilder#1336](https://github.com/kubernetes-sigs/kubebuilder/issues/1336)).
- Left alone it falls back on the current context or `~/.kube/config`: hence the explicit client.

**Rejected:**

- The Ginkgo e2e suite with an existing-cluster mode: its Kind setup would have to be unhooked.
- The Bash script of [#706](https://github.com/enix/kube-image-keeper/pull/706): untyped, a CRD
  change breaks it silently.
- `helm test`: the chart would ship RBAC creating cluster-scoped CRs; Argo CD ignores the hooks.
- Chainsaw: one more binary and a YAML test language next to Go.

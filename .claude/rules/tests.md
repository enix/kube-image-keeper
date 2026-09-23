---
paths:
  - "**/*_test.go"
  - "test/**"
---

# Test conventions

- **Ginkgo + Gomega only** ([0004](../../notes/0004-test-framework.md)): `DescribeTable` for
  tables, no `[]struct{}` + `t.Run`.
- **Cases before bodies.** The `It` and `Entry` strings are natural-language test cases,
  one per behaviour, and they are reviewed before the bodies are written. Write the tree
  first with pending specs (`PIt`, `PEntry`), show it with `task test-outline -- <path>`
  (or `task test-outline DIFF=origin/main` for the cases added and removed since `main`),
  then fill the bodies once the cases are agreed. The outline tool is `hack/testoutline`.
- **A test exercises a behaviour, not a value.** Every spec must be able to fail on a
  change worth catching. Do not write a spec that reads a literal back (a field of a
  hard-coded list, a constant), that compares a file to a copy of itself, or that repeats
  a passing assertion under a different `It` string: that is testing 1 == 1. One spec per
  rule, not one per verb, kind or field the rule applies to: when a rule grants `get, list,
  watch`, one verb stands for the three. The bar rises with the cost of the suite: a unit
  spec may pin something basic, an envtest spec must exercise a reconciliation, and an e2e
  spec (Kind cluster, minutes per run) must check something only a real cluster can
  answer.
- **Suites** are `suite_test.go` files on envtest and load the CRDs from
  `config/crd/bases/`: run `task manifests` before testing a type change.
- **Run one spec** by filtering on the `It` text, not on `-run`:

  ```sh
  go test ./internal/controller/kuik -v -ginkgo.focus 'text of the It'
  ```

- **e2e** (`test/e2e/`, `task test-e2e`) runs on an isolated Kind cluster created and
  deleted by the task. Never run it against a real cluster.

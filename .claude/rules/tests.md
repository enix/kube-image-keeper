---
paths:
  - "**/*_test.go"
  - "test/**"
---

# Test conventions

- **Ginkgo + Gomega only** ([0004](../../notes/0004-test-framework.md)): `DescribeTable` for
  tables, no `[]struct{}` + `t.Run`.
- **Cases before bodies, then stop.** The `It` and `Entry` strings are natural-language
  test cases, one per behaviour. Write them as `PIt` / `PEntry` with empty bodies (plus the
  stubs needed to compile), show them with `task test-outline` and stop: the maintainer
  reviews the cases before any body or implementation exists. An agent ends its turn on the
  outline. Follow the [`test-outline`](../skills/test-outline/SKILL.md) skill.
- **Tests before implementation, one pair of commits per component.** First a
  `test(<scope>): ...` commit with the full bodies against the stubs: the specs fail there,
  `task lint-fix` passes. Then the `feat(<scope>): ...` commit that turns exactly those specs
  green, and only then the next component. A `feat` commit touches no `*_test.go` file and
  no test helper package: `git log --stat` shows every test file added by a `test` commit
  and left alone by the `feat` commits.
- **A wrong test is reported, never edited to pass.** A spec that contradicts `docs/v3/` or
  cannot be written as stated goes to the maintainer with the section it relies on. It
  changes only with the maintainer's approval, in its own `test` commit. The same holds for
  any change to a test after its `test` commit: a `fixup!` of a `test` commit, a fix to a
  test helper, a relaxed assertion. Show the maintainer its diff and its reason before
  committing it.
- **Regression tests follow a fix.** When a review (human, `spec-reviewer`, CodeRabbit)
  finds a bug the outline missed, the fix may come first and its regression spec in its own
  `test` commit after it. That commit says it is one: subject
  `test(<scope>): add regression specs for <what>`, and a 2 or 3 line body naming the review
  that found the bug, the component or commit subject that carries the fix (never a sha:
  the branch is rebased before merge, only hashes on `main` are stable), and that the specs
  pin the fix. The PR description names it in one line.
- **A test exercises a behaviour, not a value.** Every spec must be able to fail on a
  change worth catching. Do not write a spec that reads a literal back (a field of a
  hard-coded list, a constant), that compares a file to a copy of itself, or that repeats
  a passing assertion under a different `It` string: that is testing 1 == 1. The unit of
  coverage depends on the suite:
  - **envtest and e2e**: one spec per rule, not one per verb, kind or field the rule applies
    to. When a rule grants `get, list, watch`, one verb stands for the three. An envtest
    spec must exercise a reconciliation; an e2e spec (Kind cluster, minutes per run) must
    check something only a real cluster can answer.
  - **unit**: a mapping or a parser gets one `Entry` per distinct input value (each status
    code of an HTTP-to-reason mapping). A test that checks one row does not prove the
    mapping. A unit spec may also pin something basic.
- **Suites** are `suite_test.go` files on envtest and load the CRDs from
  `config/crd/bases/`: run `task manifests` before testing a type change.
- **An envtest suite skips itself in short mode**
  ([0011](../../notes/0011-envtest-suites-skip-in-short-mode.md)): its `func TestX` starts
  with `if testing.Short() { t.Skip("envtest suite") }`, so `task test-short` (the pre-push
  hook) runs the unit suites alone. A suite without it runs in `task test-short`, where no
  API server exists. Never pass a `-ginkgo.*` flag to `task test-short`: `go test` caches
  no run that has one.
- **Run one spec** by filtering on the `It` text, not on `-run`:

  ```sh
  go test ./internal/controller/kuik -v -ginkgo.focus 'text of the It'
  ```

- **e2e** (`test/e2e/`, `task test-e2e`) runs on an isolated Kind cluster: the task
  creates `KIND_CLUSTER` when it is missing and deletes it afterwards only in that case. Never
  run it against a real cluster. Follow the
  [`e2e-spec`](../skills/e2e-spec/SKILL.md) skill.
- **e2e specs come last in a PR** ([0012](../../notes/0012-e2e-specs-last-in-a-pr.md)): once
  the PR is otherwise ready (automated review addressed, unit and envtest specs green, the
  feature believed to work), a final `test` commit adds them. If they fail, the corrections
  are `fixup!` commits, autosquashed once like review fixes. This is the one exception to "a
  `test` commit before each `feat` commit", which still holds for unit and envtest specs: a
  Kind run takes minutes, so iterating on e2e while the PR moves wastes it.

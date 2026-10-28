---
paths:
  - "**/*_test.go"
  - "test/**"
---

# Test conventions

- **Ginkgo + Gomega only** ([0004](../../notes/0004-test-framework.md)): `DescribeTable` for
  tables, no `[]struct{}` + `t.Run`. `test/acceptance/` is not a suite: it is the program the
  release acceptance runs with, which acts and collects and never asserts
  ([0015](../../notes/0015-acceptance-tooling-acts-the-agent-judges.md)).
- **Cases before bodies.** The `It` and `Entry` strings are natural-language test cases,
  one per behaviour. Write them as `PIt` / `PEntry` with empty bodies (plus the stubs needed
  to compile), have `spec-reviewer` check them, show them with `task test-outline` in the
  session's report and go on with the bodies at once: the maintainer reads the outline
  there and stops the session if a case is wrong. The outline holds the PR's e2e cases too,
  as empty `PIt` specs checked with the rest; their bodies come last (see e2e below). Follow
  the [`test-outline`](../skills/test-outline/SKILL.md) skill.
- **Tests before implementation, one pair of commits per component.** First a
  `test(<scope>): ...` commit with the full bodies against the stubs: the specs fail there,
  `task lint-fix` passes. Then the `feat(<scope>): ...` commit that turns exactly those specs
  green, and only then the next component. A `feat` commit touches no `*_test.go` file and
  no test helper package: `git log --stat` shows every test file added by a `test` commit
  and left alone by the `feat` commits. A bug fix is no exception, whatever finds it: its
  regression spec comes first, in a `test(<scope>)` commit where it fails (with a stub when
  it needs a symbol only the fix defines), then the `fix(<scope>)` commit turns it green.
  `task check-commits` runs the specs of each `test` commit there and fails when they all
  pass: a `test` commit is red. The one exception is the e2e specs (below).
- **A `fix` commit names the pull request that introduced the bug**, in a last body line
  `Introduced-by: #<PR>`, so the release notes tell a fix of a released version from a fix
  of a feature never released. Find it with `git log`, `git blame` and `gh`; when you cannot,
  ask the user; if they do not know either, write `Introduced-by: unknown` and tell them.
  `task check-commits` and the Conform workflow reject a `fix` commit without it.
- **A wrong test is reported, never edited to pass.** A spec that contradicts `docs/v3/` or
  cannot be written as stated goes to the maintainer with the section it relies on. It
  changes only with the maintainer's approval, in its own `test` commit.
- **Review fixes need no approval.** Fixes from a review and their new regression specs are
  decided by the session; a spec reading or a behaviour choice is sent to the coordinator
  and listed in the pull request description. A rewritten or relaxed test goes to the
  maintainer only when the change opens a gap with `docs/v3/`.
- **A regression spec goes where the bug lives.** When a review (`reviewer`, human,
  CodeRabbit) finds a bug the outline missed:
  - in code a commit of the branch adds, the fix is a `fixup!` of that `feat` commit and its
    regression spec a `fixup!` of the `test` commit of the same component. After the
    autosquash the history still reads `test` (red) then `feat` (green). The `test` commit
    must still build alone: when the spec needs a symbol only the `feat` defines, the `test`
    fixup adds a stub and the `feat` fixup replaces it;
  - in code no commit of the branch owns (already on `main`), a regression pair on top of
    the branch: `test(<scope>): add regression specs for <what>` (red, with a stub when the
    spec needs a symbol only the fix defines), with a 2 or 3 line body naming the review that
    found the bug, then the `fix(<scope>)` commit that turns it green, with its
    `Introduced-by:` line. The PR description names it in one line.
- **A late batch of review fixes goes at the end of the branch.** When the fixes of a review
  round touch code that several later commits rewrite, retargeting each as a `fixup!` turns
  the autosquash into a cascade of conflicts: commit them instead as regression pairs on top
  of the branch, a `test(<scope>): add regression specs for <what>` commit (red) then the
  change it pins. This is an exception to the `fixup!` rule for review fixes of the
  [`pull-request`](../skills/pull-request/SKILL.md) skill, like the regression pair of a bug
  in code no commit of the branch owns.
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
- **envtest is not a cluster.** Its client is a cluster admin, so RBAC never refuses it, and
  the in-memory registries
  answer on loopback, where go-containerregistry speaks plain HTTP whatever `insecure` says.
  A behaviour that depends on credentials needs a fixture that requires them
  (`registrytest.WithBasicAuth`); one that depends on RBAC or on HTTPS needs an e2e spec.
  Every RBAC rule a change grants gets its entry in the e2e RBAC table of
  `test/e2e/e2e_test.go`, one verb standing for the verbs granted together.
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

- **A flaky spec is made deterministic, not rerun.** Find what it races with (a counter read
  before the request is counted, a timer) and make the spec wait for it or control it. Never
  rerun it in a loop to measure how flaky it is.
- **e2e** (`test/e2e/`, `task test-e2e`) runs on an isolated Kind cluster: the task
  creates `KIND_CLUSTER` when it is missing and deletes it afterwards only in that case. Never
  run it against a real cluster. Follow the
  [`e2e-spec`](../skills/e2e-spec/SKILL.md) skill.
- **e2e specs come last in a PR** ([0012](../../notes/0012-e2e-specs-last-in-a-pr.md)): their
  cases are in the outline from the start, as empty `PIt` specs; once the PR is otherwise
  ready (automated review addressed, unit and envtest specs green, the feature believed to
  work), a final `test` commit writes their bodies. If they fail, the corrections
  are `fixup!` commits, autosquashed once like review fixes. This is the one exception to "a
  `test` commit before each `feat` commit", which still holds for unit and envtest specs: a
  Kind run takes minutes, so iterating on e2e while the PR moves wastes it.

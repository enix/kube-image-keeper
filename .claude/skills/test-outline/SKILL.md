---
name: test-outline
description: Use before writing, adding or reworking Ginkgo specs in kuik; drafts the It/Entry strings as pending specs, has spec-reviewer check them and shows them with task test-outline before any body is written.
argument-hint: "[path...]"
allowed-tools: Bash(task test-outline *) Bash(go test *) Read Grep Glob Edit Write
---

# Test outline first

The test cases are written as a plain-English tree before any body. Follow the
steps in order, every time a task adds or changes specs. Target: `$ARGUMENTS` (the packages
to outline; empty means the whole repository).

## 1. Read the behaviour

- Read what defines the behaviour: the section of `docs/v3/spec.md` or `docs/v3/status.md`,
  or the walkthrough under `docs/v3/`.
- Read the package's `*_test.go` to reuse its `Describe` structure and helpers.
- Never invent behaviour. When the spec is silent or ambiguous, decide: what the kubelet
  does first, then the most literal reading of `docs/v3/`. Send each such decision to the
  coordinator and list it in the pull request description, one line each.

Suites live next to the code: one `suite_test.go` per package (envtest bootstrap, loads the
CRDs from `config/crd/bases/`) and `*_test.go` files beside the sources. Ginkgo and Gomega
only.

## 2. Write the tree with pending specs

`Describe` / `Context` name the object and the situation. Each case is a `PIt`, or a
`PEntry` inside a `DescribeTable`, with an empty body.

- One behaviour per string, present tense, the observable outcome.
- No implementation detail: `"sets Ready to False when the destination registry is
  unreachable"`, not `"calls probe()"`. A case that would break if the code were reorganised
  without changing what the user sees tests an implementation detail: drop it.
- A test exercises a behaviour, not a value (see [`tests.md`](../../rules/tests.md)): no spec
  that reads a literal back. In envtest and e2e, one spec per rule rather than per verb, kind
  or field; in a unit suite, one `Entry` per distinct input of a mapping or a parser.
- Add the stubs the outline needs to compile (a type, a function returning its zero value),
  nothing more.

```go
var _ = Describe("ImageMirror", func() {
    Context("when the destination registry is unreachable", func() {
        PIt("sets Ready to False with reason Unreachable", func() {})
        PIt("retries without blocking the other images", func() {})
    })
    DescribeTable("destination path", func(path string) {},
        PEntry("accepts a path with a trailing slash", "registry.tld/mirror/"),
        PEntry("rejects a path carrying a tag", "registry.tld/mirror:latest"),
    )
})
```

## 3. Show the outline and go on

```sh
task test-outline -- <path>                   # the whole tree of the package
task test-outline DIFF=origin/main -- <path>  # only the cases added and removed since main
```

The example above prints:

```text
# internal/controller/kuik/imagemirror_controller_test.go
ImageMirror
  when the destination registry is unreachable
    - sets Ready to False with reason Unreachable [pending]
    - retries without blocking the other images [pending]
  destination path
    - accepts a path with a trailing slash [pending]
    - rejects a path carrying a tag [pending]
```

and with `DIFF=origin/main`, one line per case prefixed by `+` (added) or `-` (removed):

```text
+ ImageMirror / when the destination registry is unreachable / sets Ready to False with reason Unreachable [pending]
```

Before showing it, hand the outline and the spec section it covers to the `spec-reviewer`
sub-agent. Fix the cases it reports as missing or not in the spec; where the spec is
unclear, decide as in step 1.

Paste the output in the session's report, each top-level `Describe` group preceded by one line naming
the spec section it covers, with its GitHub link:

```text
Covers docs/v3/status.md, "Conditions and their reasons": https://github.com/enix/kube-image-keeper/blob/main/docs/v3/status.md#conditions-and-their-reasons
```

A pull request stays issue-sized, about 30 specs at most. When the outline holds more, as an
epic (a feature too large for one review) does, cut it into such pull requests,
each mergeable on its own (`main` stays coherent, nothing is half-wired), each with its
docs and, last, its e2e specs. The issues of an epic are grouped under the GitHub
milestones of the release: its beta, then its GA.

Then go on to step 5 at once: the outline and the cut are reported, not waited on. The
user reads them in the report and stops the session if a case or the cut must change.

The outline may be committed as a WIP `test(<scope>): outline ...` commit. It is squashed
into the first test commit of step 5.

## 4. Iterate on the strings

When the user asks for changes, apply them to the strings, and to the bodies already
written, then show the outline again and go on.

## 5. Fill the bodies, before any implementation

Keep a todo list with Claude Code's task tools for the pull request,
one task per step of the plan (each outline group, each `test`/`feat` pair, the docs, the
e2e specs, each step of the [`pull-request`](../pull-request/SKILL.md) skill), updated as
it goes. A list that grows too long is the sign the pull request must be cut further.

The session that owns the pull request hands the checked outline to one `implementer`
agent, which writes the bodies and the code; a `reviewer` agent reviews each `feat` commit
as it lands ([0014](../../../notes/0014-implementer-and-reviewer-agents.md)).

Cut the outline into components by the code each `feat` commit changes, not by test layer:
each component names the files its `feat` commit modifies. A spec that needs no code of its
own (an earlier component already makes it pass) goes in the `test` commit of the component
whose `feat` turns it green, never in a `test` commit of its own, which would be green from
the start. e2e specs are the exception: they come in one last `test` commit, once the pull
request is ready ([`e2e-spec`](../e2e-spec/SKILL.md)).

Work one component at a time, in 2 commits:

1. `test(<scope>): ...`: turn `PIt` into `It` (`PEntry` into `Entry`) and write the full
   bodies against the stubs. The specs fail at this commit, as expected; `task lint-fix`
   passes.
2. `feat(<scope>): ...`: the implementation that turns exactly those specs green. It
   touches no `*_test.go` file and no test helper package.

Then the next component. `git log --stat` must show every test file added by a `test`
commit and left alone by the `feat` commits: the reviewer checks it.

- Never rename a string while filling. A rename goes back to step 3.
- Never edit a test to make it pass. A test that contradicts `docs/v3/` or cannot be
  written as stated is reported to the user with the section it relies on. It changes only
  with the user's approval, in its own `test` commit.
- When a review (`reviewer`, human, CodeRabbit) finds a bug the outline missed in code a
  commit of this branch adds, its regression spec is a `fixup!` of the `test` commit of
  that component, and the fix a `fixup!` of its `feat` commit: the history keeps `test`
  (red) then `feat` (green). Neither waits for approval
  ([`tests.md`](../../rules/tests.md)).
- Exception: a bug in code no commit of the branch owns. The fix comes first and its
  regression spec in its own `test` commit after it. Subject
  `test(<scope>): add regression specs for <what>`; a 2 or 3 line body names the review that
  found the bug, the component or commit subject that carries the fix (never a sha: the
  branch is rebased before merge, only hashes on `main` are stable), and says the specs pin
  the fix. Whoever reads `git log` then knows why this test commit follows its
  implementation.
- Run the case alone, then the package:

  ```sh
  go test ./<pkg> -v -ginkgo.focus '<It text>'
  go test ./<pkg>
  ```

- Run `task manifests` first when a type changed: envtest loads the generated CRDs.
- A plain `go test` on an envtest suite finds the binaries in the main checkout's
  `bin/k8s/`, shared by every worktree, only after `task setup-envtest` (or any
  `task test`) has run once.

## 6. Before handing back

- No `PIt` / `PEntry` left unless the user agreed to keep it.
- `task lint-fix`.
- `task test-outline DIFF=origin/main` once more, and put its output in the report.

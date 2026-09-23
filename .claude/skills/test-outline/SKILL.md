---
name: test-outline
description: Use before writing, adding or reworking Ginkgo specs in kuik; drafts the It/Entry strings as pending specs and shows them with task test-outline for review before any body.
argument-hint: "[path...]"
allowed-tools: Bash(task test-outline *) Bash(go test *) Read Grep Glob Edit Write
---

# Test outline first

The test cases are agreed as a plain-English tree before any body is written. Follow the
steps in order, every time a task adds or changes specs. Target: `$ARGUMENTS` (the packages
to outline; empty means the whole repository).

## 1. Read the behaviour

- Read what defines the behaviour: the section of `docs/v3/spec.md` or `docs/v3/status.md`,
  or the walkthrough under `docs/v3/`.
- Read the package's `*_test.go` to reuse its `Describe` structure and helpers.
- Never invent behaviour. When the spec is silent, stop and ask.

Suites live next to the code: one `suite_test.go` per package (envtest bootstrap, loads the
CRDs from `config/crd/bases/`) and `*_test.go` files beside the sources. Ginkgo and Gomega
only.

## 2. Write the tree with pending specs

`Describe` / `Context` name the object and the situation. Each case is a `PIt`, or a
`PEntry` inside a `DescribeTable`, with an empty body.

- One behaviour per string, present tense, the observable outcome.
- No implementation detail: `"sets Ready to False when the destination registry is
  unreachable"`, not `"calls probe()"`.
- A test exercises a behaviour, not a value (see [`tests.md`](../../rules/tests.md)): no spec
  that reads a literal back, one spec per rule rather than per verb, kind or field.

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

## 3. Show the outline and stop

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

Paste the output in the reply and stop. The user reviews the cases before any body exists.

## 4. Iterate on the strings

Apply the requested changes to the strings only, then show the outline again. Repeat until
the user agrees.

## 5. Fill the bodies

- Turn `PIt` into `It` (`PEntry` into `Entry`) one case at a time.
- Never rename a string while filling. A rename goes back to step 3.
- Run the case alone, then the package:

  ```sh
  go test ./<pkg> -v -ginkgo.focus '<It text>'
  go test ./<pkg>
  ```

- Run `task manifests` first when a type changed: envtest loads the generated CRDs.
- A plain `go test` on an envtest suite finds the binaries in `bin/k8s/` only after
  `task setup-envtest` (or any `task test`) has run once.

## 6. Before handing back

- No `PIt` / `PEntry` left unless the user agreed to keep it.
- `task lint-fix`.
- `task test-outline DIFF=origin/main` once more, and put its output in the report.

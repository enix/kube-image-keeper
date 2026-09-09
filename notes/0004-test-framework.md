# 0004 — Ginkgo everywhere as the single test framework

**Date:** 2026-09-09 · **Status:** decided

## Decision

**Ginkgo/Gomega is the only test framework in the repository**, pure unit tests included.
One runner, one assertion vocabulary, no demarcation rule to apply per file.

- **Gomega is the only assertion grammar.** Where a plain `func TestX(t *testing.T)`
  remains, it asserts through `g := NewWithT(t)`; the target shape for new tests is a
  Ginkgo spec.
- **Table-driven tests are written as `DescribeTable` / `Entry`**, not `[]struct{}` +
  `t.Run`. This is already the shape used in
  [`internal/controller/kuik/mirror_reconciler_test.go`](../internal/controller/kuik/mirror_reconciler_test.go).
- **Fast feedback comes from Ginkgo labels**, not from `testing.Short()`:
  `Label("envtest")` / `Label("e2e")` plus `-ginkgo.label-filter` (details below).

The reason is **traceability between a reviewed test case and the test that runs it**, not
the technical merits of Ginkgo. Natural-language test cases are authored in the text of the
issue (phase 3 of [0002](./0002-development-pipeline.md)); the generation step transcribes
them into the `Describe` / `Context` / `It` / `Entry` strings. The English a human reviewed
therefore lands in the code, greppable, adjacent to its assertion. Ginkgo is the only Go
framework that carries that narrative layer natively.

Accuracy note on the existing tree: of the 19 `*_test.go` files, 8 are Ginkgo and 11 are
plain `testing` (4 of those already assert through Gomega, 7 through `t.Errorf`). Those 11
files stay as they are; this rule governs new code.

## Why traceability decides it

The pipeline is: spec → NL cases in the issue text → human review of the cases → generated
Go tests → implementation. The review gate sits on the natural language, upstream of the
code. That only pays off if the reviewed sentence is still identifiable once the Go exists.

`Describe` / `Context` / `It` maps one-to-one onto the structure of a case: context, action,
assertion. A `[]struct{ name string; ... }` entry technically also carries a name, but it
carries it flat — a case's "given" and "when" either collapse into a single string or get
split between a struct field and a comment. That, and not terseness, is the whole argument.

Two consequences worth stating:

- The case text becomes **greppable**, and `--focus` / `--focus-file` can run exactly the
  case under discussion in a review.
- Semantic drift — a test asserting something other than what its case says — is not
  eliminated (only a single-artefact format like Gherkin does that by construction), but it
  is substantially reduced: the claim and the assertion sit on adjacent lines, and a
  reviewer or a review agent reads them together. The residual mechanism for catching drift
  belongs to [0002](./0002-development-pipeline.md), not to this note.

## Why one framework rather than a split

Two runners is a permanent tax on every contributor and every agent: two idioms to learn,
two failure-output formats to read (which matters for an agent parsing CI logs), two
possible answers to "how do I write this test", and a boundary to adjudicate on every new
file.

The current tree shows the cost of not deciding. Three styles coexist with no written rule
— Ginkgo/Gomega (8 files), `testing` + Gomega via `NewWithT` (4), `testing` + `t.Errorf`
(7) — and the boundary is not even at package granularity:
`internal/controller/kuik/registryconfig_test.go` is plain `testing` inside the package
whose Ginkgo suite bootstraps envtest, and
`internal/webhook/core/v1/pod_webhook_test.go` is 753 lines of Ginkgo testing pure
in-memory logic.

## Fast feedback: Ginkgo labels

Today [`make test-short`](../Makefile) is `go test -short ./...`, and both envtest suites
opt out with `if testing.Short() { t.Skip(...) }` inside the `func TestX` that calls
`RunSpecs`. The granularity is therefore the whole suite. Under "Ginkgo everywhere", a unit
spec sharing a package with an envtest suite would be skipped along with it — the fast
target would lose coverage exactly as the suite grows.

**Decision: label the specs that need infrastructure and filter on labels.** Feasibility was
checked against the existing targets:

- **No new tooling.** Ginkgo's `ginkgo.`-prefixed flags are parsed by a plain `go test`
  binary; the repo already relies on this — `make test-e2e` passes `-ginkgo.v`. The `ginkgo`
  CLI is not needed and stays out of the Makefile's tool list.
- **Suite-level label for homogeneous suites**: `RunSpecs(t, "Controller Suite", Label("envtest"))`.
  Every spec in `internal/controller/kuik` needs a real API server, so the label belongs to
  the suite.
- **Spec-level labels where a package genuinely mixes.** `internal/webhook/core/v1` is that
  case: 34 `It`s, the overwhelming majority calling `defaultPod` on in-memory objects;
  `k8sClient` appears three times and only to populate the defaulter's `Client` field, and
  no test in the package creates a Pod through the API server.
- **The non-obvious consequence.** Filtering specs out only saves time if the envtest
  bootstrap is not paid anyway. For a label-homogeneous suite, filtering all its specs out
  leaves Ginkgo nothing to run and `BeforeSuite` is not entered. For a suite that mixes
  labelled and unlabelled specs — the webhook one — `BeforeSuite` still runs for the
  unlabelled specs, so the envtest bootstrap has to move out of an unconditional
  `BeforeSuite` into a lazily-initialised helper called from the labelled specs (or the pure
  specs move to their own suite). Whoever labels the webhook suite owns that change.
- **Target wiring, when the first labels land**: `test-short` becomes
  `go test ./... -ginkgo.label-filter='!envtest && !e2e'`. The existing `testing.Short()`
  guards can stay during the transition; `-short` and a label filter are not mutually
  exclusive. [`make test`](../Makefile) — the one CI runs, see
  [`.github/workflows/test.yaml`](../.github/workflows/test.yaml) — is unchanged: it runs
  everything with envtest assets and excludes `./test/e2e` by package path, which labels do
  not affect. `make test-e2e` is unchanged. `.lefthook.yaml`'s pre-push job runs
  `make test-short`, so it inherits the improvement for free.
- **Not rewired in this commit.** With no labelled specs in the tree,
  `-ginkgo.label-filter='!envtest'` selects everything, and `test-short` would start
  envtest. The Makefile change belongs to the commit that introduces the first labels.

## Alternatives rejected

### Gherkin / godog

1. **Mismatch with the work ahead.** Much of the suite is table-driven over pure functions,
   where `Scenario Outline` is more verbose and less maintainable than a Ginkgo table.
   Milestone 2 of [0002](./0002-development-pipeline.md) — the `imagePrefix` segment trie and
   the `secretRef`/`provider` auth model — is table territory.
2. **The hidden cost of step definitions.** For an operator, the "given" is "these CRs
   applied, this pod created, these registries reachable, this secret in that namespace".
   The step vocabulary explodes, and both exits are bad: hyper-specific non-reusable steps
   (the worst of both worlds), or designing a DSL — spending milestones 2-3 building a test
   language instead of the trie and the auth model. A classic BDD failure mode on
   infrastructure code.
3. **One runner, one vocabulary.** godog is a separate runner with its own binary, its own
   coverage plumbing and its own idiom. This argument is available to us without
   self-contradiction precisely because we no longer institute two runners ourselves.
   What is *not* an argument, and was wrongly believed to be one: "envtest, kubebuilder and
   controller-runtime require Ginkgo". They do not — `envtest.Environment` is an ordinary Go
   library that starts fine from a `TestMain`; our `suite_test.go` files merely let
   `RunSpecs` stand in for it. The real version is weaker and still sufficient: Ginkgo is the
   kubebuilder scaffolding convention, the team and the agents already read it, and
   `ginkgolinter` is already enabled in [`.golangci.yml`](../.golangci.yml).
4. **Gherkin pays when non-technical authors write the scenarios** and those scenarios must
   stay executable indefinitely. Here the reviewer is a kuik engineer, so the premium buys
   much less.

Note what the chosen decision buys, which is exactly what Gherkin was for: with the case
text living in the spec strings, the reviewed prose and the executable assertion are in the
same file, on adjacent lines. Not literally one artefact, but close enough that the drift
Gherkin prevents by construction becomes visible to a reader and to `grep`.

### Gherkin for the e2e suite only

The one place it would come close to paying: the spec's walkthroughs 01-03 are already
Given/When/Then narratives against a real cluster, few in number, with genuine step reuse
(create the cluster, apply the CR, create the pod, assert the rewritten image). Rejected
anyway — two frameworks in one repository is a permanent tax on every contributor and every
agent, which is the same reason the hybrid below is rejected. Worth revisiting only if the
e2e suite grows a lot.

### The hybrid: `testing` for pure logic, Ginkgo for envtest and e2e

This was the working assumption until the decision to author NL cases in the issue text, and
it is defensible on its own terms: a Go table is terser than `DescribeTable` for a pure
function, and the criterion could have been made mechanical — *Ginkgo if and only if the test
needs a real API server*. It loses on the only axis that decides this note: half the new
tests would not carry their case text in a nested narrative form, and the criterion is a
judgement call an agent must re-make on every new test file.

One correction for the record, since it was part of the case for the hybrid: the criterion
usually reached for — "Ginkgo where `Eventually` and envtest justify it" — does not describe
this repository. There are 11 `Eventually` calls in 4,199 lines of test, no `Consistently`
at all, and `internal/controller/kuik/suite_test.go` starts **no manager** — the specs call
`Reconcile()` directly against an envtest API server. What justifies envtest here has always
been a real API server (CRD/CEL validation, the status subresource, conflicts, owner
references), never asynchrony.

## Consequences landed with this note

- [`.coderabbit.yaml`](../.coderabbit.yaml), the `**/*.go` path instruction: "use
  Ginkgo/Gomega with envtest" replaced by the actual rule. That string is injected into every
  Go review, so a stale version actively pushes contributors the wrong way.
- [`AGENTS.md`](../AGENTS.md), the `internal/testsetup/` bullet: states the rule. It also
  records that the package is **not** blank-imported anywhere today. It is left that way
  rather than wired in as a no-op: no test currently asserts on a `*regexp.Regexp`, and an
  import that changes nothing observable is noise. It becomes load-bearing when milestone 2's
  trie and filter tests start comparing compiled regexps — blank-import it then, in the suite
  that needs it.
- `AGENTS.md`, the Git Hooks section, corrected while in the file (unrelated drift found in
  passing): pre-push runs `make test-short`, not `make test`, and `markdownlint-cli2` is
  skipped below Node.js 22, not 20 — per [`.lefthook.yaml`](../.lefthook.yaml).
- Spec labels and the `test-short` rewiring land with the first labelled suite, not here.
- The residual semantic-drift question is [0002](./0002-development-pipeline.md)'s.

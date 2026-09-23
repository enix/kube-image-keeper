# AGENTS.md

Guidance for AI coding agents working on this repository.

## What this branch is

kube-image-keeper (kuik) v3: a Kubernetes operator for container image routing,
mirroring and monitoring, rebuilt from a clean tree. The branch holds:

- [`docs/v3/`](./docs/v3/) — the specification, source of truth for behaviour. Read
  `spec.md` first, then `status.md`, the walkthroughs and `open-questions.md`.
- [`notes/`](./notes/) — the engineering journal: decisions, analyses, process. Start
  with [`notes/README.md`](./notes/README.md); its index states each decision in one
  line. Open a note only when it matters for the task, never the whole folder.
- The kubebuilder scaffold (multi-group layout, see [Code](#code)) with empty
  reconcilers and an empty Pod webhook, plus the chart, CI, hooks and release tooling.
- [`CONTRIBUTING.md`](./CONTRIBUTING.md) — hooks, commit conventions and scopes,
  release process. Follow it; it is not repeated here.

Code, chart and tooling are re-added milestone by milestone
([0002](./notes/0002-development-pipeline.md)). v2 lives on the `2.3.x` maintenance
branch: read it with `git show 2.3.x:<path>` and lift on purpose, never copy by reflex.
[0001](./notes/0001-v2-reuse-analysis.md) lists what is worth lifting: `internal/registry`
and its availability statuses, `internal/parallel.FirstSuccessful`, `SecretOwnerReconciler`,
the config merge, `internal/info`, the envtest suite bootstrap.

## Code

Layout, group `kuik`, version `v1alpha1`:

```text
cmd/                          Manager entry: one subcommand per process (webhook,
                              reconciler, secret-syncer), gating what it registers
api/kuik/v1alpha1/*_types.go  CRD schemas and kubebuilder markers
internal/controller/kuik/*    Reconcilers, one per kind
internal/webhook/core/v1/*    Pod mutating webhook (image routing)
config/                       controller-gen output (CRDs, rbac/role.yaml, webhook), read by envtest
helm/kube-image-keeper/       The Helm chart, the only deployment path (crds/ and files/ generated)
test/e2e/                     End-to-end suite, runs on a Kind cluster
hack/                         Developer tools run with go run (the test outline, the values check)
website/                      The docs site (Astro Starlight), see .claude/rules/docs.md
PROJECT                       Kubebuilder metadata
```

**Generated, never edit by hand**: the paths [`.gitattributes`](./.gitattributes) marks
`generated-by=<task>`, with the task that rebuilds them. Edit the markers or the `# --`
comments of `values.yaml` in the sources and regenerate.

**Keep the scaffold intact**: never delete `// +kubebuilder:scaffold:*` comments, the
CLI injects code there. Do not move files: the CLI expects this layout. Scaffold new
kinds and webhooks with `kubebuilder create api` / `kubebuilder create webhook`, never
by hand.

**Commands** are [Task](https://taskfile.dev) tasks in `Taskfile.yaml` (`task --list`), see
[0006](./notes/0006-taskfile.md). The `Makefile` is a shim that forwards `make <target>`
to `task <target>` for the kubebuilder CLI and the e2e suite; do not add targets to it.
After a change, before committing:

```sh
task manifests generate   # after editing *_types.go or any kubebuilder marker
task lint-fix             # after editing *.go
task test                 # unit and envtest suites
# one spec only: the suites are Ginkgo, so filter on the It text, not on -run
go test ./internal/controller/kuik -v -ginkgo.focus 'text of the It'
```

CI fails on any drift in generated files, formatting or `go mod tidy`.

**Deploying** goes through the Helm chart only ([0007](./notes/0007-helm-only.md)):
`task deploy IMG=...`, or `task kind-deploy` on a Kind cluster. `config/` holds no
deployment overlay; do not add kustomize bases or patches. The webhook serving
certificate comes from cert-manager (`templates/webhook.yaml`: Certificate, Issuer and
`cert-manager.io/inject-ca-from` on the MutatingWebhookConfiguration); a cluster without
cert-manager cannot run kuik.

**e2e tests** (`task test-e2e`) need an isolated Kind cluster. Never run them against a
real cluster.

## Conventions

The conventions of each area of the tree live in [`.claude/rules/`](./.claude/rules/) and
load when a matching file is opened: [`go.md`](./.claude/rules/go.md) (reconcilers, API
types, image references, logging, RBAC markers), [`tests.md`](./.claude/rules/tests.md)
(Ginkgo, cases before bodies, a test exercises a behaviour, envtest, e2e),
[`docs.md`](./.claude/rules/docs.md) (Markdown conventions, how the site is built) and
[`helm.md`](./.claude/rules/helm.md) (values per process, RBAC bindings, generated files,
cert-manager). Read the one that matters before editing.

**Every behaviour change ships with its tests and its documentation** in the same PR: the
page under `docs/`, the `# --` comments of `values.yaml` for chart values, this file or the
rule when the layout or the rules change.

## Docs

User documentation lives under [`docs/`](./docs/) and is published from `main` at
[kuik.enix.io](https://kuik.enix.io): a broken page ships as soon as it is merged. The
markdown is the single source of truth; read it alongside the code. [`docs/v3/`](./docs/v3/)
(the design documents) renders on GitHub only, `notes/` is never published. Conventions and
build are in [`.claude/rules/docs.md`](./.claude/rules/docs.md).

## Claude Code configuration

[`.claude/`](./.claude/) is committed and shared:

- `settings.json`: no AI attribution on commits and PRs
  ([CONTRIBUTING](./CONTRIBUTING.md#use-of-ai-tools)), an allowlist of the read-only and
  build commands, `ask` rules that back the outbound hook up, `deny` rules on the
  project's secret files (`.env`, keys; `Read` only, a Bash `cat` is not covered), and the
  hooks below.
- `hooks/guard-generated-files.sh` refuses Edit and Write on the generated files of
  [Code](#code) and names the task to run instead. A Bash command that writes a file
  (`sed -i`, `>`) is not covered.
- `hooks/confirm-outbound-actions.sh` asks the user before any command that leaves the
  working copy (`git push`, `gh pr|issue` writes, `docker push`, releases, `task deploy`
  or `run`, the e2e tasks, `helm install`, `kubectl apply|label`...), in every permission
  mode, even when several tasks share one call (`task build deploy`). Not forbidden: the
  user decides, the agent never does it on its own
  ([0003](./notes/0003-agent-orchestration.md)).
- `rules/`: the path-scoped conventions above.
- `skills/`: [`test-outline`](./.claude/skills/test-outline/SKILL.md) (the spec-first Ginkgo
  workflow, picked up whenever specs are written) and
  [`decision-note`](./.claude/skills/decision-note/SKILL.md) (`/decision-note`, writes a note
  under `notes/` when the filter holds).
- `agents/`: [`spec-reviewer`](./.claude/agents/spec-reviewer.md) (read-only conformance
  review of a change against `docs/v3/`, keeps the spec out of the main context).

The hooks need `jq` and refuse the call without it, so the guards always hold: install
it before developing. `worktrees/`, `artifacts/` and
`settings.local.json` are gitignored.

## Writing

Everything written here is read under time pressure. Be concise and go straight to the
point, in commit messages, PR and issue descriptions, review comments, notes,
documentation, specification and answers to questions alike.

- **Lead with the answer**: the decision, the conclusion, what changed. Context comes
  after, and only if it changes what the reader does.
- **One idea per sentence.** Short and plain beats clause chains and jargon.
- **Cut what adds nothing**: restating the title, narrating the diff, announcing what you
  are about to say, closing summaries, filler adjectives.
- **Never pad to look thorough.** Length follows content, not effort.
- Concise is not incomplete: keep every fact the reader needs to act, drop the rest.

**Commit messages**: the subject is the description, and the diff shows what changed.
Most commits need no body. Add one only for what the diff does not show (the reason, a
rejected alternative, a non-obvious consequence), in a few lines; never list the changes.

**PR descriptions** are written for the reviewer, the way a colleague would: a short
paragraph on what the PR does and why, then only what helps the review (where to start, a
choice to weigh in on, what is deliberately left out). Link the spec or the note instead
of paraphrasing it. No headings, tables or bullet lists that restate the commits or the
diff.

## Decision log

The "why" of a change lives in its commit body by default. Write a note under `notes/`
only when the decision passes the filter in [`notes/README.md`](./notes/README.md): it
constrains later work, rejects an alternative that will come back, or has no single
commit to live in. Notes record deliberation that actually happened; do not invent
alternatives to fill the template. Decision notes stay under 25 lines. An `active` note is
amended in place, marked and dated; a `decided` note is frozen: supersede it with a new
note, never rewrite it. A commit that implements a decision references its note.

## Git hooks

[lefthook](./.lefthook.yaml) runs on pre-commit `task manifests generate` (when API,
controller or webhook sources are staged), `task lint-fix` and `task lint-markdown-fix`
(skipped without Node.js ≥ 22); on pre-push `task test-short`; on commit-msg `task conform`.
Conventional commits with the scopes of `.conform.yaml`, see
[`CONTRIBUTING.md`](./CONTRIBUTING.md).

## References

- [Kubebuilder book](https://book.kubebuilder.io), in particular
  [markers](https://book.kubebuilder.io/reference/markers.html) and
  [good practices](https://book.kubebuilder.io/reference/good-practices.html).
- [controller-runtime FAQ](https://github.com/kubernetes-sigs/controller-runtime/blob/main/FAQ.md).

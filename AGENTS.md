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
  reconcilers and an empty Pod webhook, plus the CI, hooks and release tooling.
- [`CONTRIBUTING.md`](./CONTRIBUTING.md) — hooks, commit conventions and scopes,
  release process. Follow it; it is not repeated here.

Code, chart and tooling are re-added milestone by milestone
([0002](./notes/0002-development-pipeline.md)). v2 code is retrieved from history with
`git show main:<path>` and lifted on purpose, never copied by reflex
([0001](./notes/0001-v2-reuse-analysis.md)).

## Code

Layout, group `kuik`, version `v1alpha1`:

```text
cmd/main.go                   Manager entry: registers controllers and webhooks
api/kuik/v1alpha1/*_types.go  CRD schemas and kubebuilder markers
internal/controller/kuik/*    Reconcilers, one per kind
internal/webhook/core/v1/*    Pod mutating webhook (image routing)
config/                       controller-gen output (CRDs, RBAC, webhook), read by envtest
helm/kube-image-keeper/       The Helm chart, the only deployment path (crds/ and files/ generated)
test/e2e/                     End-to-end suite, runs on a Kind cluster
PROJECT                       Kubebuilder metadata
```

**Generated, never edit by hand**: `**/zz_generated.*.go` (`task generate`),
`config/crd/bases/*.yaml`, `config/rbac/role.yaml`, `config/webhook/manifests.yaml`,
`helm/kube-image-keeper/crds/*.yaml`, `helm/kube-image-keeper/files/*.yaml`
(`task manifests`), `PROJECT` (kubebuilder CLI). Edit the markers in the Go sources and
regenerate.

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
```

The pre-commit hook runs the first two on staged files (`.lefthook.yaml`); CI fails on
any drift in generated files.

**Deploying** goes through the Helm chart only ([0007](./notes/0007-helm-only.md)):
`task deploy IMG=...` or `task kind-deploy` on a Kind cluster. `config/` holds no
deployment overlay; do not add kustomize bases or patches.

**e2e tests** (`task test-e2e`) need an isolated Kind cluster. Never run them against a
real cluster.

## Conventions

- **Tests**: Ginkgo + Gomega only ([0004](./notes/0004-test-framework.md)). The `It`
  and `Entry` strings are the reviewed test cases from the issue, verbatim.
- **Reconcilers**: idempotent; re-fetch the object before updating it; report state
  with `metav1.Condition`; watch secondary resources with `Owns()` / `Watches()` rather
  than polling with `RequeueAfter`; use finalizers only for external resources.
- **API types**: follow the
  [Kubernetes API conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md);
  `metav1.Time` for dates, validation and default markers on fields.
- **Logging**: structured, `log := logf.FromContext(ctx)`. Messages follow the
  [Kubernetes style](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-instrumentation/logging.md#message-style-guidelines):
  capitalised, no trailing period, past tense, object type named
  (`"Created Deployment"`, not `"Created"`), balanced key-value pairs.
- **RBAC**: declared with `// +kubebuilder:rbac` markers on the reconciler, never in
  `config/rbac/role.yaml` directly.

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

## Decision log

The "why" of a change lives in its commit body by default. Write a note under `notes/`
only when the decision passes the filter in [`notes/README.md`](./notes/README.md): it
constrains later work, rejects an alternative that will come back, or has no single
commit to live in. Notes record deliberation that actually happened; do not invent
alternatives to fill the template. Decision notes stay under 25 lines and are
append-only: supersede with a new note, never rewrite. A commit that implements a
decision references its note.

## References

- [Kubebuilder book](https://book.kubebuilder.io), in particular
  [markers](https://book.kubebuilder.io/reference/markers.html) and
  [good practices](https://book.kubebuilder.io/reference/good-practices.html).
- [controller-runtime FAQ](https://github.com/kubernetes-sigs/controller-runtime/blob/main/FAQ.md).

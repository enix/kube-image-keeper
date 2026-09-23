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
hack/                         Developer tools run with go run (the test outline)
website/                      The docs site (Astro Starlight), see Docs below
PROJECT                       Kubebuilder metadata
```

**Generated, never edit by hand**: `**/zz_generated.*.go` (`task generate`),
`config/crd/bases/*.yaml`, `config/rbac/role.yaml`, `config/webhook/manifests.yaml`,
`helm/kube-image-keeper/crds/*.yaml`, `helm/kube-image-keeper/files/rbac.yaml`
(`task manifests`), `helm/kube-image-keeper/README.md` (`task generate-helm-docs`, from the
`# --` comments of `values.yaml`), `PROJECT` (kubebuilder CLI). Edit the markers or the
comments in the sources and regenerate.

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

- **Tests**: Ginkgo + Gomega only ([0004](./notes/0004-test-framework.md)). The `It`
  and `Entry` strings are natural-language test cases, one per behaviour, and they are
  reviewed before the bodies are written: write the tree first with pending specs (`PIt`),
  show it with `task test-outline -- <path>` or `task test-outline DIFF=origin/main` for
  the cases added and removed, then fill the bodies once the cases are agreed. Suites are
  `suite_test.go` files on envtest and load the CRDs from `config/crd/bases/`, so run
  `task manifests` before testing a type change.
- **Reconcilers**: idempotent; re-fetch the object before updating it; report state
  with `metav1.Condition`; watch secondary resources with `Owns()` / `Watches()` rather
  than polling with `RequeueAfter`; use finalizers only for external resources.
- **API types**: follow the
  [Kubernetes API conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md);
  `metav1.Time` for dates, validation and default markers on fields.
- **Image references**: canonicalize with `github.com/distribution/reference`
  (`ParseNormalizedNamed`, as v2 did) before any comparison; `nginx`, `docker.io/nginx`
  and `docker.io/library/nginx:latest` are the same image.
- **Logging**: structured, `log := logf.FromContext(ctx)`. Messages follow the
  [Kubernetes style](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-instrumentation/logging.md#message-style-guidelines):
  capitalised, no trailing period, past tense, object type named
  (`"Created Deployment"`, not `"Created"`), balanced key-value pairs.
- **RBAC** ([0009](./notes/0009-rbac-rules-from-markers-bindings-from-chart.md)): rules are
  `// +kubebuilder:rbac` markers on the code that exercises the permission, each with
  `roleName=` naming its process (`webhook`, `reconciler`, `secret-syncer`) or
  `secret-reader` (cluster-wide Secret read, bound by `secretAccess`). The rules are
  generated; the bindings are the chart's (`templates/rbac.yaml`). A marker without
  `roleName=` fails `helm template`. Delete the `admin/editor/viewer` roles
  `kubebuilder create api` scaffolds under `config/rbac/`.
- **Chart values** ([0008](./notes/0008-chart-values-per-process.md)): a pod setting goes
  at the root of `values.yaml` **and**, empty with a `# @default -- the root ...` line, in
  each of `webhook`, `reconciler` and `secretSyncer`; a setting one process alone has goes
  in its block only. `templates/deployments.yaml` resolves the fallback once at the top of
  its loop, never inline in the body. `task lint-values` checks the blocks, `task
  generate-helm-docs` rebuilds the README.
- **Every behaviour change ships with its tests and its documentation** in the same PR:
  the page under `docs/`, the `# --` comments of `values.yaml` for chart values, this
  file when the layout or the rules change.

## Docs

User documentation lives under [`docs/`](./docs/) and is published from `main` at
[kuik.enix.io](https://kuik.enix.io) by [`.github/workflows/website.yaml`](./.github/workflows/website.yaml):
a broken page ships as soon as it is merged. The markdown is the single source of truth;
read it alongside the code. Today: [`docs/crds.md`](./docs/crds.md) (CRD reference, kept in
step with `api/kuik/v1alpha1`), [`docs/configuration.md`](./docs/configuration.md),
[`docs/guides/development.md`](./docs/guides/development.md) (local workflow) and the
use cases. The v2 user docs are served from the `2.3.x` branch, not from here.

[`docs/v3/`](./docs/v3/) (the design documents) is listed in `UNPUBLISHED_DOCS` of
[`website/scripts/sync-docs.mjs`](./website/scripts/sync-docs.mjs) and renders on GitHub
only. `notes/` is never published.

### Markdown conventions

The same files render on GitHub and on the Astro Starlight site. Write for GitHub first;
the build adapts:

- The page title is a leading `# H1`, never a frontmatter `title:`; the build lifts it
  into the frontmatter Starlight needs and strips it from the body. Add a frontmatter
  `description:`: it is the SEO description and the text of the use-case cards.
- Links between pages are relative markdown links with the `.md` extension
  (`./crds.md#imagemirror`); the build rewrites them to site routes. Never write a site
  route like `/crds/`: it breaks on GitHub. markdownlint checks that targets and anchors
  exist.
- Callouts use GitHub alerts (`> [!NOTE]`, `> [!TIP]`, `> [!WARNING]`, `> [!IMPORTANT]`,
  `> [!CAUTION]`); the build converts them to Starlight asides. Never use Starlight's
  `:::note`, it renders as raw text on GitHub.
- A new file under `docs/use-cases/` is picked up by the use-cases index and the sidebar
  automatically.

### How the site is built

Starlight only reads `website/src/content/docs/`, so a `sync-docs` integration
([`website/astro.config.mjs`](./website/astro.config.mjs)) generates it (gitignored) before
content loads: it copies `docs/`, then [`website/src/content/overlay/`](./website/src/content/overlay/)
(website-only pages, copied last so they win), lifts the H1 titles and skips
`UNPUBLISHED_DOCS`. Two plugins bridge the syntaxes: `remark-github-admonitions-to-directives`
for alerts and `astro-rehype-relative-markdown-links` for links. Archived versions come from
[`website/versions.mjs`](./website/versions.mjs): each one is sourced with `git archive` from
its maintenance branch (`2.3.x`...), whose `docs/` tree holds its markdown and sidebar, with
`slug:` injected on the fly. The full workflow is in
[`website/README.md`](./website/README.md#documentation-versioning).

Local preview: `cd website && npm install && npm run dev` (Node.js 24). A watcher mirrors
`docs/` edits into the generated directory. Run one `astro dev` at a time; editing
`astro.config.mjs` or `sync-docs.mjs` restarts it.

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
alternatives to fill the template. Decision notes stay under 25 lines and are
append-only: supersede with a new note, never rewrite. A commit that implements a
decision references its note.

## Git hooks

[lefthook](./.lefthook.yaml) runs on pre-commit `task manifests generate` (when API,
controller or webhook sources are staged), `task lint-fix` and markdownlint (skipped
without Node.js ≥ 22); on pre-push `task test-short`; on commit-msg `task conform`.
Conventional commits with the scopes of `.conform.yaml`, see
[`CONTRIBUTING.md`](./CONTRIBUTING.md).

## References

- [Kubebuilder book](https://book.kubebuilder.io), in particular
  [markers](https://book.kubebuilder.io/reference/markers.html) and
  [good practices](https://book.kubebuilder.io/reference/good-practices.html).
- [controller-runtime FAQ](https://github.com/kubernetes-sigs/controller-runtime/blob/main/FAQ.md).

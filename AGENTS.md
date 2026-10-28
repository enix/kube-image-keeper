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

Code, chart and tooling are re-added epic by epic
([0002](./notes/0002-development-pipeline.md)), implementing the spec as written: a doubt
about the spec is recorded and deferred, never a reason to edit `docs/v3/` or to stall
([0010](./notes/0010-spec-as-is-until-the-alpha-runs.md)). v2 lives on the `2.3.x` maintenance
branch: read it with `git show 2.3.x:<path>` and lift on purpose, never copy by reflex.
[0001](./notes/0001-v2-reuse-analysis.md) lists what is worth lifting: `internal/registry`
and its availability statuses, the config merge, `internal/info`, the envtest suite
bootstrap; `internal/parallel.FirstSuccessful` is not lifted, the spec probes candidates
sequentially.

## Code

Layout, group `kuik`, version `v1alpha1`:

```text
cmd/                          Manager entry: one subcommand per process (webhook,
                              reconciler, secret-syncer), gating what it registers
api/kuik/v1alpha1/*_types.go  CRD schemas and kubebuilder markers
internal/controller/kuik/*    Reconcilers, one per kind
internal/controller/secretsyncer  The secret syncer loop: one pull Secret per (resource, namespace)
internal/webhook/core/v1/*    Pod mutating webhook (image routing)
internal/registry             Registry client: Check, Copy, ListTags, DeleteTag, requests counter
internal/registry/keychain    Registry credentials from docker config Secrets
internal/registry/credentialprovider  Vendored kubelet keyring, kept as upstream wrote it
internal/registry/registrytest  In-memory registry fakes for the registry specs
internal/registry/pacing      The windows that pace every read of a source registry: check rings, copy queues
internal/registry/tlstrust    The CA Secrets of the tls key, read and handed to the registry clients without restart
internal/info                 Build information and the kuik_build_info collector
internal/imagepath            Image references, repository paths and the segment trie that matches them
internal/mirrorpath           What an ImageMirror does with one origin: destination reference, tags, exclusion
internal/mirrorpath/plan      What an ImageMirror owes its destination: desired state, tag sweep, status counts
internal/mirrorpath/clusterid  The cluster ID: generated once, kept in ConfigMap kuik-cluster-id, read by the reconciler and the webhook
internal/auth                 Credential resolution order and the reserved Secret names
internal/auth/pullsecret      The desired injected pull Secret of a pair, and its grace period
internal/config               The global config file: schema, defaults, validation, reload
internal/routing/podrecord    The three pod annotations the webhook records its decisions in
internal/status/*             Status building blocks shared by the reconcilers: attribution,
                              condition, capped (bounded lists), routingstatus, imagemetrics
                              (the image series ImageMonitor and ImageMirror both export)
config/                       controller-gen output (CRDs, rbac/role.yaml, webhook), read by envtest
helm/kube-image-keeper/       The Helm chart, the only deployment path (crds/ and files/ generated)
test/e2e/                     End-to-end suite, runs on a Kind cluster
test/acceptance/              Acceptance of a release on a cluster where kuik runs: user stories (scenarios.md),
                              YAML fixtures and a program that applies and prints test objects, never asserts
                              (see its README)
hack/                         Developer tools run with go run (the test outline, the values check,
                              the release notes renderer), the CodeRabbit review wait and slot
                              ledger of the pull-request skill, and
                              the commit rules check of check-commits and the Conform workflow
website/                      The docs site (Astro Starlight), see .claude/rules/docs.md
PROJECT                       Kubebuilder metadata
```

**Group new packages** under the package of their concern (`internal/status/*`,
`internal/routing/*`) rather than at the root of `internal/`: a new top-level package needs
a concern no existing one covers.

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
task test-short           # unit suites only, no envtest (the pre-push hook)
# one spec only: the suites are Ginkgo, so filter on the It text, not on -run
go test ./internal/controller/kuik -v -ginkgo.focus 'text of the It'
```

CI fails on any drift in generated files, formatting or `go mod tidy`. `manifests` and
`generate` (so `task test` too) refuse to run while a rebase, a cherry-pick or a merge is
stopped: resolve or abort it first.

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
(Ginkgo, cases before bodies, a `test` commit before each `feat` commit, a test exercises a
behaviour, envtest, e2e),
[`docs.md`](./.claude/rules/docs.md) (Markdown conventions, how the site is built) and
[`helm.md`](./.claude/rules/helm.md) (values per process, RBAC bindings, generated files,
cert-manager). Read the one that matters before editing.

**Every behaviour change ships with its tests and its documentation** in the same PR: the
page under `docs/`, the `# --` comments of `values.yaml` for chart values, this file or the
rule when the layout or the rules change. A change that adds or lifts a known limit updates
`docs/known-limits.md` in the same PR.

**Do what the kubelet does.** When kuik does something the kubelet already does
(credentials, Secret formats, anonymous fallback, retries and backoff, references and
digests, timeouts), do the same or the closest possible, so kuik feels transparent: find
what the kubelet does, cite it, and recommend it first. Follow it when the reasons are the
same: the finalizer backoff of an ImageMirror keeps its 30-minute cap, since it waits for
pods, not for a pull. Where `docs/v3/` decides otherwise, the spec stands
([0010](./notes/0010-spec-as-is-until-the-alpha-runs.md)): recommend the kubelet behaviour
to the maintainers, never edit the spec to get it.

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
  build commands plus `task acceptance-check|inspect|get|setup|apply|delete` (read-only, or writes guarded by the program), `ask` rules that back the outbound
  hook up, `deny` rules on the
  project's secret files (`.env`, keys; `Read` only, a Bash `cat` is not covered) and on
  edits of the acceptance approval (an `Edit` rule covers every file-editing tool, the hook
  covers Bash), and the hooks below. A new local task (reads nothing outside the machine,
  writes only in the working copy) gets its allow rule `task <task> *` in the same PR, and its
  name in the local task list of the outbound hook,
  which allows it itself after a leading `-d` to a worktree (an allow rule cannot pin the value
  of `-d`).
- `hooks/guard-generated-files.sh` refuses Edit and Write on the generated files of
  [Code](#code) and names the task to run instead. A Bash command that writes a file
  (`sed -i`, `>`) is not covered.
- `hooks/confirm-outbound-actions.sh` asks the user before any command that leaves the
  working copy (`git push`, `gh pr|issue` writes, `docker push`, releases, `task deploy`
  or `run`, the e2e tasks, `task acceptance-cleanup`, `task acceptance-config` (an `ask` rule:
  it changes kuik's own configuration) and any `go run|build` of `test/acceptance`,
  any other acceptance task when anything but the acceptance fixtures, Markdown files and tooling (`.claude/`, `.github/`, `.coderabbit.yaml`) differs from `origin/main` and the user has not approved that change with `task acceptance-approve`, or when git could not compare against the real `origin/main`, `task acceptance-approve` itself,
  a `task` with `--taskfile`, `--dir` or `--global` (except a leading `-d` to the top of a
  worktree under `.claude/worktrees/`, checked as the plain task run there),
  `helm install`, `kubectl apply|label`, code downloaded and run on the fly: `npx`, `uvx`,
  `pipx run`, `go run|install <module>@<version>`, which the tasks alone run pinned...), in every permission
  mode, even when several tasks share one call (`task build deploy`); a pure read standing
  alone (`grep`, `rg`, `sed -n '<range>p'`, `cat`, `head`, `tail`, `wc`) is never taken for
  the commands its text names. Not forbidden: the
  user decides, the agent never does it on its own
  ([0017](./notes/0017-a-session-drives-its-pull-request-to-the-merge.md)). To let a session drive its own pull
  request, the hook itself allows 7 single commands, with no prompt: `git push origin <current feature branch>`
  (`-u` and `--force-with-lease` included; a bare `git push` still asks, it may follow an
  upstream on `main`; never a plain `--force`, `main`, a maintenance or release branch or a
  tag; the branch name starts with a letter or a digit, and `origin` is this repository with
  no push refmap, URL rewrite or `push.followTags`; it is refused while auto-merge is
  enabled on the branch's pull request, which would merge the push unreviewed: disable it
  first),
  `gh pr edit <n> --add-label e2e-ready`, `gh pr merge <n> --disable-auto`,
  `gh pr merge <n> --auto --rebase --match-head-commit <full sha of the commit checked out>`
  (starting the work on a pull request is the go to merge it),
  `gh pr comment <n> --body "@coderabbitai review"` (or `full review`) and
  `gh api graphql -f query='mutation { resolveReviewThread(input: {threadId: "PRRT_..."}) { thread { isResolved } } }'`
  (one thread, no alias, variable or `--jq`; the hook checks the thread belongs to the pull
  request) on the pull request of the current branch, and a read written
  `gh api graphql -f query='...' [--jq '...']`, with no single quote, backslash, `$` or
  backtick inside the query (double quotes are fine), no `$` in the filter and no `mutation`
  (any other form asks), and the reads `gh pr view|list|checks|diff`, `gh release view`,
  `gh run view|watch|list`, `gh issue view|list`. Every gh form accepts
  `--repo enix/kube-image-keeper` (or `-R`); another repository asks. Each form must match the
  whole command, so no quoting can hide a second command: run them alone. A read followed by
  a pipe or `2>&1` is refused with the way to trim it (`--jq`, GraphQL `first:`); any other
  addition asks. Any other `gh pr merge` (a direct merge, unpinned) still asks.
  `confirm-outbound-actions.test.sh` checks it on a table of commands in a throwaway
  repository: `task lint-hooks`, run by the Lint workflow.
- `rules/`: the path-scoped conventions above.
- `skills/`: [`test-outline`](./.claude/skills/test-outline/SKILL.md) (the spec-first Ginkgo
  workflow, picked up whenever specs are written),
  [`decision-note`](./.claude/skills/decision-note/SKILL.md) (`/decision-note`, writes a note
  under `notes/` when the filter holds),
  [`pull-request`](./.claude/skills/pull-request/SKILL.md) (prepares the branch, drafts the
  description for the user to confirm, keeps it in step),
  [`e2e-spec`](./.claude/skills/e2e-spec/SKILL.md) (e2e specs on Kind, added last in a PR),
  [`acceptance-scenarios`](./.claude/skills/acceptance-scenarios/SKILL.md) (drafts the
  acceptance scenarios of a release from its commits, with the user, and writes the agreed
  ones; never touches a cluster),
  [`release-acceptance`](./.claude/skills/release-acceptance/SKILL.md) (acceptance of a release on
  a shared cluster: runs the scenarios of `test/acceptance/scenarios.md`, explores and
  judges, and cleans up once the user accepts the results)
  and [`release-notes`](./.claude/skills/release-notes/SKILL.md) (every release: computes
  the next version, drafts its notes for users with the person who releases, and creates the
  draft GitHub release the Release workflow requires).
- `agents/`: [`implementer`](./.claude/agents/implementer.md) (writes one pull request in
  its worktree, `test` then `feat` per component; a hook refuses push, history rewrites,
  GitHub but `gh issue view` and `gh pr view`, and clusters), [`reviewer`](./.claude/agents/reviewer.md) (read-only, traces the
  code: each `feat` commit as it lands, then the whole branch with spec conformance before
  the pull request opens), [`spec-reviewer`](./.claude/agents/spec-reviewer.md) (read-only
  conformance review of the test outline against `docs/v3/`, keeps the spec out of the main
  context; both run only `git diff|show|log|grep`, also with `-C` on a worktree under
  `.claude/worktrees/`, through `hooks/review-guard.sh`) and [`v2-archaeologist`](./.claude/agents/v2-archaeologist.md) (reads v2 on
  `2.3.x` with `git show` and recommends what to lift). The session that owns the pull
  request launches one `implementer` and resumes it for the whole pull request, a fresh
  `reviewer` per review, and does the pushes and GitHub writes itself; CodeRabbit reviews
  last ([0014](./notes/0014-implementer-and-reviewer-agents.md)). The session stays where it
  started and reaches its pull request's worktree by path: it never switches its own
  worktree (EnterWorktree) while an agent works, since the agent's isolation moves with the
  session and blocks it mid-task.
- [`.mcp.json`](./.mcp.json) (at the repository root): the `astro-docs` MCP server, the
  Astro and Starlight documentation for the site under `website/`. Public endpoint, no
  credentials; Claude Code asks once per user before enabling it. The outbound hook
  matches Bash commands only: before adding an MCP server with write tools (GitHub,
  Kubernetes...), extend the hook to its `mcp__*` tools or the guard no longer holds.

The hooks need `jq` and refuse the call without it, so the guards always hold: install
it before developing. `worktrees/`, `artifacts/` and
`settings.local.json` are gitignored.

**One command per Bash call.** The hooks match the whole command: a chain or a substitution
around a command they let through alone makes them ask the user. Never chain (`&&`, `;`, `|`) around a
command that asks (a commit, a push, a GitHub write), never use `$(...)` or backticks, and
write files with Write or Edit, never a heredoc, `>` or `sed -i`. Read-only pipes
(`git log | head`) are fine.

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

**Review fixes** are `fixup!` commits, pushed without rewriting the branch while the review
runs, then folded by one autosquash and force-push once it is over (the
[`pull-request`](./.claude/skills/pull-request/SKILL.md) skill). This is the maintainers'
cycle: `CONTRIBUTING.md` only asks outside contributors for a branch that holds commits
meant for `main`.

**PR descriptions** are written for the reviewer, the way a colleague would: 1 to 3
sentences on what the PR does and why, then a short list of the topics worth reviewing (a
mechanism, a choice to weigh in on, what is deliberately left out), one line each. Link the
spec or the note instead of paraphrasing it. No dense block of prose, no headings or tables,
nothing that restates the commits or the diff. The
[`pull-request`](./.claude/skills/pull-request/SKILL.md) skill gives the shape.

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

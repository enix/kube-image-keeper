# Contributing to kube-image-keeper (kuik)

Thank you for considering contributing to kube-image-keeper! Before you start contributing, please read this guide to understand our contribution process.

## Project status

kube-image-keeper v2 has entered its **maintenance phase**: no new features will be added to v2, only bug fixes. v3 is being built on `main`, against the specification in [`docs/v3/`](./docs/v3/); it is in beta, feature complete for 3.0 but not production-ready yet.

Pull requests targeting v2 must be opened against the `2.3.x` branch. Everything else targets `main`.

## Getting started

### Run kuik locally

Before contributing to kube-image-keeper, you need a Kubernetes cluster with [cert-manager](https://cert-manager.io/docs/installation/) installed (kuik uses it to issue its webhook certificate). See the [development guide](./docs/guides/development.md) for how to run kube-image-keeper locally.

### Devcontainer

The repository ships a [devcontainer](https://containers.dev/) (`.devcontainer/`) with Go, Docker, Kind, kubectl, Helm, kubebuilder, Node.js, Task and lefthook pre-installed, and the git hooks registered. Use it with the [devcontainer CLI](https://github.com/devcontainers/cli) (`npm install -g @devcontainers/cli`) or any editor that supports devcontainers:

```sh
devcontainer up --workspace-folder .              # build and start the container
devcontainer exec --workspace-folder . task test  # run a command in it
```

The repository is bind-mounted, so you can keep editing with your own editor on the host. Everything below is already set up inside the container.

### Task

Build, test and deployment commands are [Task](https://taskfile.dev) tasks, listed by `task --list` and defined in [`Taskfile.yaml`](./Taskfile.yaml). Install it with `go install github.com/go-task/task/v3/cmd/task@latest` (or see [taskfile.dev/installation](https://taskfile.dev/installation/)). The `Makefile` is only a shim for tools that call `make` themselves (the kubebuilder CLI, the e2e suite): `make <target>` forwards to `task <target>`.

### Git hooks (lefthook)

We use [lefthook](https://github.com/evilmartians/lefthook) to run checks locally (code generation, Go linting, Markdown linting, commit message linting). After cloning the repository, install lefthook, then register the hooks:

```sh
# Install lefthook (see https://lefthook.dev/install/ for other methods)
go install github.com/evilmartians/lefthook@latest

# Register the git hooks
lefthook install
```

The Markdown lint step runs `task lint-markdown-fix` on the staged files (`task lint-markdown` checks the whole repository). It runs [`markdownlint-cli2`](https://github.com/DavidAnson/markdownlint-cli2) via `npx` and requires **Node.js ≥ 22** (the `markdownlint-rule-relative-links` rule needs it). If Node.js is missing or older, the step is automatically skipped — contributors who don't touch any `.md` files don't need a Node toolchain.

The commit message step lets `fixup!`, `squash!` and `amend!` commits through (`git commit --fixup`), since `git rebase --autosquash` folds them into their target. CI still lints every commit of a pull request and rejects them there.

## Contributing guidelines

### Issues and feature requests

If you encounter any issues with kube-image-keeper or have ideas for new features, please open an issue on the GitHub repository. When creating an issue, provide a clear and detailed description of the problem or feature request to help us better understand the situation.

### Pull requests

We welcome contributions through pull requests. For your pull request to be accepted, it requires to:

- Pass all tests (run `task test` locally before pushing).
- Include tests covering any new behavior or bug fix.
- Follow the [conventional commits](https://www.conventionalcommits.org/en/v1.0.0/#summary) specification (enforced on every pull request).
- Contain no merge commits (the `Conform` check rejects them). To bring your branch up to date with `main`, rebase it (`git rebase origin/main`) instead of merging `main` into it.
- Hold only commits meant for `main`: pull requests are merged by rebase, so each commit lands as is. Fold review fixes into the commit they fix before the merge, the way you like: amend, interactive rebase, or `git commit --fixup` then `git rebase --autosquash`.

The automated review (CodeRabbit) runs once, when the pull request is opened; comment `@coderabbitai review` to have later commits reviewed.

`Conform`, `Test` and `E2E` are required checks. A pull request that changes no file the e2e suite depends on passes `E2E` without running it and needs no label. Otherwise the suite, which takes minutes on a Kind cluster, runs once, on the branch as it will merge: add the `e2e-ready` label once the pull request is reviewed, its e2e specs are pushed and it holds only commits meant for `main`. Until then, `E2E` fails at once and says what is missing: a branch carrying `fixup!` commits does not run the suite until they are folded and force-pushed.

Dependabot pull requests that target `main` carry the `e2e-ready` label from the start, and a workflow approves them and enables auto-merge: they are merged once the required checks pass. Major updates, the `kubernetes` group and the pull requests that target a maintenance branch are reviewed and merged by hand.

### Commit scopes

Scopes in commit messages are optional, but when used they must belong to the list below. Each scope fits into one of the following categories:

| Category | Scopes | Purpose |
| --- | --- | --- |
| Feature | `mirroring`, `routing`, `monitoring`, `migration` | Describe *what* the change affects functionally (e.g. `feat(routing): ...`); `migration` is the `migrate` subcommand that guides a cluster from v2 to v3. |
| Component | `auth`, `metrics`, `registry`, `helm`, `config`, `secret-syncer`, `pacing` | A distinct code area with its own concerns: credentials and secrets handling, the metrics surface, `internal/registry/`, `helm/kube-image-keeper/`, the global config file (`internal/config/`, its ConfigMap, `docs/configuration.md`), the secret syncer process and the pull secrets it materialises and renews, the pace of the reads kuik makes from registries (`registries.<host>.check` and `copy` in the global config). |
| Origin | `deps` | Dependency updates (e.g. `build(deps): ...`). |
| Tooling | `e2e`, `claude` | Project tooling, not kuik itself: the end-to-end suite under `test/e2e/` with its CI job and label, the shared Claude Code configuration under `.claude/` (skills, agents, rules, hooks). |

**Tooling scopes and types.** The tooling scopes, `claude` and `e2e`, never take `feat` or `fix`: use `chore`, `docs` for a skill or rule whose change is text only, `ci` or `test` (for example `test(e2e): ...`, `chore(claude): ...`). semantic-release lists `feat` and `fix` commits in the release notes and bumps the version on them, and the tooling changes no product behaviour. `task check-commits` rejects them.

**Picking a scope.** Prefer a feature scope over an architectural one. `fix(routing): ...` is more informative than `fix(controller): ...` because the reader learns *what* changed, not where the code happens to live. If no scope fits cleanly, omit it; scopes are optional. If a change crosses feature boundaries, consider splitting it into several commits.

**Proposing a new scope.** Identify which category it belongs to; avoid architectural/layer scopes (`controller`, `webhook`, `crd`) that duplicate feature scopes. Open a pull request updating both `.conform.yaml` and this table.

### Use of AI tools

Using AI tools to help write your PR is acceptable, but **as the author, you are responsible for understanding every change**. We judge the result, not the tools that produced it.

The following rules apply:

- **No AI attribution on commits.** Do not list AI as a co-author, do not co-sign commits with an AI, and do not use trailers like `Assisted-by:` or `Co-developed-by:` referring to an AI. The commit author is the human who submits the work.
- **Verify before you submit.** Do not leave the first review of AI-generated changes to the reviewers. Run `task test`, exercise the behaviour, confirm the APIs/types you reference actually exist, and read the full diff yourself.
- **No large PRs.** Keep each pull request small enough to be reviewed as a whole.
- **Commit messages follow the conventions and say why.** Conventional commit subjects, and a body for what the diff does not show (see [Pull requests](#pull-requests)).
- **Be ready to explain your changes.** If, during review, you cannot explain why a change was made, the PR will be closed.
- **Answer reviews with your own understanding.** Replies to review comments show that you understand the change.

### License

kube-image-keeper is licensed under the [MIT License](./LICENSE). By contributing to this project, you agree to license your contributions under the same license.

## Releases

Releases are cut manually by dispatching the [Release workflow](./.github/workflows/release.yaml) on the branch to publish. semantic-release computes the version from the conventional commits since the last release, and reads its configuration from the dispatched branch's [`.releaserc.mjs`](./.releaserc.mjs): each release line therefore carries its own branches configuration (`main` and `release` for the main line; `X.Y.x-stable` and `X.Y.x-rc` for a maintenance line).

Each release this configuration publishes, betas and rcs included, is announced on Slack with its notes, through the incoming webhook stored in the `SLACK_WEBHOOK` repository secret. The workflow fails, dry-run included, when that secret is missing.

### Channels

- **beta** (from `main`): pre-releases of the next major or minor version. Up to `3.0.0-alpha.6`, `main` published alphas.
- **rc** (from `X.Y.x-rc`): frozen release candidates for a patch of the `X.Y` line, published for validation (for example on a pre-production cluster) before the stable release. Only a release blocker justifies cutting an rc.2.
- **stable** (from `release` for the main line, from `X.Y.x-stable` for a maintenance line): production releases.

### Releasing the main line

- Beta: dispatch the Release workflow on `main`.
- Stable: fast-forward `release` to the commit to publish, then dispatch the workflow on `release`. The `release` branch follows `main`'s lineage and is protected against force pushes and deletion: it only moves forward. It is also the only branch that moves the `:latest` image tag, so a maintenance release never pulls it back to an older major.

### Release notes

From the first beta on, each release carries notes written for the people who install kuik, in this order: highlights, breaking changes (with the upgrade steps), known limits, and a changelog of features and fixes (each collapsed). Their single source is a structured file, `release-notes.yaml`: [`hack/releasenotes`](./hack/releasenotes/notes.go) validates it and renders the release body (`go run ./hack/releasenotes render <file>`) and the chart's `artifacthub.io/changes` (`go run ./hack/releasenotes changes <file>`) from it. Alphas keep the notes semantic-release generates from the commit subjects.

1. Before dispatching the workflow, the person who releases runs the [`release-notes`](./.claude/skills/release-notes/SKILL.md) Claude Code skill. It computes the next version with `task release-dry-run` (a semantic-release dry-run on a fresh clone, with the workflow's plugins), reads the commits, pull requests, closed issues and test outlines since the previous release, writes `release-notes.yaml` and shows what it renders. Without Claude Code, follow the skill's steps by hand.
2. Once the notes are accepted, they go into a draft GitHub release for exactly the tag of that version: the rendered body, `gh release create v<version> --draft --title v<version> --notes-file <dir>/release-notes.md`, and the YAML as its asset, `gh release upload v<version> <dir>/release-notes.yaml`, `<dir>` being where the skill wrote both, outside the repository. A draft creates no tag. The body is only a preview: never edit a draft's notes on GitHub. Any change goes through the skill, which edits the YAML and replaces both the body and the asset.
3. The Release workflow fails before building anything when no draft exists for the tag of the version its dry-run computes, and names that version and the drafts that exist, or when the draft has no non-empty `release-notes.yaml` asset, or when that asset does not validate. A draft for `v3.0.0-beta.1` while semantic-release computes `3.1.0` means commits changed the version: check them, then write the notes again. Versions with an `alpha` pre-release identifier need no draft.
4. The workflow renders the draft's asset into the GitHub release and the Slack message, sets the chart's `artifacthub.io/changes` from it (features as `added`, fixes as `fixed`, breaking changes as `changed`, with their pull requests and issues as links), and deletes the draft. Do not edit the draft once the workflow runs: the chart takes its changes at the start, the release its notes at the end.
5. When the Release job publishes no release, it puts the draft back, with its asset. If the tag `v<version>` was not pushed, dispatch the workflow again. If it was, publish that draft by hand (`gh release edit v<version> --draft=false`; the restored draft is already marked as a pre-release when the version is one): a new dispatch would compute the next version.

### Releasing a maintenance line `X.Y`

- Release candidate: fast-forward `X.Y.x-rc` to the head of `X.Y.x`, then dispatch the workflow on `X.Y.x-rc`.
- Stable: fast-forward `X.Y.x-stable` to the head of `X.Y.x`, then dispatch the workflow on `X.Y.x-stable`.

A maintenance line only ships fixes. `X.Y.x-stable` and `X.Y.x-rc` are regular release branches for semantic-release, so nothing caps their computed version natively; the Release workflow enforces the line instead, and fails when the computed version escapes `X.Y.*` (which would mean a stray `feat` or breaking-change commit landed on the line).

## Cutting a maintenance branch

Each release line `X.Y` is maintained from a **maintenance branch `X.Y.x`**: it carries the release-line code and its published documentation, and receives the line's fixes through pull requests. When cutting a new line `X.Y`:

1. Create the branch from the release tag and push it:

   ```bash
   git switch -c X.Y.x vX.Y.Z
   git push -u origin X.Y.x
   ```

2. Make the release configuration line-local: on `X.Y.x`, trim the `branches` of [`.releaserc.mjs`](./.releaserc.mjs) down to `X.Y.x-stable` and `X.Y.x-rc` (with `prerelease: "rc"`), as described in [Releases](#releases), then commit and push (the Release workflow reads the file from the branch it is dispatched on):

   ```bash
   git commit -m "chore: make the release configuration line-local" .releaserc.mjs
   git push
   ```

3. Create the line's publishing branches from that commit, so both carry the line-local configuration (if they were created earlier, fast-forward them onto it instead):

   ```bash
   git branch X.Y.x-stable X.Y.x
   git branch X.Y.x-rc X.Y.x
   git push -u origin X.Y.x-stable X.Y.x-rc
   ```

4. Retarget the maintenance-branch Dependabot entries: in [`.github/dependabot.yml`](./.github/dependabot.yml) (on `main`), update the `target-branch` of the patch-only `gomod` and `docker` entries to `X.Y.x`, and drop entries for release lines that reached end of life. These entries keep the release line's dependencies patched (security fixes almost always ship as patch releases), and their `fix` commit prefix makes a later release dispatch cut a patch release.

5. Publish the version's documentation on the website: see [Add a new archived version](./website/README.md#add-a-new-archived-version).

# Contributing to kube-image-keeper (kuik)

Thank you for considering contributing to kube-image-keeper! Before you start contributing, please read this guide to understand our contribution process.

## Project status

kube-image-keeper v2 has entered its **maintenance phase**: no new features will be added to v2, only bug fixes. v3 is being built on `main`, against the specification in [`docs/v3/`](./docs/v3/); it is generally available since 3.0.0, stable and ready for production. There is no migration path from v2 yet: v2 stays supported for existing installs until one exists.

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
- Include tests covering any new behavior or bug fix, committed before the code: a `test` commit whose new specs fail, then the `feat` or `fix` commit that makes them pass (e2e specs aside, written last). `task check-commits` runs the specs each `test` commit adds or changes and fails when they already pass there.
- End the body of every `fix` commit with `Introduced-by: #<PR>`, the pull request that introduced the bug, found with `git log`, `git blame` or the pull request history (`Introduced-by: unknown` only when nobody can tell). The release notes read it: a bug of a released version is reported with the version that introduced it, a bug of a feature never released is left out. The `Conform` check and `task check-commits` reject a `fix` commit without it; Dependabot's `fix(deps)` commits, which fix no bug, are exempt.
- Follow the [conventional commits](https://www.conventionalcommits.org/en/v1.0.0/#summary) specification (enforced on every pull request).
- Contain no merge commits (the `Conform` check rejects them). To bring your branch up to date with `main`, rebase it (`git rebase origin/main`) instead of merging `main` into it.
- Say in the description how the pull request differs from the issue it answers, under `Differences from the issue:`, one line each: a point the issue left open and how you settled it, a proposal of the issue done another way and why, an addition the issue did not ask for, or something the issue asks that the pull request leaves out. A pull request that does exactly what its issue asks has no such section. When it does less, or does it another way, also leave a short comment on the issue with the link to the pull request. The automated review checks the section against the issue.
- Hold only commits meant for `main`: pull requests are merged by rebase, so each commit lands as is. Fold review fixes into the commit they fix before the merge, the way you like: amend, interactive rebase, or `git commit --fixup` then `git rebase --autosquash`. Once the review is over, autosquash and force-push: `Conform` fails while a `fixup!`, `squash!` or `amend!` commit remains, and the pull request cannot merge.

The automated review (CodeRabbit) runs once, when the pull request is opened; comment `@coderabbitai review` to have later commits reviewed.

`Conform`, `Test` and `E2E` are required checks. A pull request that changes no file the e2e suite depends on passes `E2E` without running it and needs no label. Otherwise the suite, which takes minutes on a Kind cluster, runs once, on the branch as it will merge: add the `e2e-ready` label once the pull request is reviewed, its e2e specs are pushed and it holds only commits meant for `main`. Until then, `E2E` waits (GitHub shows it as "Expected") and keeps the pull request from merging, while a job named after what is missing (`E2E waits for the e2e-ready label`, `E2E waits for the autosquash`) passes in its place: a branch carrying `fixup!` commits does not run the suite until they are folded and force-pushed.

Dependabot pull requests that target `main` carry the `e2e-ready` label from the start, and a workflow approves them and enables auto-merge: they are merged once the required checks pass. Major updates, the `kubernetes` group and the pull requests that target a maintenance branch are reviewed and merged by hand.

### Commit scopes

Scopes in commit messages are optional, but when used they must belong to the list below. Each scope fits into one of the following categories:

| Category | Scopes | Purpose |
| --- | --- | --- |
| Feature | `mirroring`, `routing`, `monitoring` | Describe *what* the change affects functionally (e.g. `feat(routing): ...`). |
| Component | `auth`, `metrics`, `registry`, `helm`, `config`, `secret-syncer`, `pacing` | A distinct code area with its own concerns: credentials and secrets handling, the metrics surface, `internal/registry/`, `helm/kube-image-keeper/`, the global config file (`internal/config/`, its ConfigMap, `docs/configuration.md`), the secret syncer process and the pull secrets it materialises and renews, the pace of the reads kuik makes from registries (`registries.<host>.check` and `copy` in the global config). |
| Origin | `deps` | Dependency updates (e.g. `build(deps): ...`). |
| Tooling | `e2e`, `claude` | Project tooling, not kuik itself: the end-to-end suite under `test/e2e/` with its CI job and label, the shared Claude Code configuration under `.claude/` (skills, agents, rules, hooks). |

**Tooling scopes and types.** The tooling scopes, `claude` and `e2e`, never take `feat` or `fix`: use `chore`, `docs` for a skill or rule whose change is text only, `ci` or `test` (for example `test(e2e): ...`, `chore(claude): ...`). semantic-release lists `feat` and `fix` commits in the release notes and bumps the version on them, and the tooling changes no product behaviour. The `Conform` check and `task check-commits` reject them.

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

What ships stable has run on clusters first:

- A patch `X.Y.Z` is released from its maintenance line, after a release candidate `X.Y.Z-rc.N`.
- A minor or a major `X.Y.0` is released from `release`, after a beta `X.Y.0-beta.N` from `main`.
- A stable is cut from the exact commit of its last pre-release, and `:latest` (with the GitHub "Latest" badge) stays on the highest line.

A release is cut by dispatching the [Release workflow](./.github/workflows/release.yaml) on the branch to publish. semantic-release computes the version from the conventional commits since the last release. It reads its configuration from the dispatched branch's [`.releaserc.mjs`](./.releaserc.mjs), so each line carries its own branches: `main` and `release` on `main`, `X.Y.x-stable` and `X.Y.x-rc` on a maintenance line `X.Y.x`.

Each release, betas and rcs included, is announced on Slack with its notes, through the incoming webhook stored in the `SLACK_WEBHOOK` repository secret. The workflow fails, dry-run included, when that secret is missing. The message carries the notes whole, one block per section, with each collapsed `<details>` list shown open and its summary line dropped ([`.github/release-slack.mjs`](./.github/release-slack.mjs), checked by `task test-release`); past Slack's 50 blocks, it ends on a link to the release.

### Channels

| Branch | What it publishes |
| --- | --- |
| `main` | Betas of the next minor or major (`X.Y.0-beta.N`). Up to `3.0.0-alpha.6`, it published alphas. |
| `release` | Stables of the latest minor or major (`X.Y.0`), cut from their last beta. Moves `:latest`. |
| `3.0.x`, `3.0.x-rc`, `3.0.x-stable` | The 3.0 maintenance line: fixes only, through pull requests on `3.0.x`. Rcs `3.0.Z-rc.N` from `3.0.x-rc`, then stables `3.0.Z` from `3.0.x-stable`, which moves `:latest` while 3.0 is the highest line. |
| `2.3.x`, `2.3.x-rc`, `2.3.x-stable` | The 2.3 maintenance line (v2): fixes only, rcs then stables, the same way. |

The Release workflow enforces the rule before building anything ([`.github/release-gate.sh`](./.github/release-gate.sh)): a minor `X.Y.0` (`Y > 0`) needs a beta, a patch `X.Y.Z` (`Z > 0`) needs an rc and is released from `X.Y.x-stable` only, and the stable must be the very commit of the last one (the numbered tags only, in version order). The gate does not check majors yet. It holds on the branches whose workflow has it: the 2.3 line is not gated until the change is brought there. It runs once, before anything is built: do not publish a new pre-release of a version while its stable release runs, or the stable may ship from the commit of the previous one.

On a maintenance line, semantic-release caps nothing: the workflow fails when the computed version leaves `X.Y.*`, which means a stray `feat` or breaking-change commit landed on the line.

### Releasing a patch

1. Land the fix on `main` through a pull request.
2. Bring it to the line: on a branch from `X.Y.x`, `git cherry-pick -x <sha>`, then open a pull request on `X.Y.x`.
3. Fast-forward `X.Y.x-rc` to `X.Y.x`: `git fetch origin`, then `git push origin origin/X.Y.x:X.Y.x-rc`.
4. Draft the notes of `X.Y.Z-rc.N` with the `release-notes` skill on the branch `X.Y.x-rc` (see [Release notes](#release-notes)).
5. Dispatch the Release workflow on `X.Y.x-rc`.
6. Accept the rc on a cluster. Only a release blocker justifies an rc.2: fix it from step 1.
7. Push the rc commit to the stable branch: `git fetch origin --tags`, then `git push origin vX.Y.Z-rc.N^{commit}:X.Y.x-stable`.
8. Draft the notes of `X.Y.Z` with the `release-notes` skill on the branch `X.Y.x-stable`.
9. Dispatch the Release workflow on `X.Y.x-stable`.

### Releasing a minor or a major

1. For each beta, draft its notes, then dispatch the Release workflow on `main`. Accept the last beta on a cluster.
2. Fast-forward `release` to that beta: `git fetch origin --tags`, then `git push origin vX.Y.0-beta.N^{commit}:release`. When `release` is already past it, the push is refused: release a new beta on a commit that contains it, then push that one. `release` follows `main`'s lineage and is protected against force pushes and deletion: it only moves forward.
3. Draft the notes of `X.Y.0` with the `release-notes` skill on the branch `release`, then dispatch the Release workflow on `release` once no stable run of the line it supersedes is in progress. From then on, dispatch no stable on the line it supersedes until that line's `IS_LATEST` is `false` (step 3 of [Cutting a maintenance branch](#cutting-a-maintenance-branch)), or it moves `:latest` back.
4. Open the `X.Y` maintenance line for its patches (see [Cutting a maintenance branch](#cutting-a-maintenance-branch)).

### Release notes

Each release, alphas included, carries notes written for the people who install kuik, in this order: an introduction, short by default and free (the person releasing may write it as they like, and reviews it before the release), highlights (0 to 5), breaking changes (with the upgrade steps; without any, the section is left out and a bold line after the introduction says so), and a changelog of features and fixes (each collapsed when there are highlights, open otherwise). Each release is written for the readers of its stability level: its changes are counted from the latest lower version at least as stable on the same line (on another line, the latest stable), so an alpha counts from the previous alpha and the first alpha of a line from the previous stable. The first release of a stability level (`X.Y.Z-alpha.1`, `X.Y.Z-beta.1`, `X.Y.Z-rc.1`, any stable `X.Y.Z`) also serves a second reader, who upgrades from the version directly before it whatever its level, unless that version is the one its changes are counted from: Enix runs every version and nobody skips a level. Its notes list the changes since the version directly before (every feature and every fix, fixes of bugs only pre-releases had included, with no "broken since"), which become its entries on Artifact Hub but do not show in the release body, whose changelog counts from the version its changes are counted from only; the release's `CHANGELOG.md` asset gives developers the commit-level delta since the version directly before. The main fixes list a fix only when its reader could have run the bug (the commit that introduced it is in the version the changes are counted from, or in a released version of the same stability level since), and each says since which version of that level it was broken. A breaking change is listed only for these readers: the one the changes are counted from (or its major line, `v2`), or the version directly before; a change that broke only intermediate pre-releases is left out. When something breaks the second reader, the breaking changes are grouped under a heading per reader, a change that breaks both shows in both groups, and a main reader nothing breaks reads "Nothing breaks since <version>." under its heading; otherwise they are plain bullets. The changelog always opens with the version the changes are counted from. The chart's `artifacthub.io/changes` reads as successive deltas in Artifact Hub's view of every version: a release with a second reader gives that reader's lists, breaking changes, features and fixes, instead of the full summary. Their single source is a structured file, `release-notes.yaml`: [`hack/releasenotes`](./hack/releasenotes/notes.go) validates it and renders the release body (`go run ./hack/releasenotes render <file>`) and the chart's `artifacthub.io/changes` (`go run ./hack/releasenotes changes <file>`) from it.

The notes have no known limits section: [`docs/known-limits.md`](./docs/known-limits.md) holds the full list for the major line, read in its latest state, and must be up to date in the commit released. The notes say only what changes: a limit the release lifts is a feature or a fix in the changelog; a limit it adds is a breaking change (something that worked no longer does) or is said in the entry of the new feature.

1. Before dispatching the workflow, the person who releases runs the [`release-notes`](./.claude/skills/release-notes/SKILL.md) Claude Code skill. It computes the next version with `task release-dry-run` (a semantic-release dry-run on a fresh clone, with the workflow's plugins), reads the commits, pull requests, closed issues and test outlines since the previous release, writes `release-notes.yaml` and shows what it renders. Without Claude Code, follow the skill's steps by hand.
2. Once the notes are accepted, they go into a draft GitHub release for exactly the tag of that version: the rendered body, `gh release create v<version> --draft --title v<version> --notes-file <dir>/release-notes.md`, and the YAML as its asset, `gh release upload v<version> <dir>/release-notes.yaml`, `<dir>` being where the skill wrote both, outside the repository. A draft creates no tag. The body is only a preview: never edit a draft's notes on GitHub. Any change goes through the skill, which edits the YAML and replaces both the body and the asset.
3. The Release workflow fails before building anything when no draft exists for the tag of the version its dry-run computes, and names that version and the drafts that exist, or when the draft has no non-empty `release-notes.yaml` asset, or when that asset does not validate. A draft for `v3.0.0-beta.1` while semantic-release computes `3.1.0` means commits changed the version: check them, then write the notes again.
4. The workflow renders the draft's asset into the GitHub release and the Slack message, sets the chart's `artifacthub.io/changes` from it (features as `added`, fixes as `fixed`, breaking changes as `changed`, with their pull requests and issues as links), and deletes the draft. The published release carries 2 assets: `release-notes.yaml`, the source of its notes, and `CHANGELOG.md`, the changelog semantic-release generates from the commits. Do not edit the draft once the workflow runs: the chart takes its changes at the start, the release its notes at the end.

### Cutting a maintenance branch

Each line `X.Y` is maintained from a **maintenance branch `X.Y.x`**: it carries the line's code and its published documentation, and receives the line's fixes through pull requests. To open a line `X.Y`:

1. Create the branch from the commit of `vX.Y.0` and push it:

   ```bash
   git fetch origin --tags
   git switch -c X.Y.x vX.Y.0
   git push -u origin X.Y.x
   ```

2. Make the release configuration line-local on `X.Y.x`, since the Release workflow reads it from the dispatched branch. In [`.releaserc.mjs`](./.releaserc.mjs), trim `branches` down to `X.Y.x-stable` and `X.Y.x-rc` (with `prerelease: "rc"`). In [`.github/workflows/release.yaml`](./.github/workflows/release.yaml), make `IS_LATEST` require `github.ref_name == 'X.Y.x-stable'` in place of `release`, so the line's patches move `:latest` while it is the highest line. Commit and push:

   ```bash
   git commit -m "chore: make the release configuration line-local for X.Y" .releaserc.mjs .github/workflows/release.yaml
   git push
   ```

3. On the line this one supersedes, set `IS_LATEST` to `false` in its `release.yaml`, so its patches no longer move `:latest` back. The stable branch runs the workflow of the rc it is pushed to: release that line's next stable only from an rc cut after this change.

4. Create the line's publishing branches from the commit of step 2, so both carry the line-local configuration (if they were created earlier, fast-forward them onto it instead):

   ```bash
   git branch X.Y.x-stable X.Y.x
   git branch X.Y.x-rc X.Y.x
   git push -u origin X.Y.x-stable X.Y.x-rc
   ```

5. In [`.github/dependabot.yml`](./.github/dependabot.yml) (on `main`), add a patch-only `gomod` and `docker` pair with `target-branch: X.Y.x`, copied from an existing maintenance pair, and drop the pairs of lines that reached end of life. They keep the line's dependencies patched (security fixes almost always ship as patches), and their `fix` prefix makes the next dispatch cut a patch.

6. Once `main` documents a later version, publish the line's documentation as an archived version: see [Add a new archived version](./website/README.md#add-a-new-archived-version).

### Recovering from a failed run

- **Nothing should be published**: the dry-run found no `feat`, `fix` or breaking change since the last release of the branch. Nothing to recover.
- **The gate refuses**: follow its message. Fast-forward the branch to the tag it names (`git push origin v<version>-<pre>.<N>^{commit}:<branch>`) and dispatch again, or release a new pre-release. When the branch is already past that tag, the push is refused: release a new pre-release on a commit that contains the branch head.
- **The version escapes the line**: a `feat` or breaking-change commit landed on `X.Y.x`. Revert it there with `git revert`, its subject reworded as `revert: <subject>` and its `This reverts commit <sha>.` body kept, then release again.
- **No draft for the version**: see step 3 of [Release notes](#release-notes).
- **A job fails before the Release job** (lint, test, e2e, build): no tag was pushed. Fix the cause and dispatch again. A failed Build may already have pushed the version's images and chart (and `:latest` for a stable of the highest line): the next dispatch publishes the same version over them, as long as no commit landed on the branch meanwhile.
- **The Release job publishes no release**: it puts the draft back, with its asset. If the tag `v<version>` was not pushed, dispatch the workflow again. If it was, publish that draft by hand (`gh release edit v<version> --draft=false`; the restored draft is already marked as a pre-release when the version is one): a new dispatch would compute the next version.

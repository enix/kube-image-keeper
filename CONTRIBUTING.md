# Contributing to kube-image-keeper (kuik)

Thank you for considering contributing to kube-image-keeper! Before you start contributing, please read this guide to understand our contribution process.

## Project status

kube-image-keeper v2 has entered its **maintenance phase**: no new features will be added to v2, only bug fixes. v3 is being built on `main`, against the specification in [`docs/v3/`](./docs/v3/); it is not usable yet and its pre-releases are not production-ready.

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

The Markdown lint step runs [`markdownlint-cli2`](https://github.com/DavidAnson/markdownlint-cli2) via `npx` and requires **Node.js ≥ 22** (the `markdownlint-rule-relative-links` rule needs it). If Node.js is missing or older, the step is automatically skipped — contributors who don't touch any `.md` files don't need a Node toolchain.

The commit message step lets `fixup!`, `squash!` and `amend!` commits through (`git commit --fixup`), since `git rebase --autosquash` folds them into their target. CI still lints every commit of a pull request, so autosquash your branch before asking for review.

## Contributing guidelines

### Issues and feature requests

If you encounter any issues with kube-image-keeper or have ideas for new features, please open an issue on the GitHub repository. When creating an issue, provide a clear and detailed description of the problem or feature request to help us better understand the situation.

### Pull requests

We welcome contributions through pull requests. For your pull request to be accepted, it requires to:

- Pass all tests (run `task test` locally before pushing).
- Include tests covering any new behavior or bug fix.
- Follow the [conventional commits](https://www.conventionalcommits.org/en/v1.0.0/#summary) specification (enforced on every pull request).
- Contain no merge commits. To bring your branch up to date with `main`, rebase it (`git rebase origin/main`) instead of merging `main` into it: merge commits break the commit message linting and clutter the history once the pull request is merged.

Open your pull request as a draft (`gh pr create --draft`) and mark it ready for review once CI passes. The automated review (CodeRabbit) runs once, when the pull request is opened or marked ready for review; comment `@coderabbitai review` to have later commits reviewed.

### Commit scopes

Scopes in commit messages are optional, but when used they must belong to the list below. Each scope fits into one of the following categories:

| Category | Scopes | Purpose |
| --- | --- | --- |
| Feature | `mirroring`, `routing`, `monitoring` | Describe *what* the change affects functionally (e.g. `feat(routing): ...`). |
| Component | `auth`, `metrics`, `registry`, `helm` | A distinct code area with its own concerns: credentials and secrets handling, the metrics surface, `internal/registry/`, `helm/kube-image-keeper/`. |
| Origin | `deps` | Dependency updates (e.g. `build(deps): ...`). |

**Picking a scope.** Prefer a feature scope over an architectural one. `fix(routing): ...` is more informative than `fix(controller): ...` because the reader learns *what* changed, not where the code happens to live. If no scope fits cleanly, omit it; scopes are optional. If a change crosses feature boundaries, consider splitting it into several commits.

**Proposing a new scope.** Identify which category it belongs to; avoid architectural/layer scopes (`controller`, `webhook`, `crd`) that duplicate feature scopes. Open a pull request updating both `.conform.yaml` and this table.

### Use of AI tools

Concerning AI usage in your contributions, we follow the [Kubernetes AI guidance](https://github.com/kubernetes/community/blob/main/contributors/guide/pull-requests.md#ai-guidance).

Using AI tools to help write your PR is acceptable, but **as the author, you are responsible for understanding every change**. If you used AI tools in preparing your PR, you must disclose this in the description of your PR.

The following rules apply:

- **No AI attribution on commits.** Do not list AI as a co-author, do not co-sign commits with an AI, and do not use trailers like `Assisted-by:` or `Co-developed-by:` referring to an AI. The commit author is the human who submits the work.
- **Verify before you submit.** Do not leave the first review of AI-generated changes to the reviewers. Run `task test`, exercise the behaviour, confirm the APIs/types you reference actually exist, and read the full diff yourself.
- **No large AI-generated PRs and no AI-generated commit messages.** Conventional commit subjects and bodies are written by you (see [Pull requests](#pull-requests)).
- **Be ready to explain your changes.** If, during review, you cannot explain why a change was made, the PR will be closed.
- **Respond to reviews yourself.** When replying to review comments, do so without relying on AI tools.

### License

kube-image-keeper is licensed under the [MIT License](./LICENSE). By contributing to this project, you agree to license your contributions under the same license.

## Releases

Releases are cut manually by dispatching the [Release workflow](./.github/workflows/release.yaml) on the branch to publish. semantic-release computes the version from the conventional commits since the last release, and reads its configuration from the dispatched branch's [`.releaserc.mjs`](./.releaserc.mjs): each release line therefore carries its own branches configuration (`main` and `release` for the main line; `X.Y.x-stable` and `X.Y.x-rc` for a maintenance line).

### Channels

- **alpha** (from `main`): development pre-releases of the next major or minor version. The scope is still moving; breaking changes may occur between alphas.
- **rc** (from `X.Y.x-rc`): frozen release candidates for a patch of the `X.Y` line, published for validation (for example on a pre-production cluster) before the stable release. Only a release blocker justifies cutting an rc.2.
- **stable** (from `release` for the main line, from `X.Y.x-stable` for a maintenance line): production releases.

### Releasing the main line

- Alpha: dispatch the Release workflow on `main`.
- Stable: fast-forward `release` to the commit to publish, then dispatch the workflow on `release`. The `release` branch follows `main`'s lineage and is protected against force pushes and deletion: it only moves forward. It is also the only branch that moves the `:latest` image tag, so a maintenance release never pulls it back to an older major.

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

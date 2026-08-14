# Contributing to kube-image-keeper (kuik)

Thank you for considering contributing to kube-image-keeper! Before you start contributing, please read this guide to understand our contribution process.

## Getting started

### Run kuik locally

Before contributing to kube-image-keeper, you need a Kubernetes cluster with [cert-manager](https://cert-manager.io/docs/installation/) installed (kuik uses it to issue its webhook certificate). See the [development guide](./docs/guides/development.md) for how to run kube-image-keeper locally.

### Git hooks (lefthook)

We use [lefthook](https://github.com/evilmartians/lefthook) to run checks locally (code generation, Go linting, Markdown linting, commit message linting). After cloning the repository, install lefthook, then register the hooks:

```sh
# Install lefthook (see https://lefthook.dev/install/ for other methods)
go install github.com/evilmartians/lefthook@latest

# Register the git hooks
lefthook install
```

The Markdown lint step runs [`markdownlint-cli2`](https://github.com/DavidAnson/markdownlint-cli2) via `npx` and requires **Node.js ≥ 22** (the `markdownlint-rule-relative-links` rule needs it). If Node.js is missing or older, the step is automatically skipped — contributors who don't touch any `.md` files don't need a Node toolchain.

## Contributing guidelines

### Issues and feature requests

If you encounter any issues with kube-image-keeper or have ideas for new features, please open an issue on the GitHub repository. When creating an issue, provide a clear and detailed description of the problem or feature request to help us better understand the situation.

### Pull requests

We welcome contributions through pull requests. For your pull request to be accepted, it requires to:

- Pass all tests (run `make test` locally before pushing).
- Include tests covering any new behavior or bug fix.
- Follow the [conventional commits](https://www.conventionalcommits.org/en/v1.0.0/#summary) specification (enforced on every pull request).

### Commit scopes

Scopes in commit messages are optional, but when used they must belong to the list below. Each scope fits into one of the following categories:

| Category | Scopes | Purpose |
| --- | --- | --- |
| Feature | `mirroring`, `routing`, `monitoring`, `metrics` | Describe *what* the change affects functionally (e.g. `feat(routing): ...`). |
| Component | `registry`, `helm` | A distinct code area with its own concerns (`internal/registry/`, `helm/kube-image-keeper/`). |
| Origin | `deps` | Dependency updates (e.g. `build(deps): ...`). |

**Picking a scope.** Prefer a feature scope over an architectural one. `fix(routing): ...` is more informative than `fix(controller): ...` because the reader learns *what* changed, not where the code happens to live. If no scope fits cleanly, omit it; scopes are optional. If a change crosses feature boundaries, consider splitting it into several commits.

**Proposing a new scope.** Identify which category it belongs to; avoid architectural/layer scopes (`controller`, `webhook`, `crd`) that duplicate feature scopes. Open a pull request updating both `.conform.yaml` and this table.

### Use of AI tools

Concerning AI usage in your contributions, we follow the [Kubernetes AI guidance](https://github.com/kubernetes/community/blob/main/contributors/guide/pull-requests.md#ai-guidance).

Using AI tools to help write your PR is acceptable, but **as the author, you are responsible for understanding every change**. If you used AI tools in preparing your PR, you must disclose this in the description of your PR.

The following rules apply:

- **No AI attribution on commits.** Do not list AI as a co-author, do not co-sign commits with an AI, and do not use trailers like `Assisted-by:` or `Co-developed-by:` referring to an AI. The commit author is the human who submits the work.
- **Verify before you submit.** Do not leave the first review of AI-generated changes to the reviewers. Run `make test`, exercise the behaviour, confirm the APIs/types you reference actually exist, and read the full diff yourself.
- **No large AI-generated PRs and no AI-generated commit messages.** Conventional commit subjects and bodies are written by you (see [Pull requests](#pull-requests)).
- **Be ready to explain your changes.** If, during review, you cannot explain why a change was made, the PR will be closed.
- **Respond to reviews yourself.** When replying to review comments, do so without relying on AI tools.

### License

kube-image-keeper is licensed under the [MIT License](./LICENSE). By contributing to this project, you agree to license your contributions under the same license.

## Cutting a maintenance branch

Each release line `X.Y` is maintained from a **maintenance branch `X.Y.x`**: semantic-release publishes maintenance releases from it (see the `branches` glob in [`.releaserc.json`](./.releaserc.json)), and it carries both the release-line code and its published documentation. When cutting a new line `X.Y`:

1. Create the branch from the release tag and push it:

   ```bash
   git switch -c X.Y.x vX.Y.Z
   git push -u origin X.Y.x
   ```

2. Retarget the maintenance-branch Dependabot entries: in [`.github/dependabot.yml`](./.github/dependabot.yml) (on `main`), update the `target-branch` of the patch-only `gomod` and `docker` entries to `X.Y.x`, and drop entries for release lines that reached end of life. These entries keep the release line's dependencies patched (security fixes almost always ship as patch releases), and their `fix` commit prefix makes semantic-release cut a patch release when they are merged.

3. Publish the version's documentation on the website: see [Add a new archived version](./website/README.md#add-a-new-archived-version).

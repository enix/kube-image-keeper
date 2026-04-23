# Contributing to kube-image-keeper (kuik)

Thank you for considering contributing to kube-image-keeper! Before you start contributing, please read this guide to understand our contribution process.

## Getting started

### Installation

Before contributing to kube-image-keeper, ensure you have all prerequisites installed. They are detailed in the [prerequisites](./README.md#prerequisites) section of the readme. You can then install kube-image-keeper following the [installation instructions](./README.md#installation).

### Git hooks (lefthook)

We use [lefthook](https://github.com/evilmartians/lefthook) to run checks locally (code generation, formatting, linting, commit message linting). After cloning the repository, install lefthook and [conform](https://github.com/siderolabs/conform) (used to check commit messages against the conventional commits specification), then register the hooks:

```sh
# Install lefthook (see https://lefthook.dev/installation/ for other methods)
go install github.com/evilmartians/lefthook@latest

# Install conform (pinned to the version used in CI)
go install github.com/siderolabs/conform/cmd/conform@v0.1.0-alpha.31

# Register the git hooks
lefthook install
```

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

### License

kube-image-keeper is licensed under the [MIT License](./LICENSE). By contributing to this project, you agree to license your contributions under the same license.

# 0006 — Task replaces the Makefile

**Date:** 2026-09-15 · **Status:** decided

Build, test and deployment commands live in `Taskfile.yaml` ([Task](https://taskfile.dev)).
The `Makefile` is a shim forwarding `make <target> VAR=value` to `task <target> VAR=value`,
kept for the tools that call make on their own: the kubebuilder CLI and the e2e suite.
Nothing else goes in it.

## Why

- The kubebuilder Makefile is 300 lines of shell inside make. The Taskfile says the same in
  YAML, with a description per task and `task --list` for free.
- `sources` / `generates` skip `manifests` and `generate` when nothing changed.
- Tool versions, installation and the `.custom-gcl.yml` rebuild follow one pattern.

## Cost accepted

- Contributors need `task`: the devcontainer and `CONTRIBUTING.md` install it, CI pins it in
  `.github/actions/setup-task`, next to the shim's `TASK_VERSION`.
- A future re-scaffold emits a full Makefile that has to be discarded, not merged.

## Rejected

- Makefile as the reference with Task as an optional wrapper: two sources of truth drift.

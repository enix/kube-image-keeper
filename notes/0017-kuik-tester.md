# 0017 — kuik-tester: a fault-injecting registry for the tests, in test/kuik-tester

**Date:** 2026-11-06 · **Status:** active

**Decision:** kuik-tester lives in `test/kuik-tester/` as a nested Go module with its own `Tester`
workflow, under the tooling scope `tester` (never `feat` or `fix`); it is
released by dispatching `Tester release` on `main`, which pushes its image and chart to quay.io
and then tags `test/kuik-tester/vX.Y.Z`; it logs with slog.

**Why:**

- kuik's tests need registries that fail on purpose: `test/e2e/mirror_test.go` hand-built one with
  nginx. Users can point kuik at it too (`test/kuik-tester/docs/demo.md`).
- A nested module keeps a test tool out of the module kuik builds and deploys.
- The tag is the Go version of the nested module; semantic-release and kuik's `v*` lookups skip it.
- slog covers its three log lines without a logging dependency.

**Rejected:**

- A standalone `enix/kuik-tester` repository: it serves kuik's e2e suite and changes with kuik.
- semantic-release: one version line per repository, kuik's; a second needs its own config.
- Publishing on a tag push: any pushed tag publishes, with no check of the branch or version.
- Publishing every merge to `main`: no stable version for users nor for Go.
- logr like kuik: a dependency for three lines, in a tool that is not kuik.

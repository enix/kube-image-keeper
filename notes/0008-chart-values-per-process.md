# 0008 — Chart values: root defaults, one block per process

**Date:** 2026-09-23 · **Status:** decided

**Decision:** the pod settings of the three processes live at the root of `values.yaml`
and apply to all of them. `webhook`, `reconciler` and `secretSyncer` repeat every one of
those keys, empty, with a `# @default -- the root ...` annotation; a value set there
replaces the root one for that process alone. Two exceptions: `podAnnotations` is merged
over the root map, `env` is appended to the root list. An empty value falls back to the
root, so a root setting cannot be unset for a single process. A setting that only one
process has (`webhook.certificate`) lives in its block alone. `hack/valuescheck` fails the
Lint workflow when a block misses a root key.

**Why:**

- The three Deployments differ in replicas, resources and scheduling, so one shared block
  cannot describe them; three full blocks would triple every default and let them drift.
- Repeating the keys empty is how argo-cd and kyverno get helm-docs to document every
  setting of every component without shadowing the shared default.
- The root rather than `global:` follows Enix's x509-certificate-exporter chart; `global:`
  has no special meaning without subcharts, so the two only differ by convention.
- Replace rather than merge for scheduling and resources is what argo-cd, kyverno and x509
  do; only labels and annotations are merged, only env and volumes appended.

**Rejected:**

- `global:` for the shared settings: equivalent, and one convention apart from x509.
- A single `manager:` block for the three (the previous shape): the processes differ.
- Deep-merging `resources` or `securityContext`: no chart surveyed does it, and it hides
  which side a value came from.

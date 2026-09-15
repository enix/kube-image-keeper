# 0007 — Helm is the only deployment path

**Date:** 2026-09-15 · **Status:** decided

The Helm chart under `helm/kube-image-keeper/` deploys kuik everywhere: developer clusters
(`task deploy`, `task kind-deploy`), e2e and production. `config/` keeps only what
controller-gen writes and envtest reads (`crd/`, `rbac/`, `webhook/`, `samples/`); the
kustomize overlays (`default/`, `manager/`, `certmanager/`, `prometheus/`, `network-policy/`)
are gone. `task manifests` copies the generated CRDs and RBAC rules into the chart, as v2 did.

## Why

- One install path: what a contributor deploys is what users install and what e2e will test.
  The kustomize path already diverged from the chart (wrong entrypoint, `failurePolicy: Fail`).
- The chart is a designed product (3 processes, `secretAccess`, PDB, ServiceMonitor,
  documented values) that the v2 chart already shaped; kustomize only duplicated it, and
  Enix operates Helm daily.

## Rejected

- Kubebuilder's `helm/v2-alpha` plugin: renders `kustomize build` into `dist/chart/`,
  regenerated with `--force`, which overwrites `values.yaml`. A build artifact, not a chart
  one can design; and it keeps kustomize as the source.
- Gitignoring `config/default`: only the kubebuilder CLI recreates it, at scaffolding time,
  and an ignored file is absent from a fresh clone anyway.

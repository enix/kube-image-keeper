---
paths:
  - "helm/**"
---

# Helm chart

- The chart is **the only deployment path** ([0007](../../notes/0007-helm-only.md)), in
  dev, CI and production: `task deploy IMG=...`, or `task kind-deploy` on a Kind cluster.
  `config/` holds no deployment overlay; do not add kustomize bases or patches.
- **Values per process** ([0008](../../notes/0008-chart-values-per-process.md)): a pod
  setting goes at the root of `values.yaml` **and**, empty with a `# @default -- the root
  ...` line, in each of `webhook`, `reconciler` and `secretSyncer`; a setting one process
  alone has goes in its block only. `templates/deployments.yaml` resolves the fallback once
  at the top of its loop, never inline in the body. `task lint-values` checks the blocks
  (`hack/valuescheck`, run by the Lint workflow).
- **RBAC** ([0009](../../notes/0009-rbac-rules-from-markers-bindings-from-chart.md)): the
  rules are generated into `files/rbac.yaml` from the `// +kubebuilder:rbac` markers of the
  Go code; the chart owns only the bindings (`templates/rbac.yaml`). A marker without
  `roleName=` fails `helm template`.
- **Generated, never edit by hand**: `crds/*.yaml` and `files/rbac.yaml` come from
  `task manifests`; `README.md` comes from `task generate-helm-docs`, which reads the
  `# --` comments of `values.yaml` and `README.md.gotmpl`. Every new or changed value
  gets its `# --` comment in the same change.
- The webhook serving certificate comes from cert-manager (`templates/webhook.yaml`:
  Certificate, Issuer and `cert-manager.io/inject-ca-from` on the
  MutatingWebhookConfiguration); a cluster without cert-manager cannot run kuik.
- Check a change with `task helm-lint`, `task lint-values` and `helm template`.

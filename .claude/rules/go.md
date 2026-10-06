---
paths:
  - "**/*.go"
---

# Go conventions

- **Reconcilers**: idempotent; re-fetch the object before updating it; report state with
  `metav1.Condition`; watch secondary resources with `Owns()` / `Watches()` rather than
  polling with `RequeueAfter`; use finalizers only for external resources.
- **API types**: follow the
  [Kubernetes API conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md);
  `metav1.Time` for dates, validation and default markers on fields. The CRD schema is the
  contract with `docs/v3/spec.md` and `docs/v3/status.md`: a shared Go struct must never
  surface a field where the spec does not define one, even always zero or hidden by
  `omitempty`. When two populations share most fields, embed the common struct inline in a
  dedicated type that adds the extra field, rather than adding it to the shared type.
- **Image references**: canonicalize with `github.com/distribution/reference`
  (`ParseNormalizedNamed`, as v2 did) before any comparison; `nginx`, `docker.io/nginx`
  and `docker.io/library/nginx:latest` are the same image.
- **Logging**: structured, `log := logf.FromContext(ctx)`. Messages follow the
  [Kubernetes style](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-instrumentation/logging.md#message-style-guidelines):
  capitalised, no trailing period, past tense, object type named (`"Created Deployment"`,
  not `"Created"`), balanced key-value pairs.
  - Levels: `Info` for a decision or a state change, `V(1)` for per-candidate or per-check
    detail. An expected failure (a registry unreachable, a copy retried) goes at `Info` or
    `V(1)` with an `"error"` key, never `log.Error`: `log.Error` is for what an operator must
    act on. Only DPanic carries a stacktrace.
  - Log an error or return it, never both: controller-runtime already logs a returned error
    as `"Reconciler error"`.
  - Keys: `"image"` (a container's image as its pod spec writes it, `nginx`; any other image
    reference in canonical form, `docker.io/library/nginx:latest`), `"registry"`, `"reason"` (a reason of
    `docs/v3/observability.md`), `"candidate"`, `"resource"` (`<kind>/<name>`), `"path"` (a
    file); `klog.KObj` / `klog.KRef` for a Kubernetes object, under its lowercase kind
    (`"secret"`, `"pod"`). No synonym: a key that names an image is `"image"`, whatever its role
    in the line, unless two images share it (the webhook's `"origin"`).
  - Every deletion is logged at `Info`: events expire, logs are the audit trail.
  - Never log the content of a Secret, only its reference (`klog.KRef`).
- **RBAC** ([0009](../../notes/0009-rbac-rules-from-markers-bindings-from-chart.md)): rules
  are `// +kubebuilder:rbac` markers on the code that exercises the permission, each with
  `roleName=` naming its process (`webhook`, `reconciler`, `secret-syncer`) or
  `secret-reader` (cluster-wide Secret read, bound by `secretAccess`). The rules are
  generated, never written in `config/rbac/role.yaml`; the bindings are the chart's
  (`templates/rbac.yaml`). A marker without `roleName=` fails `helm template`; the e2e suite
  checks the granted permissions against `docs/v3/architecture.md`. Delete the
  `admin/editor/viewer` roles `kubebuilder create api` scaffolds under `config/rbac/`.
- **Scaffold**: never delete `// +kubebuilder:scaffold:*` comments, the CLI injects code
  there. Scaffold new kinds and webhooks with `kubebuilder create api` /
  `kubebuilder create webhook`, never by hand.
- After editing `*_types.go` or any kubebuilder marker: `task manifests generate`. After
  editing any `*.go`: `task lint-fix`. CI fails on any drift in generated files,
  formatting or `go mod tidy`.

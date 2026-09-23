# 0009 — RBAC: rules from the markers, bindings from the chart

**Date:** 2026-09-23 · **Status:** decided

**Decision:** every `+kubebuilder:rbac` marker names the process it serves with `roleName=`
(`webhook`, `reconciler`, `secret-syncer`), or `secret-reader` for the cluster-wide Secret
read. One controller-gen pass emits one role per name; the chart reads that file verbatim,
names each role, binds it to its process's ServiceAccount, and binds `secret-reader` as
`secretAccess` says. The default role name is `unassigned`, and the chart fails on it.

**Why:**

- A marker sits on the code that exercises the permission, so the two change together.
- controller-tools 0.22 supports `roleName=` per marker, which removes the only reason to
  write the rules elsewhere.
- Bindings depend on install-time choices (ServiceAccount names, `secretAccess`).

**Rejected:**

- Hand-written rules in the chart: the permissions would drift from the code.
- 3 controller-gen passes on disjoint packages: it imposes a package layout per process.

**Constraints:** a new watch or client call needs its marker in the same change. The
syncer's Secret writes and the ValidatingAdmissionPolicy bounding them ship with its loop.

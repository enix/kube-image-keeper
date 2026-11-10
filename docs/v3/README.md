# kuik v3 design documents

These files are the specification of kuik v3 as decided before the implementation started. They are kept as a historical record and are not updated: what kuik 3.0 actually does differs in places.

To know what kuik does, read the user documentation at [kuik.enix.io](https://kuik.enix.io), from [`docs/`](../index.md) in this repository: the [CRD reference](../crds.md), the [configuration](../configuration.md) and the [known limits](../known-limits.md).

For example, the spec has `ImageMonitor` detect drift and skew: a running digest that differs from the digest of the reference. kuik 3.0 does not do it, because Kubernetes exposes no reliable, non-invasive way to know which digest a container actually runs, for instance the platform manifest of a multi-arch index (see [kubernetes/kubernetes#115199](https://github.com/kubernetes/kubernetes/issues/115199)). Drift detection stays on [`ImageMirror`](../crds.md#imagemirror) (`driftPolicy`), which does not need that information and still catches a mutable tag that moved.

To read the design, start with [`spec.md`](./spec.md), then [`status.md`](./status.md), the [walkthroughs](./walkthroughs/) and [`open-questions.md`](./open-questions.md).

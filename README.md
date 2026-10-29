<!-- HELM_DOCS_LOGO -->
<!-- markdownlint-disable-next-line MD041 -->
<p align="center">
  <img alt="kuik" title="kube-image-keeper" src="https://raw.githubusercontent.com/enix/kube-image-keeper/refs/heads/main/docs/assets/logo.svg">
</p>
<!-- /HELM_DOCS_LOGO -->

<div align="center">

[![Release][release-img]][release] [![Documentation][docs-img]][docs] [![Kubernetes 1.30+][kubernetes-img]][kubernetes] [![Artifact Hub][artifacthub-img]][artifacthub] [![MIT license][license-img]][license] [![Brought to you by Enix][enix-img]][enix]

</div>

<!-- A branch root has no relative form, so no-unpinned-repo-urls cannot be satisfied here. -->
<!-- markdownlint-disable search-replace -->
> [!WARNING]
> **kuik v3 is in beta: every feature planned for 3.0 is in, and we are now stabilizing it.** It works, but it is not stable yet: do not run it in production. Please try it on a test or staging cluster and [tell us how it goes](https://github.com/enix/kube-image-keeper/issues): each report brings the GA closer. See [Status](#status) for what is in. The latest stable version is maintained on the [`2.3.x`](https://github.com/enix/kube-image-keeper/tree/2.3.x) branch: see its [releases](https://github.com/enix/kube-image-keeper/releases) and its documentation on [kuik.enix.io](https://kuik.enix.io/2.3/).
<!-- markdownlint-enable search-replace -->

[release]: https://github.com/enix/kube-image-keeper/releases
[release-img]: https://img.shields.io/github/v/release/enix/kube-image-keeper?sort=semver&display_name=tag&logo=github&label=Release&color=3a6ed7
[docs]: https://kuik.enix.io
[docs-img]: https://img.shields.io/badge/Docs-kuik.enix.io-3a6ed7
[kubernetes]: https://kuik.enix.io/installation/
[kubernetes-img]: https://img.shields.io/badge/Kubernetes-1.30%2B-326ce5?logo=kubernetes&logoColor=white
[artifacthub]: https://artifacthub.io/packages/helm/enix/kube-image-keeper
[artifacthub-img]: https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/enix
[license]: https://opensource.org/licenses/MIT
[license-img]: https://img.shields.io/badge/License-MIT-blue.svg
[enix]: https://enix.io
[enix-img]: https://img.shields.io/badge/Brought%20to%20you%20by-ENIX-%23377dff?labelColor=888&logo=data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAA4AAAAOCAQAAAC1QeVaAAAABGdBTUEAALGPC/xhBQAAACBjSFJNAAB6JgAAgIQAAPoAAACA6AAAdTAAAOpgAAA6mAAAF3CculE8AAAAAmJLR0QA/4ePzL8AAAAHdElNRQfkBAkQIg/iouK/AAABZ0lEQVQY0yXBPU8TYQDA8f/zcu1RSDltKliD0BKNECYZmpjgIAOLiYtubn4EJxI/AImzg3E1+AGcYDIMJA7lxQQQQRAiSSFG2l457+655x4Gfz8B45zwipWJ8rPCQ0g3+p9Pj+AlHxHjnLHAbvPW2+GmLoBN+9/+vNlfGeU2Auokd8Y+VeYk/zk6O2fP9fcO8hGpN/TUbxpiUhJiEorTgy+6hUlU5N1flK+9oIJHiKNCkb5wMyOFw3V9o+zN69o0Exg6ePh4/GKr6s0H72Tc67YsdXbZ5gENNjmigaXbMj0tzEWrZNtqigva5NxjhFP6Wfw1N1pjqpFaZQ7FAY6An6zxTzHs0BGqY/NQSnxSBD6WkDRTf3O0wG2Ztl/7jaQEnGNxZMdy2yET/B2xfGlDagQE1OgRRvL93UOHqhLnesPKqJ4NxLLn2unJgVka/HBpbiIARlHFq1n/cWlMZMne1ZfyD5M/Aa4BiyGSwP4Jl3UAAAAldEVYdGRhdGU6Y3JlYXRlADIwMjAtMDQtMDlUMTQ6MzQ6MTUrMDI6MDDBq8/nAAAAJXRFWHRkYXRlOm1vZGlmeQAyMDIwLTA0LTA5VDE0OjM0OjE1KzAyOjAwsPZ3WwAAAABJRU5ErkJggg==

**kuik** (pronounced /kwɪk/, like "quick") is the shortname of **kube-image-keeper**.

✅ Its primary objective is to **maximize the availability of Pod images** strictly within the Kubernetes cluster it runs on.

✅ Its secondary goal is to ensure **bulletproof reliability** by keeping the manipulation of Kubernetes primitives to an absolute minimum.

## Under the hood

It relies on three core mechanisms:

- **Image routing**: rewrites Pod image paths on the fly during their creation to redirect them to a functional registry.
- **Image copy**: mirror images **used by the local cluster** across registries, building a virtual, highly available registry.
- **Image monitoring**: continuously tracks the availability of Pod images **used within the local cluster** across various registries.

> [!NOTE]
> Image routing is performed at Pod creation by a lightweight `MutatingWebhook` that automatically rewrites the image path whenever the source registry becomes unavailable.

## Status

This branch holds **kuik v3**, a rewrite released as betas since [3.0.0-beta.1](https://github.com/enix/kube-image-keeper/releases/tag/v3.0.0-beta.1):

- [**v3.0 beta**](https://github.com/enix/kube-image-keeper/milestone/7), reached on 2026-10-27: every feature planned for 3.0 is in, not yet stable.
- [**v3.0 GA**](https://github.com/enix/kube-image-keeper/milestone/5), next: stable, ready for production.

| Feature | Status |
| ------- | ------ |
| [Image routing](./docs/crds.md#imagealternative) (`ImageAlternative`, mutating webhook) | Available since 3.0.0-alpha.2 |
| [Pull secret injection](./docs/concepts/pull-secrets.md) (secret syncer) | Available since 3.0.0-alpha.3 |
| [Image copy](./docs/crds.md#imagemirror) (`ImageMirror`) | Available since 3.0.0-alpha.4 |
| [Image monitoring](./docs/crds.md#imagemonitor) (`ImageMonitor`) | Tracking and checks available since 3.0.0-alpha.4, drift and alternatives since 3.0.0-alpha.5 |
| [Admission validation](https://github.com/enix/kube-image-keeper/issues/719) | Available since 3.0.0-alpha.5 |
| Credentials from a cloud provider (`auth.provider`) | Accepted and validated, but not used yet: kuik skips it and tries the next credential. Planned for v3.1 ([#747](https://github.com/enix/kube-image-keeper/issues/747)) |
| Plain-HTTP alternatives and mirror destinations (`insecure`) | Available since 3.0.0-alpha.2 for `ImageAlternative`, since 3.0.0-alpha.4 for `ImageMirror` |
| [Private certificate authorities and self-signed certificates](./docs/configuration.md#registry-tls) (`tls`) | Available since 3.0.0-alpha.6 |
| Migration from v2 | None: a migration plan will be proposed once GA is out ([#749](https://github.com/enix/kube-image-keeper/issues/749)) |

- **Stable version**: [v2.3](https://github.com/enix/kube-image-keeper/releases), documented on [kuik.enix.io](https://kuik.enix.io/2.3/). It is in maintenance: bug fixes only.
- **v3 specification**: [`docs/v3/`](./docs/v3/), starting with [`spec.md`](./docs/v3/spec.md). It is the source of truth for v3 behaviour.
- **Custom resources**: `ImageAlternative` (routing), `ImageMirror` (copy) and `ImageMonitor` (monitoring), described in the [CRD reference](./docs/crds.md).

Development process and decisions are recorded in [`notes/`](./notes/). Contributions follow [`CONTRIBUTING.md`](./CONTRIBUTING.md).

<!-- HELM_DOCS_END -->

## License

kube-image-keeper is developed by [Enix](https://enix.io) and released under the [MIT License](./LICENSE).

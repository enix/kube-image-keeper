# kube-image-keeper (kuik)

[![Releases](https://github.com/enix/kube-image-keeper/actions/workflows/release.yaml/badge.svg?branch=main)](https://github.com/enix/kube-image-keeper/releases)
[![Go report card](https://goreportcard.com/badge/github.com/enix/kube-image-keeper)](https://goreportcard.com/report/github.com/enix/kube-image-keeper)
[![MIT license](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Brought to you by Enix](https://img.shields.io/badge/Brought%20to%20you%20by-ENIX-%23377dff?labelColor=888&logo=data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAA4AAAAOCAQAAAC1QeVaAAAABGdBTUEAALGPC/xhBQAAACBjSFJNAAB6JgAAgIQAAPoAAACA6AAAdTAAAOpgAAA6mAAAF3CculE8AAAAAmJLR0QA/4ePzL8AAAAHdElNRQfkBAkQIg/iouK/AAABZ0lEQVQY0yXBPU8TYQDA8f/zcu1RSDltKliD0BKNECYZmpjgIAOLiYtubn4EJxI/AImzg3E1+AGcYDIMJA7lxQQQQRAiSSFG2l457+655x4Gfz8B45zwipWJ8rPCQ0g3+p9Pj+AlHxHjnLHAbvPW2+GmLoBN+9/+vNlfGeU2Auokd8Y+VeYk/zk6O2fP9fcO8hGpN/TUbxpiUhJiEorTgy+6hUlU5N1flK+9oIJHiKNCkb5wMyOFw3V9o+zN69o0Exg6ePh4/GKr6s0H72Tc67YsdXbZ5gENNjmigaXbMj0tzEWrZNtqigva5NxjhFP6Wfw1N1pjqpFaZQ7FAY6An6zxTzHs0BGqY/NQSnxSBD6WkDRTf3O0wG2Ztl/7jaQEnGNxZMdy2yET/B2xfGlDagQE1OgRRvL93UOHqhLnesPKqJ4NxLLn2unJgVka/HBpbiIARlHFq1n/cWlMZMne1ZfyD5M/Aa4BiyGSwP4Jl3UAAAAldEVYdGRhdGU6Y3JlYXRlADIwMjAtMDQtMDlUMTQ6MzQ6MTUrMDI6MDDBq8/nAAAAJXRFWHRkYXRlOm1vZGlmeQAyMDIwLTA0LTA5VDE0OjM0OjE1KzAyOjAwsPZ3WwAAAABJRU5ErkJggg==)](https://enix.io)

**kuik** (pronounced /kwɪk/, like "quick") is the shortname of **kube-image-keeper**.

✅ Its primary objective is to **maximize the availability of Pod images** strictly within the Kubernetes cluster it runs on.

✅ Its secondary goal is to ensure **bulletproof reliability** by keeping the manipulation of Kubernetes primitives to an absolute minimum.

## Under the hood

It relies on three core mechanisms:

- **Image routing**: rewrites Pod image paths on the fly during their creation to redirect them to a functional registry.
- **Image copy**: mirror images **used by the local cluster** across registries, building a virtual, highly available registry.
- **Image monitoring**: continuously tracks the availability of Pod images **used within the local cluster** across various registries.

Note : image routing is performed at Pod creation by a lightweight `MutatingWebhook` that automatically rewrites the image path whenever the source registry becomes unavailable.

## Status

This branch holds **kuik v3**, a rewrite currently in development. It is not usable yet: no
release, no chart, no published image.

- **Stable version**: [v2.3](https://github.com/enix/kube-image-keeper/releases), documented on
  [kuik.enix.io](https://kuik.enix.io). It is in maintenance: bug fixes only.
- **v3 specification**: [`docs/v3/`](./docs/v3/), starting with [`spec.md`](./docs/v3/spec.md).
  It is the source of truth for v3 behaviour and is reviewed in the
  [specification pull request](https://github.com/enix/kube-image-keeper/pull/629).
- **Custom resources**: `ImageAlternative` (routing), `ImageMirror` (copy) and `ImageMonitor`
  (monitoring), described in the [CRD reference](./docs/crds.md).

Development process and decisions are recorded in [`notes/`](./notes/). Contributions follow
[`CONTRIBUTING.md`](./CONTRIBUTING.md).

<!-- HELM_DOCS_END -->

## License

kube-image-keeper is developed by [Enix](https://enix.io) and released under the [MIT License](./LICENSE).

# kube-image-keeper (kuik)

[![Releases](https://github.com/enix/kube-image-keeper/actions/workflows/release.yaml/badge.svg?branch=v2)](https://github.com/enix/kube-image-keeper/releases)
[![Go report card](https://goreportcard.com/badge/github.com/enix/kube-image-keeper)](https://goreportcard.com/report/github.com/enix/kube-image-keeper)
[![MIT license](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Brought to you by Enix](https://img.shields.io/badge/Brought%20to%20you%20by-ENIX-%23377dff?labelColor=888&logo=data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAA4AAAAOCAQAAAC1QeVaAAAABGdBTUEAALGPC/xhBQAAACBjSFJNAAB6JgAAgIQAAPoAAACA6AAAdTAAAOpgAAA6mAAAF3CculE8AAAAAmJLR0QA/4ePzL8AAAAHdElNRQfkBAkQIg/iouK/AAABZ0lEQVQY0yXBPU8TYQDA8f/zcu1RSDltKliD0BKNECYZmpjgIAOLiYtubn4EJxI/AImzg3E1+AGcYDIMJA7lxQQQQRAiSSFG2l457+655x4Gfz8B45zwipWJ8rPCQ0g3+p9Pj+AlHxHjnLHAbvPW2+GmLoBN+9/+vNlfGeU2Auokd8Y+VeYk/zk6O2fP9fcO8hGpN/TUbxpiUhJiEorTgy+6hUlU5N1flK+9oIJHiKNCkb5wMyOFw3V9o+zN69o0Exg6ePh4/GKr6s0H72Tc67YsdXbZ5gENNjmigaXbMj0tzEWrZNtqigva5NxjhFP6Wfw1N1pjqpFaZQ7FAY6An6zxTzHs0BGqY/NQSnxSBD6WkDRTf3O0wG2Ztl/7jaQEnGNxZMdy2yET/B2xfGlDagQE1OgRRvL93UOHqhLnesPKqJ4NxLLn2unJgVka/HBpbiIARlHFq1n/cWlMZMne1ZfyD5M/Aa4BiyGSwP4Jl3UAAAAldEVYdGRhdGU6Y3JlYXRlADIwMjAtMDQtMDlUMTQ6MzQ6MTUrMDI6MDDBq8/nAAAAJXRFWHRkYXRlOm1vZGlmeQAyMDIwLTA0LTA5VDE0OjM0OjE1KzAyOjAwsPZ3WwAAAABJRU5ErkJggg==)](https://enix.io)

**kuik** (pronounced /kwɪk/, like "quick") is the shortname of **kube-image-keeper**.

Its primary objective is to **maximize the availability of Pod images** within a Kubernetes cluster.

Its secondary goal is to ensure **bulletproof reliability** by keeping the manipulation of Kubernetes primitives to an absolute minimum.

## Under the hood

kuik operates as a lightweight webhook that automatically rewrites image paths whenever the source registry becomes unavailable.

It relies on three core mechanisms:
- **Image routing**: rewrites Pod image paths on the fly to redirect them to a functional registry.
- **Image copy**: mirror images between registries to build a virtual, highly available registry.
- **Image monitoring**: continuously tracks the availability of Pod images across various registries.

Developed by Enix, kube-image-keeper is a battle-tested solution currently running in production across multiple Kubernetes clusters.

## Table of contents

📖 **Documentation, concepts and use cases are available here: [kuik.enix.io](https://kuik.enix.io)**

- [Get started](#-get-started)
- [Releases & Roadmap](#-releases--roadmap)
- [Known limitations to date](#-known-limitations-to-date)
- [Why Version 2?](#why-version-2)

## 🚀 Get started

We rely on [cert-manager Custom Resources](./helm/kube-image-keeper/templates/webhook-certificate.yaml) to manage the kuik mutating webhook certificate, so you need to [install it first](https://cert-manager.io/docs/installation/).

```bash
VERSION=2.2.2
helm upgrade --install --create-namespace --namespace kuik-system kube-image-keeper oci://quay.io/enix/charts/kube-image-keeper:$VERSION
```

<!-- HELM_DOCS_END -->

Custom Resource Definitions (CRDs) are used to configure the behavior of kuik such as its routing and mirroring features. Those are described in the [CRD reference](./docs/crds.md).

To setup an [*ImageSetMirror* (or a *ClusterImageSetMirror*)](./docs/crds.md#clusterimagesetmirror), you will first need to configure a registry where kuik will copy matched images. Then generate a token with permission to pull, push and delete (if cleanup enabled) in this registry and create the secret to use in your *ImageSetMirror* with:

```bash
kubectl create secret docker-registry my-registry-secret --docker-server=my-registry.company.com --docker-username=my-username --docker-password=my-token
```

If you let kuik cleanup expired images in your registry, you still have to configure garbage collection on your own as kuik only delete images reference.

## 📅 Releases & Roadmap

> [!NOTE]
> Kuik v2 has reached **General Availability** and is **Production Ready** as of v2.2.2 🚀

### Already available

- [**v2.0**](https://github.com/enix/kube-image-keeper/releases/tag/v2.1.0) We announced the launch of version 2.0 (General Availability) at the [Cloud Native Days France 2026 convention](https://www.cloudnativedays.fr/)
- [**v2.1**](https://github.com/enix/kube-image-keeper/releases/tag/v2.1.0) Priorities for routing and replication are now a thing
  - [**v2.1.1**](https://github.com/enix/kube-image-keeper/releases/tag/v2.1.1) Fix concurrent access to a single registry (in particular regarding the garbage collect mechanism) by multiple Kuik instances on multiple clusters
- [**v2.2**](https://github.com/enix/kube-image-keeper/releases/tag/v2.2.0) Complete implementation of the **Image monitoring** feature with associated metrics

### Planned features

- [**v2.3** Better filtering](https://github.com/enix/kube-image-keeper/milestone/1)
- [**v2.4** Complete OCI support of tags, architectures and digests](https://github.com/enix/kube-image-keeper/milestone/3)
- [**v2.5** Improve stability & efficiancy](https://github.com/enix/kube-image-keeper/milestone/2)

## 🚧 Known limitations to date

- The mutating webhook do not support the Pod `Update` call
- Digest tags are not supported, ex: `@sha256:cb4e4ffc5789fd5ff6a534e3b1460623df61cba00f5ea1c7b40153b5efb81805`
- Per-platform mirroring status is not tracked in the (Cluster)ImageSetMirror status. As a result: (1) Kuik cannot report which architectures are actually mirrored for a given image — a mirror is marked successful as long as at least one configured platform is available, and missing platforms are only logged as a warning; and (2) changing `mirroring.platforms` after images have been mirrored does not re-mirror or clean up already-copied manifests (added or removed platforms only apply to subsequent mirror operations)

## Why Version 2?

Even if we are *proud* of what we achieved with the v1 of **kube-image-keeper**, it was too often painful to work with: it was hard to deploy, overly complex, and the image caching feature — while ambitious — introduced often too much issues. We missed our original goal: to make kube-image-keeper an **easy, no-brainer install for any cluster** which would help ops in their day to day work and **provide confidence**.

We learned *a lot* from this experience and with v2, ***we're starting fresh!*** Our focus is on **simplicity** and **ease of use** with the same set of features and even more! kuik should be effortless to install and to use — you shouldn't have to think twice before adding it to your cluster. Our goal: you will **forget it's even there** and don't even notice when a registry goes down or an image becomes unavailable.

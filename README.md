# kube-image-keeper (kuik)

[![Releases](https://github.com/enix/kube-image-keeper/actions/workflows/release.yaml/badge.svg?branch=v2)](https://github.com/enix/kube-image-keeper/releases)
[![Go report card](https://goreportcard.com/badge/github.com/enix/kube-image-keeper)](https://goreportcard.com/report/github.com/enix/kube-image-keeper)
[![MIT license](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Brought to you by Enix](https://img.shields.io/badge/Brought%20to%20you%20by-ENIX-%23377dff?labelColor=888&logo=data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAA4AAAAOCAQAAAC1QeVaAAAABGdBTUEAALGPC/xhBQAAACBjSFJNAAB6JgAAgIQAAPoAAACA6AAAdTAAAOpgAAA6mAAAF3CculE8AAAAAmJLR0QA/4ePzL8AAAAHdElNRQfkBAkQIg/iouK/AAABZ0lEQVQY0yXBPU8TYQDA8f/zcu1RSDltKliD0BKNECYZmpjgIAOLiYtubn4EJxI/AImzg3E1+AGcYDIMJA7lxQQQQRAiSSFG2l457+655x4Gfz8B45zwipWJ8rPCQ0g3+p9Pj+AlHxHjnLHAbvPW2+GmLoBN+9/+vNlfGeU2Auokd8Y+VeYk/zk6O2fP9fcO8hGpN/TUbxpiUhJiEorTgy+6hUlU5N1flK+9oIJHiKNCkb5wMyOFw3V9o+zN69o0Exg6ePh4/GKr6s0H72Tc67YsdXbZ5gENNjmigaXbMj0tzEWrZNtqigva5NxjhFP6Wfw1N1pjqpFaZQ7FAY6An6zxTzHs0BGqY/NQSnxSBD6WkDRTf3O0wG2Ztl/7jaQEnGNxZMdy2yET/B2xfGlDagQE1OgRRvL93UOHqhLnesPKqJ4NxLLn2unJgVka/HBpbiIARlHFq1n/cWlMZMne1ZfyD5M/Aa4BiyGSwP4Jl3UAAAAldEVYdGRhdGU6Y3JlYXRlADIwMjAtMDQtMDlUMTQ6MzQ6MTUrMDI6MDDBq8/nAAAAJXRFWHRkYXRlOm1vZGlmeQAyMDIwLTA0LTA5VDE0OjM0OjE1KzAyOjAwsPZ3WwAAAABJRU5ErkJggg==)](https://enix.io)

**kuik** (pronounced /kwɪk/, like "quick") is the shortname of **kube-image-keeper**, a container image routing, mirroring and replication system for Kubernetes developed by Enix. It helps make applications more highly available by ensuring reliable access to container images.

## 🚀 Status: General Availability

> [!NOTE]
> kuik v2 is a **complete rewrite** of the project with a focus on **simplicity** and **ease of use**.

> [!CAUTION]
> Not recommended for production use yet. Kuik v2 is currently being battle tested on several clusters.

## What's new in v2 !

Mostly a redesigned architecture

- **Minimal default features**: core functionality enabled by default, others opt-in
- **Image routing**: kuik can rewrite Pod images on-the-fly to point to an operational registry
- **Image replication**: kuik can manage copy between registries to create a virtual highly available registry
- **Image monitoring**: kuik can monitor image availability across various registries (planned for v2.2)
- **Redesigned CRDs** for better clarity and extensibility

## When to use Kube Image Keeper

### ✅ Overcome public registry limitations
- You face an image pull rate limit
- Your upstream registry is no longer available

See: [Overcome public registry limitations](/docs/use-case/overcome-public-registry-limitations.md)

### ✅ Detect missing images before outage
- You plan a maintenance which will reschedule a lot of pods on new workers
- You plan a Kubernetes upgrade
- You have a lot of legacy images deployed on your cluster

See: [Detect missing images before outage](/docs/use-case/detect-missing-images-before-outage.md)

### ✅ Protect images from garbage collect
- You have an aggressive garbage collect
- You have plenty of images (outdated, prior versions, development version) but only a small fraction is being used in reality
- You would like to push only a subset of useful images to your production registry

See: [Protect images from garbage collect](/docs/use-case/protect-images-from-garbage-collect.md)

### ✅ Better performance with local registry
- You use a development registry (ex: gitlab, maven, ...) for production Kubernetes clusters.
- Your registry is overloaded.
- Image pull from Kubernetes are too slow / long.
- Your source registry is too far away (from a network / geographic / latency standpoint) from the Kubernetes cluster

See: [Better performance with local registry](/docs/use-case/better-performance-with-local-registry.md)

### ✅ Automatically route images to a proxy cache registry
- You already have setup a proxy cache registry (like Harbor or Gitlab proxy cache) but do not know how to use it
- You do not want to review all workloads deployments (and change their image path)

See: [Automatically route images to a proxy cache registry](/docs/use-case/automatically-route-images-to-a-proxy-cache-registry.md)

## 📅 Releases & Roadmap

### Already available

- [**v2.0**](https://github.com/enix/kube-image-keeper/releases/tag/v2.1.0) We announced the launch of version 2.0 (General Availability) at the [Cloud Native Days France 2026 convention](https://www.cloudnativedays.fr/)
- [**v2.1**](https://github.com/enix/kube-image-keeper/releases/tag/v2.1.0) Priorities for routing and replication are now a thing
  - [**v2.1.1**](https://github.com/enix/kube-image-keeper/releases/tag/v2.1.1) Fix concurrent access to a single registry (in particular regarding the garbage collect mechanism) by multiple Kuik instances on multiple clusters
- [**v2.2**](https://github.com/enix/kube-image-keeper/releases/tag/v2.2.0) Complete implementation of the **Image monitoring** feature with associated metrics

### Planned features

- **v2.3** Various quality of life improvements
  - Better filtering for cluster wide resources (`includeNamespace` & `excludeNamespace`)
  - Optional monitoring of mirrored images with re-mirroring when needed
- **v2.4** Improve stability of critical components (such as the mutating webhook) by deploying them individually

## 🚧 Known limitations to date

- ~~Mirrored images are considered replicated even if the image was later deleted~~ fixed in `v2.1.1`
- Competition between Kuik's cluster wide custom resources and namespaced resources might lead to weird scenarios (to be partially fixed in `v2.1.1`)
- The mutating webhook do not support the Pod `Update` call
- With replication enabled from registry A to registry B, launching a Pod with image on B will be rerouted (rewritten) to image on A
- Digest tags are not supported, ex: `@sha256:cb4e4ffc5789fd5ff6a534e3b1460623df61cba00f5ea1c7b40153b5efb81805`


## 📦 Installation

```bash
kubectl create namespace kuik-system
VERSION=2.2.0
helm upgrade --install --namespace kuik-system kube-image-keeper oci://quay.io/enix/charts/kube-image-keeper:$VERSION
```

<!-- HELM_DOCS_END -->

Custom Resource Definitions (CRDs) are used to configure the behavior of kuik such as its routing and mirroring features. Those are described in the [docs/crds.md](./docs/crds.md) document.

## Why Version 2?

Even if we are _proud_ of what we achieved with the v1 of **kube-image-keeper**, it was too often painful to work with: it was hard to deploy, overly complex, and the image caching feature — while ambitious — introduced often too much issues. We missed our original goal: to make kube-image-keeper an **easy, no-brainer install for any cluster** which would help ops in their day to day work and **provide confidence**.

We learned _a lot_ from this experience and with v2, **_we're starting fresh!_** Our focus is on **simplicity** and **ease of use** with the same set of features and even more! kuik should be effortless to install and to use — you shouldn't have to think twice before adding it to your cluster. Our goal: you will **forget it's even there** and don't even notice when a registry goes down or an image becomes unavailable.

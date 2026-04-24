# kube-image-keeper (kuik)

[![Releases](https://github.com/enix/kube-image-keeper/actions/workflows/release.yaml/badge.svg?branch=v2)](https://github.com/enix/kube-image-keeper/releases)
[![Go report card](https://goreportcard.com/badge/github.com/enix/kube-image-keeper)](https://goreportcard.com/report/github.com/enix/kube-image-keeper)
[![MIT license](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Brought to you by Enix](https://img.shields.io/badge/Brought%20to%20you%20by-ENIX-%23377dff?labelColor=888&logo=data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAA4AAAAOCAQAAAC1QeVaAAAABGdBTUEAALGPC/xhBQAAACBjSFJNAAB6JgAAgIQAAPoAAACA6AAAdTAAAOpgAAA6mAAAF3CculE8AAAAAmJLR0QA/4ePzL8AAAAHdElNRQfkBAkQIg/iouK/AAABZ0lEQVQY0yXBPU8TYQDA8f/zcu1RSDltKliD0BKNECYZmpjgIAOLiYtubn4EJxI/AImzg3E1+AGcYDIMJA7lxQQQQRAiSSFG2l457+655x4Gfz8B45zwipWJ8rPCQ0g3+p9Pj+AlHxHjnLHAbvPW2+GmLoBN+9/+vNlfGeU2Auokd8Y+VeYk/zk6O2fP9fcO8hGpN/TUbxpiUhJiEorTgy+6hUlU5N1flK+9oIJHiKNCkb5wMyOFw3V9o+zN69o0Exg6ePh4/GKr6s0H72Tc67YsdXbZ5gENNjmigaXbMj0tzEWrZNtqigva5NxjhFP6Wfw1N1pjqpFaZQ7FAY6An6zxTzHs0BGqY/NQSnxSBD6WkDRTf3O0wG2Ztl/7jaQEnGNxZMdy2yET/B2xfGlDagQE1OgRRvL93UOHqhLnesPKqJ4NxLLn2unJgVka/HBpbiIARlHFq1n/cWlMZMne1ZfyD5M/Aa4BiyGSwP4Jl3UAAAAldEVYdGRhdGU6Y3JlYXRlADIwMjAtMDQtMDlUMTQ6MzQ6MTUrMDI6MDDBq8/nAAAAJXRFWHRkYXRlOm1vZGlmeQAyMDIwLTA0LTA5VDE0OjM0OjE1KzAyOjAwsPZ3WwAAAABJRU5ErkJggg==)](https://enix.io)

**kuik** (pronounced /kwɪk/, like "quick") is the shortname of **kube-image-keeper**, a container image routing, mirroring (caching) and replication system for Kubernetes developed by Enix. It helps make applications more highly available by ensuring reliable access to container images.

> [!NOTE]
> Kuik v2 has reached **General Availability** and is **Production Ready** as of v2.2.2 🚀

## Table of contents

- [Introduction](#introduction)
- [When to use Kube Image Keeper](#when-to-use-kube-image-keeper)
- [Documentation](#-documentation)
- [Releases & Roadmap](#-releases--roadmap)
- [Known limitations to date](#-known-limitations-to-date)
- [Installation](#-installation)
- [Why Version 2?](#why-version-2)

## Introduction

kuik v2 is a **complete rewrite** of the project with a focus on **simplicity** and **ease of use** :

- **Minimal default features**: core functionality enabled by default, others opt-in
- **Image routing**: kuik can rewrite Pod images on-the-fly to point to an operational registry
- **Image copy**: kuik can manage copy between registries to create a virtual highly available registry
- **Image monitoring**: kuik can monitor image availability across various registries
- **Redesigned CRDs** for better clarity and extensibility

### Concept : Container image alternatives

KuiK use a mutating webhook to rewrite pod containers images when their are not available.
It use [Custom Resources *ImageSetMirror* and *ReplicatedImageSet*](docs/crds.md) to generate a list of **alternatives** image values (including **original** one) for a given container image and check their availability to know if we keep using **original** image or **rewrite** it to an available **alternative**.

*ReplicatedImageSet* and *ImageSetMirror* both generate **alternatives** images when checking image availability in mutating webhook, but *ImageSetMirror* also handle the copy of **original** image to the given mirror registry.

## When to use Kube Image Keeper

### ✅ Overcome public registry limitations

- You face an image pull rate limit
- Your upstream registry is no longer available

&emsp;[Implementation guide](/docs/use-case/overcome-public-registry-limitations.md)

### ✅ Detect missing images before outage

- You plan a maintenance which will reschedule a lot of pods on new workers
- You plan a Kubernetes upgrade
- You have a lot of legacy images deployed on your cluster

&emsp;[Implementation guide](/docs/use-case/detect-missing-images-before-outage.md)

### ✅ Protect images from garbage collect

- You have an aggressive garbage collect
- You have plenty of images (outdated, prior versions, development version) but only a small fraction is being used in reality
- You would like to push only a subset of useful images to your production registry

&emsp;[Implementation guide](/docs/use-case/protect-images-from-garbage-collect.md)

### ✅ Automatically route images to a proxy cache registry

- You already have setup a proxy cache registry (like Harbor or Gitlab proxy cache) but do not know how to use it
- You do not want to review all workloads deployments (and change their image path)

&emsp;[Implementation guide](/docs/use-case/automatically-route-images-to-a-proxy-cache-registry.md)

### ✅ Better performance with local registry

- You use a development registry (ex: gitlab, maven, ...) for production Kubernetes clusters.
- Your registry is overloaded.
- Image pull from Kubernetes are too slow / long.
- Your source registry is too far away (from a network / geographic / latency standpoint) from the Kubernetes cluster

&emsp;[Implementation guide](/docs/use-case/better-performance-with-local-registry.md)

## 📘 Documentation

- A detailed explanation of all [Kuik Custom Resources](docs/crds.md)
- Kuik manages multiple alternatives of an image and selects the best-suited one. You might want to learn more about the [Priority mechanism](docs/image-routing.md)
- A preliminary migration path from [Kuik v1 to Kuik v2](docs/v1-to-v2-migration-path.md)
- A collection of documented [use cases](#when-to-use-kube-image-keeper)
- A [development guide](docs/development.md)

## 📅 Releases & Roadmap

### Already available

- [**v2.0**](https://github.com/enix/kube-image-keeper/releases/tag/v2.1.0) We announced the launch of version 2.0 (General Availability) at the [Cloud Native Days France 2026 convention](https://www.cloudnativedays.fr/)
- [**v2.1**](https://github.com/enix/kube-image-keeper/releases/tag/v2.1.0) Priorities for routing and replication are now a thing
  - [**v2.1.1**](https://github.com/enix/kube-image-keeper/releases/tag/v2.1.1) Fix concurrent access to a single registry (in particular regarding the garbage collect mechanism) by multiple Kuik instances on multiple clusters
- [**v2.2**](https://github.com/enix/kube-image-keeper/releases/tag/v2.2.0) Complete implementation of the **Image monitoring** feature with associated metrics

### Planned features

- **v2.3** Various quality of life improvements
  - Better filtering for resources (`includeNamespace` & `excludeNamespace`, `includeLabels` & `excludeLabels`, …)
  - Optional monitoring of mirrored images with re-mirroring when needed
- **v2.4** Improve stability of critical components (such as the mutating webhook) by deploying them individually

## 🚧 Known limitations to date

- The mutating webhook do not support the Pod `Update` call
- Digest tags are not supported, ex: `@sha256:cb4e4ffc5789fd5ff6a534e3b1460623df61cba00f5ea1c7b40153b5efb81805`

## 📦 Installation

We rely on [cert-manager Custom Resources](./helm/kube-image-keeper/templates/webhook-certificate.yaml) to manage the kuik mutating webhook certificate, so you need to [install it first](https://cert-manager.io/docs/installation/).

```bash
VERSION=2.2.2
helm upgrade --install --create-namespace --namespace kuik-system kube-image-keeper oci://quay.io/enix/charts/kube-image-keeper:$VERSION
```

<!-- HELM_DOCS_END -->

Custom Resource Definitions (CRDs) are used to configure the behavior of kuik such as its routing and mirroring features. Those are described in the [docs/crds.md](./docs/crds.md) document.

To setup an [*ImageSetMirror* (or a *ClusterImageSetMirror*)](./docs/crds.md#clusterimagesetmirror), you will first need to configure a registry where kuik will copy matched images. Then generate a token with permission to pull, push and delete (if cleanup enabled) in this registry and create the secret to use in your *ImageSetMirror* with:

```bash
kubectl create secret docker-registry my-registry-secret --docker-server=my-registry.company.com --docker-username=my-username --docker-password=my-token
```

If you let kuik cleanup expired images in your registry, you still have to configure garbage collection on your own as kuik only delete images reference.

## Why Version 2?

Even if we are *proud* of what we achieved with the v1 of **kube-image-keeper**, it was too often painful to work with: it was hard to deploy, overly complex, and the image caching feature — while ambitious — introduced often too much issues. We missed our original goal: to make kube-image-keeper an **easy, no-brainer install for any cluster** which would help ops in their day to day work and **provide confidence**.

We learned *a lot* from this experience and with v2, ***we're starting fresh!*** Our focus is on **simplicity** and **ease of use** with the same set of features and even more! kuik should be effortless to install and to use — you shouldn't have to think twice before adding it to your cluster. Our goal: you will **forget it's even there** and don't even notice when a registry goes down or an image becomes unavailable.

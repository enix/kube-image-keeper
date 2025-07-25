# kube-image-keeper (kuik)

[![Releases](https://github.com/enix/kube-image-keeper/actions/workflows/release.yaml/badge.svg?branch=v2)](https://github.com/enix/kube-image-keeper/releases)
[![Go report card](https://goreportcard.com/badge/github.com/enix/kube-image-keeper)](https://goreportcard.com/report/github.com/enix/kube-image-keeper)
[![MIT license](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Brought to you by Enix](https://img.shields.io/badge/Brought%20to%20you%20by-ENIX-%23377dff?labelColor=888&logo=data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAA4AAAAOCAQAAAC1QeVaAAAABGdBTUEAALGPC/xhBQAAACBjSFJNAAB6JgAAgIQAAPoAAACA6AAAdTAAAOpgAAA6mAAAF3CculE8AAAAAmJLR0QA/4ePzL8AAAAHdElNRQfkBAkQIg/iouK/AAABZ0lEQVQY0yXBPU8TYQDA8f/zcu1RSDltKliD0BKNECYZmpjgIAOLiYtubn4EJxI/AImzg3E1+AGcYDIMJA7lxQQQQRAiSSFG2l457+655x4Gfz8B45zwipWJ8rPCQ0g3+p9Pj+AlHxHjnLHAbvPW2+GmLoBN+9/+vNlfGeU2Auokd8Y+VeYk/zk6O2fP9fcO8hGpN/TUbxpiUhJiEorTgy+6hUlU5N1flK+9oIJHiKNCkb5wMyOFw3V9o+zN69o0Exg6ePh4/GKr6s0H72Tc67YsdXbZ5gENNjmigaXbMj0tzEWrZNtqigva5NxjhFP6Wfw1N1pjqpFaZQ7FAY6An6zxTzHs0BGqY/NQSnxSBD6WkDRTf3O0wG2Ztl/7jaQEnGNxZMdy2yET/B2xfGlDagQE1OgRRvL93UOHqhLnesPKqJ4NxLLn2unJgVka/HBpbiIARlHFq1n/cWlMZMne1ZfyD5M/Aa4BiyGSwP4Jl3UAAAAldEVYdGRhdGU6Y3JlYXRlADIwMjAtMDQtMDlUMTQ6MzQ6MTUrMDI6MDDBq8/nAAAAJXRFWHRkYXRlOm1vZGlmeQAyMDIwLTA0LTA5VDE0OjM0OjE1KzAyOjAwsPZ3WwAAAABJRU5ErkJggg==)](https://enix.io)

**kube-image-keeper** helps Kubernetes users monitor and manage container images across their clusters. This is a complete ground-up rewrite of the project — introducing a new design, new architecture, and improved observability.

> [!WARNING]
> v2 is currently in **Alpha**. Expect breaking changes and limited functionality as we iterate.

## 🤷 Why rebooting kube-image-keeper?

Even if we are _proud_ of what we achieved with the v1 of **kube-image-keeper**, it was too often painful to work with: it was hard to deploy, overly complex, and the image caching feature — while ambitious — introduced often too much issues. We missed our original goal: to make kube-image-keeper an **easy, no-brainer install for any cluster** which would help ops in their day to day work and **provide confidence**.

We learnt _a lot_ from this experience and with v2, **_we're starting fresh!_** Our focus is on **simplicity** and **observability** first. Caching is no longer the core feature — it will return later as an opt-in, second-class citizen. This reboot is about doing **_one thing well_**: giving clear visibility into what images are used, where, and how.

## ✨ What's New in v2

- **Total rewrite** with a cleaner architecture.
- **Minimal default features**: core functionality enabled by default, others opt-in.
- CRDs redesigned for better **clarity** and **extensibility**.
- Caching as an opt-in, second-class citizen.

## 🔍 Current Features

- **Image discovery**:
  - Detect images used by running Pods
- **Upstream image monitoring**:
  - Monitor tag availability
  - Monitor registry status (up/down)
- **Prometheus metrics** for all discovered images

## 🚧 Roadmap

Planned features (subject to change):

- Discover images present on cluster nodes.
- Detect upstream retags.
- Support for tracking Deployments, StatefulSets, Jobs, etc.
- Pluggable image CVE scanner.
- Optional image caching.

## 🧪 Status: Alpha

We're actively developing and **breaking changes will happen**.

Feedback is welcome – open an issue or start a discussion.

> [!CAUTION]
> Not recommended for production use yet.

## 📦 Installation

```bash
kubectl create namespace kuik-system
VERSION=2.0.0-alpha.X
helm upgrade --install --namespace kuik-system kube-image-keeper oci://quay.io/enix/charts/kube-image-keeper:$VERSION
```

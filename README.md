# kube-image-keeper (kuik)

[![Releases](https://github.com/enix/kube-image-keeper/actions/workflows/release.yaml/badge.svg?branch=v2)](https://github.com/enix/kube-image-keeper/releases)
[![Go report card](https://goreportcard.com/badge/github.com/enix/kube-image-keeper)](https://goreportcard.com/report/github.com/enix/kube-image-keeper)
[![MIT license](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Brought to you by Enix](https://img.shields.io/badge/Brought%20to%20you%20by-ENIX-%23377dff?labelColor=888&logo=data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAA4AAAAOCAQAAAC1QeVaAAAABGdBTUEAALGPC/xhBQAAACBjSFJNAAB6JgAAgIQAAPoAAACA6AAAdTAAAOpgAAA6mAAAF3CculE8AAAAAmJLR0QA/4ePzL8AAAAHdElNRQfkBAkQIg/iouK/AAABZ0lEQVQY0yXBPU8TYQDA8f/zcu1RSDltKliD0BKNECYZmpjgIAOLiYtubn4EJxI/AImzg3E1+AGcYDIMJA7lxQQQQRAiSSFG2l457+655x4Gfz8B45zwipWJ8rPCQ0g3+p9Pj+AlHxHjnLHAbvPW2+GmLoBN+9/+vNlfGeU2Auokd8Y+VeYk/zk6O2fP9fcO8hGpN/TUbxpiUhJiEorTgy+6hUlU5N1flK+9oIJHiKNCkb5wMyOFw3V9o+zN69o0Exg6ePh4/GKr6s0H72Tc67YsdXbZ5gENNjmigaXbMj0tzEWrZNtqigva5NxjhFP6Wfw1N1pjqpFaZQ7FAY6An6zxTzHs0BGqY/NQSnxSBD6WkDRTf3O0wG2Ztl/7jaQEnGNxZMdy2yET/B2xfGlDagQE1OgRRvL93UOHqhLnesPKqJ4NxLLn2unJgVka/HBpbiIARlHFq1n/cWlMZMne1ZfyD5M/Aa4BiyGSwP4Jl3UAAAAldEVYdGRhdGU6Y3JlYXRlADIwMjAtMDQtMDlUMTQ6MzQ6MTUrMDI6MDDBq8/nAAAAJXRFWHRkYXRlOm1vZGlmeQAyMDIwLTA0LTA5VDE0OjM0OjE1KzAyOjAwsPZ3WwAAAABJRU5ErkJggg==)](https://enix.io)

**kuik** (pronounced /kwɪk/, like "quick") is the shortname of **kube-image-keeper**, a container image routing, mirroring and replication system for Kubernetes developed by Enix. It helps make applications more highly available by ensuring reliable access to container images.

## 🧪 Status: General Availability

> [!NOTE]
> kuik v2 is a **complete rewrite** of the project with a focus on **simplicity** and **ease of use**.

> [!CAUTION]
> Not recommended for production use yet. Kuik v2 is currently being battle tested on several clusters.

## ✨ What's New in v2

### 🔍 Redesigned Architecture

- **Minimal default features**: core functionality enabled by default, others opt-in
- **Image routing**: kuik can rewrite Pod images on-the-fly to point to an operational registry
- **Image replication**: kuik can manage copy between registries to create a virtual highly available registry
- **Image monitoring**: kuik can monitor image availability across various registries
- **Redesigned CRDs** for better clarity and extensibility

## 🚧 Roadmap

Planned features for future minor versions (subject to change):

- We expect to communicate the launch of the General Availability of v2.0 at the [Cloud Native Days France 2026 convention](https://www.cloudnativedays.fr/)
- Implement priorities for routing and replication
- Completion of the monitoring implementation
- Support of concurrent access to a single registry (in particular regarding the garbage collect mechanism) by multiple Kuik instances on multiple clusters.

## Known limitations to date

- Digest tags are not supported, ex: `@sha256:cb4e4ffc5789fd5ff6a534e3b1460623df61cba00f5ea1c7b40153b5efb81805`
- Mirrored images are considered replicated even if the image was letter deleted
- The mutating webhook do not support the Pod Update call
- With replication enabled from registry A to registry B, launching a Pod with image on B will be rerouted (rewrittent) to image on A
- Competition between Kuik's cluster wide custom ressources and namespaced ressources might lead to weird scenarios

## 📦 Installation

```bash
kubectl create namespace kuik-system
VERSION=2.0.0
helm upgrade --install --namespace kuik-system kube-image-keeper oci://quay.io/enix/charts/kube-image-keeper:$VERSION
```

<!-- HELM_DOCS_END -->

## 🔧 Development

```bash
# generate CRDs definitions from go code and install them on the cluster you're connected to
make install
# run the manager locally against the cluster you're connected to and export metrics to :8080
make run
```

### Makefile options

The way kuik is run using the Makefile can be configured through environment variables:

- `RUN_FLAG_DEVEL`: sets the `-zap-devel` flag, defaults to `true`
- `RUN_FLAG_LOG_LEVEL`: sets the `-zap-log-level` flag if present
- `RUN_FLAG_ZAP_ENCODER`: sets the `-zap-encoder` flag if present
- `METRICS_PORT`: sets the port to bind for the metrics, defaults to `8080`
- `RUN_ADDITIONAL_ARGS`: add any additional argument to the `go run ./cmd/main.go` command (you can even `| grep` here)
- `RUN_ARGS`: default arguments to the `go run ./cmd/main.go` command, it combines all previous variables together. Don't touch it if you don't need to.

I highly suggest that you try [github.com/pamburus/hl](https://github.com/pamburus/hl), an awesome tool to make json logs human readable. It can be setup with kuik like this:

```bash
export RUN_FLAG_ZAP_ENCODER=json RUN_ADDITIONAL_ARGS="2>&1 | hl --paging=never"
make run
```

## 🤷 Why Version 2?

Even if we are _proud_ of what we achieved with the v1 of **kube-image-keeper**, it was too often painful to work with: it was hard to deploy, overly complex, and the image caching feature — while ambitious — introduced often too much issues. We missed our original goal: to make kube-image-keeper an **easy, no-brainer install for any cluster** which would help ops in their day to day work and **provide confidence**.

We learned _a lot_ from this experience and with v2, **_we're starting fresh!_** Our focus is on **simplicity** and **ease of use** with the same set of features and even more! kuik should be effortless to install and to use — you shouldn't have to think twice before adding it to your cluster. Our goal: you will **forget it's even there** and don't even notice when a registry goes down or an image becomes unavailable.

# kube-image-keeper (kuik)

[![Releases](https://github.com/enix/kube-image-keeper/actions/workflows/release.yaml/badge.svg?branch=v2)](https://github.com/enix/kube-image-keeper/releases)
[![Go report card](https://goreportcard.com/badge/github.com/enix/kube-image-keeper)](https://goreportcard.com/report/github.com/enix/kube-image-keeper)
[![MIT license](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Brought to you by Enix](https://img.shields.io/badge/Brought%20to%20you%20by-ENIX-%23377dff?labelColor=888&logo=data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAA4AAAAOCAQAAAC1QeVaAAAABGdBTUEAALGPC/xhBQAAACBjSFJNAAB6JgAAgIQAAPoAAACA6AAAdTAAAOpgAAA6mAAAF3CculE8AAAAAmJLR0QA/4ePzL8AAAAHdElNRQfkBAkQIg/iouK/AAABZ0lEQVQY0yXBPU8TYQDA8f/zcu1RSDltKliD0BKNECYZmpjgIAOLiYtubn4EJxI/AImzg3E1+AGcYDIMJA7lxQQQQRAiSSFG2l457+655x4Gfz8B45zwipWJ8rPCQ0g3+p9Pj+AlHxHjnLHAbvPW2+GmLoBN+9/+vNlfGeU2Auokd8Y+VeYk/zk6O2fP9fcO8hGpN/TUbxpiUhJiEorTgy+6hUlU5N1flK+9oIJHiKNCkb5wMyOFw3V9o+zN69o0Exg6ePh4/GKr6s0H72Tc67YsdXbZ5gENNjmigaXbMj0tzEWrZNtqigva5NxjhFP6Wfw1N1pjqpFaZQ7FAY6An6zxTzHs0BGqY/NQSnxSBD6WkDRTf3O0wG2Ztl/7jaQEnGNxZMdy2yET/B2xfGlDagQE1OgRRvL93UOHqhLnesPKqJ4NxLLn2unJgVka/HBpbiIARlHFq1n/cWlMZMne1ZfyD5M/Aa4BiyGSwP4Jl3UAAAAldEVYdGRhdGU6Y3JlYXRlADIwMjAtMDQtMDlUMTQ6MzQ6MTUrMDI6MDDBq8/nAAAAJXRFWHRkYXRlOm1vZGlmeQAyMDIwLTA0LTA5VDE0OjM0OjE1KzAyOjAwsPZ3WwAAAABJRU5ErkJggg==)](https://enix.io)

**kuik** (pronounced /kwɪk/, like "quick") is the shortname of **kube-image-keeper**, a container image routing, caching and monitoring system for Kubernetes developed by Enix. It helps make applications more highly available by ensuring reliable access to container images.

> [!NOTE]
> This is a complete ground-up rewrite of the project — introducing a new design, new architecture, and improved observability.

## 🧪 Status: Beta

> [!WARNING]
> v2 is currently in **Beta**. Expect breaking changes and limited functionality as we iterate.

Feedback is welcome – open an issue or start a discussion.

> [!CAUTION]
> Not recommended for production use yet.

Production environments should continue using the stable v1 release available in the same repository until v2 reaches general availability.

## 🤷 Why Version 2?

Even if we are _proud_ of what we achieved with the v1 of **kube-image-keeper**, it was too often painful to work with: it was hard to deploy, overly complex, and the image caching feature — while ambitious — introduced often too much issues. We missed our original goal: to make kube-image-keeper an **easy, no-brainer install for any cluster** which would help ops in their day to day work and **provide confidence**.

We learnt _a lot_ from this experience and with v2, **_we're starting fresh!_** Our focus is on **simplicity** and **observability** first. Caching is no longer the core feature — it will return later as an opt-in, second-class citizen. This reboot is about doing **_one thing well_**: giving clear visibility into what images are used, where, and how.

## ✨ What's New in v2

kuik v2 is a **complete rewrite** of the project with a focus on **simplicity** and **ease of use**.

### 🔍 Redesigned Architecture

- **Minimal default features**: core functionality enabled by default, others opt-in
- **Redesigned CRDs** for better clarity and extensibility
- **Image routing**: kuik can rewrite Pod images on-the-fly to point to an operational registry
- **Image replication**: kuik can manage copy between registries to create a virtual highly available registry
- **Image monitoring**: kuik can monitor image availability across various registries

## 🚧 Roadmap

Planned features (subject to change):

- Routing, Replication and Monitoring should be implemented and available as Beta in November
- Further refinement will occur by the end of the year (2025).
- General Availability should occur by December 2025 / January 2026.
- We expect to communicate the launch of the v2 at the [Cloud Native Days France 2026 convention](https://www.cloudnativedays.fr/)

## 📦 Installation

```bash
kubectl create namespace kuik-system
VERSION=2.0.0-beta.X # Latest available beta version
helm upgrade --install --namespace kuik-system kube-image-keeper oci://quay.io/enix/charts/kube-image-keeper:$VERSION
```

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| configuration.monitoring.enabled | bool | `true` |  |
| configuration.routing.activeCheck.enabled | bool | `true` |  |
| configuration.routing.activeCheck.timeout | string | `"1s"` |  |
| configuration.routing.strategies | list | `[]` |  |
| manager.affinity | object | `{}` | Affinity for the manager pod |
| manager.env | list | `[]` | Extra env variables for the manager pod |
| manager.image.pullPolicy | string | `"IfNotPresent"` | Manager image pull policy |
| manager.image.repository | string | `"quay.io/enix/kube-image-keeper"` | Manager image repository |
| manager.image.tag | string | `""` | Manager image tag. Default chart appVersion |
| manager.imagePullSecrets | list | `[]` | Specify secrets to be used when pulling manager image |
| manager.livenessProbe | object | `{"httpGet":{"path":"/healthz","port":8081}}` | Liveness probe definition for the manager pod |
| manager.nodeSelector | object | `{}` | Node selector for the manager pod |
| manager.pdb.create | bool | `false` | Create a PodDisruptionBudget for the manager pod |
| manager.pdb.maxUnavailable | string | `""` | Maximum unavailable pods |
| manager.pdb.minAvailable | int | `1` | Minimum available pods |
| manager.podAnnotations | object | `{}` | Annotations to add to the manager pod |
| manager.podSecurityContext | object | `{}` | Security context for the manager pod |
| manager.priorityClassName | string | `""` | Set the PriorityClassName for the manager pod |
| manager.readinessProbe | object | `{"httpGet":{"path":"/readyz","port":8081}}` | Readiness probe definition for the manager pod |
| manager.replicas | int | `1` | Number of manager pods |
| manager.resources.limits.cpu | string | `"1"` | Cpu limits for the manager pod |
| manager.resources.limits.memory | string | `"512Mi"` | Memory limits for the manager pod |
| manager.resources.requests.cpu | string | `"50m"` | Cpu requests for the manager pod |
| manager.resources.requests.memory | string | `"50Mi"` | Memory requests for the manager pod |
| manager.securityContext | object | `{}` | Security context for containers of the manager pod |
| manager.serviceMonitor.create | bool | `false` | Should a ServiceMonitor object be installed to scrape kuik manager metrics. For prometheus-operator (kube-prometheus) users. |
| manager.serviceMonitor.extraLabels | object | `{}` | Additional labels to add to ServiceMonitor objects |
| manager.serviceMonitor.relabelings | list | `[]` | Relabel config for the ServiceMonitor, see: https://coreos.com/operators/prometheus/docs/latest/api.html#relabelconfig |
| manager.serviceMonitor.scrapeInterval | string | `"60s"` | Target scrape interval set in the ServiceMonitor |
| manager.serviceMonitor.scrapeTimeout | string | `"30s"` | Target scrape timeout set in the ServiceMonitor |
| manager.tolerations | list | `[]` | Toleration for the manager pod |
| manager.verbosity | string | `"INFO"` | Manager logging verbosity |
| manager.webhook.certificateIssuerRef | object | `{"kind":"Issuer","name":"kube-image-keeper-selfsigned-issuer"}` | Issuer reference to issue the webhook certificate, ignored if createCertificateIssuer is true |
| manager.webhook.createCertificateIssuer | bool | `true` | If true, create the issuer used to issue the webhook certificate |
| metrics.imageLastMonitorAgeMinutesBuckets.custom | list | `[10,30,60,120,180,360,1440]` | List of buckets to create in minutes, ommiting +inf |
| metrics.imageLastMonitorAgeMinutesBuckets.exponential | object | `{"count":12,"factor":1.5,"start":15}` | Range from 15m to 1297m (21h) (start*factor^count) See https://pkg.go.dev/github.com/prometheus/client_golang/prometheus#ExponentialBuckets |
| metrics.imageLastMonitorAgeMinutesBuckets.exponential.start | int | `15` | Default is 15m |
| metrics.imageLastMonitorAgeMinutesBuckets.exponentialRange | object | `{"count":12,"max":1000,"min":15}` | See https://pkg.go.dev/github.com/prometheus/client_golang/prometheus#ExponentialBucketsRange |
| metrics.imageLastMonitorAgeMinutesBuckets.type | string | `"exponentialRange"` | Bucket generation method (can be one of exponential, exponentialRange or custom) |
| rbac.create | bool | `true` | Create the ClusterRole and ClusterRoleBinding. If false, need to associate permissions with serviceAccount outside this Helm chart. |
| registryMonitors.defaultSpec.interval | string | `"3h"` | Time window during which maxPerInterval limits the number of monitoring tasks. (e.g., 6h, 30m) |
| registryMonitors.defaultSpec.maxPerInterval | int | `25` | Maximum monitoring tasks to run in the given interval and for a given registry. |
| registryMonitors.defaultSpec.method | string | `"HEAD"` | HTTP method to use to monitor an image of this registry |
| registryMonitors.defaultSpec.parallel | int | `1` | Maximum monitoring tasks to run in parallel for a given registry. |
| registryMonitors.defaultSpec.timeout | string | `"30s"` | Maximum duration of a monitoring task |
| registryMonitors.items | object | `{"docker.io":{"interval":"1h","maxPerInterval":2},"public.ecr.aws":{"interval":"1h","maxPerInterval":2}}` | RegistryMonitors to create on install and upgrade, if name is not provided, defaults to registry value. |
| serviceAccount.annotations | object | `{}` | Annotations to add to the serviceAccount |
| serviceAccount.create | bool | `true` | Create the serviceAccount. If false, use serviceAccount with specified name (or "default" if false and name unset.) |
| serviceAccount.extraLabels | object | `{}` | Additional labels to add to the serviceAccount |
| serviceAccount.name | string | `""` | Name of the serviceAccount (auto-generated if unset and create is true) |
| unusedImageTTL | int | `24` | Delay in hours before deleting an Image that is not used by any pod |

## License

MIT License

Copyright (c) 2020-2025 Enix SAS

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

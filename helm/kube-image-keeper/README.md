# kube-image-keeper (kuik, pronounce [kwɪk]!)

[![License MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Brought by Enix](https://img.shields.io/badge/Brought%20to%20you%20by-ENIX-%23377dff?labelColor=888&logo=data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAA4AAAAOCAQAAAC1QeVaAAAABGdBTUEAALGPC/xhBQAAACBjSFJNAAB6JgAAgIQAAPoAAACA6AAAdTAAAOpgAAA6mAAAF3CculE8AAAAAmJLR0QA/4ePzL8AAAAHdElNRQfkBAkQIg/iouK/AAABZ0lEQVQY0yXBPU8TYQDA8f/zcu1RSDltKliD0BKNECYZmpjgIAOLiYtubn4EJxI/AImzg3E1+AGcYDIMJA7lxQQQQRAiSSFG2l457+655x4Gfz8B45zwipWJ8rPCQ0g3+p9Pj+AlHxHjnLHAbvPW2+GmLoBN+9/+vNlfGeU2Auokd8Y+VeYk/zk6O2fP9fcO8hGpN/TUbxpiUhJiEorTgy+6hUlU5N1flK+9oIJHiKNCkb5wMyOFw3V9o+zN69o0Exg6ePh4/GKr6s0H72Tc67YsdXbZ5gENNjmigaXbMj0tzEWrZNtqigva5NxjhFP6Wfw1N1pjqpFaZQ7FAY6An6zxTzHs0BGqY/NQSnxSBD6WkDRTf3O0wG2Ztl/7jaQEnGNxZMdy2yET/B2xfGlDagQE1OgRRvL93UOHqhLnesPKqJ4NxLLn2unJgVka/HBpbiIARlHFq1n/cWlMZMne1ZfyD5M/Aa4BiyGSwP4Jl3UAAAAldEVYdGRhdGU6Y3JlYXRlADIwMjAtMDQtMDlUMTQ6MzQ6MTUrMDI6MDDBq8/nAAAAJXRFWHRkYXRlOm1vZGlmeQAyMDIwLTA0LTA5VDE0OjM0OjE1KzAyOjAwsPZ3WwAAAABJRU5ErkJggg==)](https://enix.io)

kube-image-keeper (a.k.a. *kuik*) is a container image caching system designed for Kubernetes.
It ensures the availability of your favorite container images by keeping a local copy within your k8s cluster. 

This is useful in various situations:
- to avoid reaching your Docker Hub (or any other rate-limited registry) pull quota
- if the registry is unavailable for some reason
- if your critical image is no longer available in the registry (deleted by mistake, inappropriate retention policy...)

## Prerequisites

- Kubernetes cluster up & running with admin permissions
- Helm >= 3.2.0
- [Cert-manager](https://cert-manager.io/docs/installation/) installed
- CNI plugin with [port-mapper](https://www.cni.dev/plugins/current/meta/portmap/) enabled
- In a production environment, we definitely recommend you to use a persistent storage

## Supported Kubernetes versions

Tested from v1.21 to v1.24 but should works on latest versions.

## How it works

kuik is composed of 3 main components:

- A mutating webhook responsible to rewrite pod's image name on the fly.
- A controller watching pods, that create a custom resource `CachedImage`.
- A controller watching `CachedImage` custom resources and fetching images from source registry and storing them to the local one.

In addition, we deploy:

- A container [registry](https://docs.docker.com/registry/) to store downloaded images.
- A proxy deployed as a DaemonSet reponsible to pull images from either the local or the source registry.

![Architecture](https://raw.githubusercontent.com/enix/kube-image-keeper/main/docs/architecture.jpg)

When a pod is scheduled, the mutating webhook will rewrite and prefix the image name with `localhost:{port}/` where `port` is configurable.
The proxy hostPort setting allows the container runtime to pull images though it on localhost. The proxy will determine if the image should be retrieve either from the local or the source registry. 

## Installation

1. Customize your `values.yaml` to configure the chart.
1. Install the helm chart:

**From [enix/helm-charts](https://github.com/enix/helm-charts) repository:**

```bash
helm repo add enix https://charts.enix.io/
helm install --create-namespace --namespace kuik-system kube-image-keeper enix/kube-image-keeper
```

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| cachedImagesExpiryDelay | int | `30` |  |
| controllers.affinity | object | `{}` |  |
| controllers.image.pullPolicy | string | `"IfNotPresent"` |  |
| controllers.image.repository | string | `"enix/kube-image-keeper"` |  |
| controllers.image.tag | string | `""` |  |
| controllers.imagePullSecrets | list | `[]` |  |
| controllers.nodeSelector | object | `{}` |  |
| controllers.podAnnotations | object | `{}` |  |
| controllers.podSecurityContext | object | `{}` |  |
| controllers.replicas | int | `2` |  |
| controllers.resources | object | `{}` |  |
| controllers.securityContext | object | `{}` |  |
| controllers.tolerations | list | `[]` |  |
| controllers.verbosity | string | `"INFO"` |  |
| controllers.webhook.certificateIssuerRef.kind | string | `"Issuer"` |  |
| controllers.webhook.certificateIssuerRef.name | string | `"kuik-selfsigned-issuer"` |  |
| controllers.webhook.createCertificateIssuer | bool | `true` |  |
| controllers.webhook.ignoredNamespaces[0] | string | `"kube-system"` |  |
| controllers.webhook.objectSelector.matchExpressions | list | `[]` |  |
| installCRD | bool | `true` |  |
| proxy.affinity | object | `{}` |  |
| proxy.hostPort | int | `7439` |  |
| proxy.image.pullPolicy | string | `"IfNotPresent"` |  |
| proxy.image.repository | string | `"enix/kube-image-keeper"` |  |
| proxy.image.tag | string | `""` |  |
| proxy.imagePullSecrets | list | `[]` |  |
| proxy.nodeSelector | object | `{}` |  |
| proxy.podAnnotations | object | `{}` |  |
| proxy.podSecurityContext | object | `{}` |  |
| proxy.resources | object | `{}` |  |
| proxy.securityContext | object | `{}` |  |
| proxy.tolerations[0].effect | string | `"NoSchedule"` |  |
| proxy.tolerations[0].operator | string | `"Exists"` |  |
| proxy.tolerations[1].key | string | `"CriticalAddonsOnly"` |  |
| proxy.tolerations[1].operator | string | `"Exists"` |  |
| proxy.tolerations[2].effect | string | `"NoExecute"` |  |
| proxy.tolerations[2].operator | string | `"Exists"` |  |
| proxy.tolerations[3].effect | string | `"NoExecute"` |  |
| proxy.tolerations[3].key | string | `"node.kubernetes.io/not-ready"` |  |
| proxy.tolerations[3].operator | string | `"Exists"` |  |
| proxy.tolerations[4].effect | string | `"NoExecute"` |  |
| proxy.tolerations[4].key | string | `"node.kubernetes.io/unreachable"` |  |
| proxy.tolerations[4].operator | string | `"Exists"` |  |
| proxy.tolerations[5].effect | string | `"NoSchedule"` |  |
| proxy.tolerations[5].key | string | `"node.kubernetes.io/disk-pressure"` |  |
| proxy.tolerations[5].operator | string | `"Exists"` |  |
| proxy.tolerations[6].effect | string | `"NoSchedule"` |  |
| proxy.tolerations[6].key | string | `"node.kubernetes.io/memory-pressure"` |  |
| proxy.tolerations[6].operator | string | `"Exists"` |  |
| proxy.tolerations[7].effect | string | `"NoSchedule"` |  |
| proxy.tolerations[7].key | string | `"node.kubernetes.io/pid-pressure"` |  |
| proxy.tolerations[7].operator | string | `"Exists"` |  |
| proxy.tolerations[8].effect | string | `"NoSchedule"` |  |
| proxy.tolerations[8].key | string | `"node.kubernetes.io/unschedulable"` |  |
| proxy.tolerations[8].operator | string | `"Exists"` |  |
| proxy.tolerations[9].effect | string | `"NoSchedule"` |  |
| proxy.tolerations[9].key | string | `"node.kubernetes.io/network-unavailable"` |  |
| proxy.tolerations[9].operator | string | `"Exists"` |  |
| proxy.verbosity | int | `1` |  |
| psp.create | bool | `false` |  |
| registry.affinity | object | `{}` |  |
| registry.env | list | `[]` |  |
| registry.garbageCollectionSchedule | string | `"0 0 * * 0"` |  |
| registry.image.pullPolicy | string | `"IfNotPresent"` |  |
| registry.image.repository | string | `"registry"` |  |
| registry.image.tag | string | `"latest"` |  |
| registry.imagePullSecrets | list | `[]` |  |
| registry.nodeSelector | object | `{}` |  |
| registry.persistence.enabled | bool | `false` |  |
| registry.persistence.size | string | `"20Gi"` |  |
| registry.persistence.storageClass | string | `nil` |  |
| registry.podAnnotations | object | `{}` |  |
| registry.podSecurityContext | object | `{}` |  |
| registry.resources | object | `{}` |  |
| registry.securityContext | object | `{}` |  |
| registry.service.type | string | `"ClusterIP"` |  |
| registry.tolerations | list | `[]` |  |
| registryUI.affinity | object | `{}` |  |
| registryUI.auth.password | string | `""` |  |
| registryUI.auth.username | string | `"admin"` |  |
| registryUI.enabled | bool | `false` |  |
| registryUI.image.pullPolicy | string | `"IfNotPresent"` |  |
| registryUI.image.repository | string | `"parabuzzle/craneoperator"` |  |
| registryUI.image.tag | string | `"2.2.5"` |  |
| registryUI.imagePullSecrets | list | `[]` |  |
| registryUI.nodeSelector | object | `{}` |  |
| registryUI.podAnnotations | object | `{}` |  |
| registryUI.podSecurityContext | object | `{}` |  |
| registryUI.resources | object | `{}` |  |
| registryUI.securityContext | object | `{}` |  |
| registryUI.tolerations | list | `[]` |  |
| serviceAccount.annotations | object | `{}` |  |
| serviceAccount.name | string | `""` |  |

## Usage

### Pod filtering

There are 3 ways to filter pods from which images should be cached.

- The first and most basic way is to add the label `kube-image-keeper.enix.io/image-caching-policy: ignore` on pods that should be ignored.
- The second way is to define the value `controllers.webhook.objectSelector.matchExpressions` in helm `values.yaml` configuration file.
- Last, you can ignore all pods scheduled in a specific namespace using the directive `controllers.webhook.ignoredNamespaces` (This feature needs [NamespaceDefaultLabelName](https://kubernetes.io/docs/concepts/services-networking/network-policies/#targeting-a-namespace-by-its-name) feature gate enabled to work).

Those parameters are used by the `MutatingWebhookConfiguration` to filter pods that needs to be updated. Once images from those pods are rewritten, a label will be added to them so the Pod controller will create CachedImages custom resources. The CachedImages controller will then cache those images.

### Cache persistance & garbage collecting

Persistance is disabled by default. It requires a CSI plugin to be installed on the cluster to be enabled. It is then configured through the `values.yaml` helm release configuration file in `registry.persistance`.

When a CachedImage expires because it is not used anymore by the cluster, the image is deleted from the registry. But it only delete **reference files** like tags, not blobs that accounts for the most storage usage. [Garbage collection](https://docs.docker.com/registry/garbage-collection/) allows to remove those blobs and free space. The garbage collecting job can be configured to run thanks to the `registry.garbageCollectionSchedule` configuration in a cron-like format. It is disabled by default as running garbage collection without persistency configured would just empty the cache registry as described in the below section.

## ⚠️  Limitations

Garbage collection can only run when the registry is read-only or not running at all to prevent corrupted images as described in the [documentation](https://docs.docker.com/registry/garbage-collection/). Thus, when the garbage collection job runs, it first stops any running instance of the cache registry before doing garbage collection. During this period of time, all pulls are proxified to the source registry so operation can continue smoothly.

Be careful, running garbage collection while not having persistance configured would simply empty the cache registry since its pod is destroyed during the operation and it is thus not recommanded for production setups.

## License

```
Copyright (c) 2020 Enix SAS

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
```

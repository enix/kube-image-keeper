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
| cachedImagesExpiryDelay | int | `30` | Delay in days before deleting an unused CachedImage |
| controllers.affinity | object | `{}` | Affinity for the controller pod |
| controllers.image.pullPolicy | string | `"IfNotPresent"` | Controller image pull policy |
| controllers.image.repository | string | `"enix/kube-image-keeper"` | Controller image repository |
| controllers.image.tag | string | `""` | Controller image tag. Default chart appVersion |
| controllers.imagePullSecrets | list | `[]` | Specify secrets to be used when pulling controller image |
| controllers.nodeSelector | object | `{}` | Node selector for the controller pod |
| controllers.podAnnotations | object | `{}` | Annotations to add to the controller pod |
| controllers.podSecurityContext | object | `{}` | Security context for the controller pod |
| controllers.replicas | int | `2` | Number of controllers |
| controllers.resources.limits.cpu | string | `"1"` | Cpu limits for the controller pod |
| controllers.resources.limits.memory | string | `"512Mi"` | Memory limits for the controller pod |
| controllers.resources.requests.cpu | string | `"50m"` | Cpu requests for the controller pod |
| controllers.resources.requests.memory | string | `"50Mi"` | Memory requests for the controller pod |
| controllers.securityContext | object | `{}` | Security context for containers of the controller pod |
| controllers.tolerations | list | `[]` | Toleration for the controller pod |
| controllers.verbosity | string | `"INFO"` | Controller logging verbosity |
| controllers.webhook.certificateIssuerRef | object | `{"kind":"Issuer","name":"kuik-selfsigned-issuer"}` | Issuer reference to issue the webhook certificate |
| controllers.webhook.createCertificateIssuer | bool | `true` | If true, create the issuer used to issue the webhook certificate  |
| controllers.webhook.ignoredNamespaces | list | `["kube-system"]` | Don't enable image caching for pods scheduled into these namespaces |
| controllers.webhook.objectSelector.matchExpressions | list | `[]` | Run the webhook if the object has matching labels. (See https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.21/#labelselectorrequirement-v1-meta)  |
| installCRD | bool | `true` | If true, install the CRD |
| proxy.affinity | object | `{}` | Affinity for the proxy pod |
| proxy.hostPort | int | `7439` | hostPort used for the proxy pod  |
| proxy.image.pullPolicy | string | `"IfNotPresent"` | Proxy image pull policy |
| proxy.image.repository | string | `"enix/kube-image-keeper"` | Proxy image repository |
| proxy.image.tag | string | `""` | Proxy image tag. Default chart appVersion |
| proxy.imagePullSecrets | list | `[]` | Specify secrets to be used when pulling proxy image |
| proxy.nodeSelector | object | `{}` | Node selector for the proxy pod |
| proxy.podAnnotations | object | `{}` | Annotations to add to the proxy pod |
| proxy.podSecurityContext | object | `{}` | Security context for the proxy pod |
| proxy.resources.limits.cpu | string | `"1"` | Cpu limits for the proxy pod |
| proxy.resources.limits.memory | string | `"512Mi"` | Memory limits for the proxy pod |
| proxy.resources.requests.cpu | string | `"50m"` | Cpu requests for the proxy pod |
| proxy.resources.requests.memory | string | `"50Mi"` | Memory requests for the proxy pod |
| proxy.securityContext | object | `{}` | Security context for containers of the proxy pod |
| proxy.tolerations | list | `[{"effect":"NoSchedule","operator":"Exists"},{"key":"CriticalAddonsOnly","operator":"Exists"},{"effect":"NoExecute","operator":"Exists"},{"effect":"NoExecute","key":"node.kubernetes.io/not-ready","operator":"Exists"},{"effect":"NoExecute","key":"node.kubernetes.io/unreachable","operator":"Exists"},{"effect":"NoSchedule","key":"node.kubernetes.io/disk-pressure","operator":"Exists"},{"effect":"NoSchedule","key":"node.kubernetes.io/memory-pressure","operator":"Exists"},{"effect":"NoSchedule","key":"node.kubernetes.io/pid-pressure","operator":"Exists"},{"effect":"NoSchedule","key":"node.kubernetes.io/unschedulable","operator":"Exists"},{"effect":"NoSchedule","key":"node.kubernetes.io/network-unavailable","operator":"Exists"}]` | Toleration for the proxy pod |
| proxy.verbosity | int | `1` | Verbosity level for the proxy pod |
| psp.create | bool | `false` | If True, create the PodSecurityPolicy |
| registry.affinity | object | `{}` | Affinity for the proxy pod |
| registry.env | list | `[]` | Extra env variables for the registry pod |
| registry.garbageCollectionSchedule | string | `"0 0 * * 0"` | Garbage collector cron schedule. Use standard crontab format. |
| registry.image.pullPolicy | string | `"IfNotPresent"` | Registry image pull policy |
| registry.image.repository | string | `"registry"` | Registry image repository |
| registry.image.tag | string | `"2.8.1"` | Registry image tag |
| registry.imagePullSecrets | list | `[]` | Specify secrets to be used when pulling proxy image |
| registry.nodeSelector | object | `{}` | Node selector for the proxy pod |
| registry.persistence.enabled | bool | `false` | If true, enable persitent storage |
| registry.persistence.size | string | `"20Gi"` | Registry persistent volume size |
| registry.persistence.storageClass | string | `nil` | StorageClass for persistent volume |
| registry.podAnnotations | object | `{}` | Annotations to add to the proxy pod |
| registry.podSecurityContext | object | `{}` | Security context for the proxy pod |
| registry.resources.limits.cpu | string | `"1"` | Cpu limits for the registry pod |
| registry.resources.limits.memory | string | `"1Gi"` | Memory limits for the registry pod |
| registry.resources.requests.cpu | string | `"50m"` | Cpu requests for the registry pod |
| registry.resources.requests.memory | string | `"256Mi"` | Memory requests for the registry pod |
| registry.securityContext | object | `{}` | Security context for containers of the proxy pod |
| registry.service.type | string | `"ClusterIP"` | Registry service type |
| registry.tolerations | list | `[]` | Toleration for the proxy pod |
| registryUI.affinity | object | `{}` | Affinity for the registry UI pod |
| registryUI.auth.password | string | `""` | Registry UI password |
| registryUI.auth.username | string | `"admin"` | Registry UI username |
| registryUI.enabled | bool | `false` | If true, enable the registry user interface |
| registryUI.image.pullPolicy | string | `"IfNotPresent"` | Registry UI image pull policy |
| registryUI.image.repository | string | `"parabuzzle/craneoperator"` | Registry UI image repository |
| registryUI.image.tag | string | `"2.2.5"` | Registry UI image tag |
| registryUI.imagePullSecrets | list | `[]` | Specify secrets to be used when pulling registry UI image |
| registryUI.nodeSelector | object | `{}` | Node selector for the registry UI pod |
| registryUI.podAnnotations | object | `{}` | Annotations to add to the registry UI pod |
| registryUI.podSecurityContext | object | `{}` | Security context for the registry UI pod |
| registryUI.resources | object | `{}` | CPU / Memory resources requests / limits for the registry UI pod |
| registryUI.securityContext | object | `{}` | Security context for containers of the registry UI pod |
| registryUI.tolerations | list | `[]` | Toleration for the registry UI pod |
| serviceAccount.annotations | object | `{}` | Annotations to add to the servicateAccount |
| serviceAccount.name | string | `""` | Name of the serviceAccount |

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

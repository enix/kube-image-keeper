# kube-image-keeper (KuiK, pronounce [kwɪk]!)

[![License MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Brought by Enix](https://img.shields.io/badge/Brought%20to%20you%20by-ENIX-%23377dff?labelColor=888&logo=data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAA4AAAAOCAQAAAC1QeVaAAAABGdBTUEAALGPC/xhBQAAACBjSFJNAAB6JgAAgIQAAPoAAACA6AAAdTAAAOpgAAA6mAAAF3CculE8AAAAAmJLR0QA/4ePzL8AAAAHdElNRQfkBAkQIg/iouK/AAABZ0lEQVQY0yXBPU8TYQDA8f/zcu1RSDltKliD0BKNECYZmpjgIAOLiYtubn4EJxI/AImzg3E1+AGcYDIMJA7lxQQQQRAiSSFG2l457+655x4Gfz8B45zwipWJ8rPCQ0g3+p9Pj+AlHxHjnLHAbvPW2+GmLoBN+9/+vNlfGeU2Auokd8Y+VeYk/zk6O2fP9fcO8hGpN/TUbxpiUhJiEorTgy+6hUlU5N1flK+9oIJHiKNCkb5wMyOFw3V9o+zN69o0Exg6ePh4/GKr6s0H72Tc67YsdXbZ5gENNjmigaXbMj0tzEWrZNtqigva5NxjhFP6Wfw1N1pjqpFaZQ7FAY6An6zxTzHs0BGqY/NQSnxSBD6WkDRTf3O0wG2Ztl/7jaQEnGNxZMdy2yET/B2xfGlDagQE1OgRRvL93UOHqhLnesPKqJ4NxLLn2unJgVka/HBpbiIARlHFq1n/cWlMZMne1ZfyD5M/Aa4BiyGSwP4Jl3UAAAAldEVYdGRhdGU6Y3JlYXRlADIwMjAtMDQtMDlUMTQ6MzQ6MTUrMDI6MDDBq8/nAAAAJXRFWHRkYXRlOm1vZGlmeQAyMDIwLTA0LTA5VDE0OjM0OjE1KzAyOjAwsPZ3WwAAAABJRU5ErkJggg==)](https://enix.io)

## Installation

Cert-manager is used to issue TLS certificate for the mutating webhook. It is thus required to install it first.

1. [Install](https://cert-manager.io/docs/installation/) cert-manager.
1. Create a `values.yaml` file to configure the chart.
1. Install the helm chart following one of the two below methods:

**From source:**

```bash
helm install --create-namespace --namespace kuik-system kube-image-keeper --values=./values.yaml ./helm/kube-image-keeper/
```

**From [enix/helm-charts](https://github.com/enix/helm-charts) repository:**

```bash
helm repo add enix https://charts.enix.io/
helm search repo enix
helm install --create-namespace --namespace kuik-system kube-image-keeper --values=./values.yaml enix/kube-image-keeper
```

## Pod filtering

There are 2 ways to filter pods from which images should be cached.

- The first and most basic way is to add the label `kube-image-keeper.enix.io/image-caching-policy: ignore` on pods that should be ignored.
- The second way is to define the value `controllers.webhook.objectSelector.matchExpressions` in helm `values.yaml` configuration file.

Those parameters are used by the `MutatingWebhookConfiguration` to filter pods that needs to be updated. Once images from those pods are rewritten, a label will be added to them so the Pod controller will create CachedImages custom resources. The CachedImages controller will then cache those images.

## Cache persistance & garbage collecting

Persistance is disabled by default. It requires a CSI plugin to be installed on the cluster to be enabled. It is then configured through the `values.yaml` helm release configuration file in `registry.persistance`.

When a CachedImage expires because it is not used anymore by the cluster, the image is deleted from the registry. But it only delete **reference files** like tags, not blobs that accounts for the most storage usage. [Garbage collection](https://docs.docker.com/registry/garbage-collection/) allows to remove those blobs and free space. The garbage collecting job can be configured to run thanks to the `registry.garbageCollectionSchedule` configuration in a cron-like format. It is disabled by default as running garbage collection without persistency configured would just empty the cache registry as described in the below section.

### ⚠️ Limitations

Garbage collection can only run when the registry is read-only or not running at all to prevent corrupted images as described in the [documentation](https://docs.docker.com/registry/garbage-collection/). Thus, when the garbage collection job runs, it first stops any running instance of the cache registry before doing garbage collection. During this period of time, all pulls are proxified to the upstream registry so operation can continue smoothly.

Be careful, running garbage collection while not having persistance configured would simply empty the cache registry since its pod is destroyed during the operation and it is thus not recommanded for production setups.

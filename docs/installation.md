---
sidebar:
  order: 1
---

# Installation

We rely on [cert-manager Custom Resources](https://github.com/enix/kube-image-keeper/tree/v2.2.3/helm/kube-image-keeper/templates/webhook-certificate.yaml) to manage the kuik mutating webhook certificate, so you need to [install it first](https://cert-manager.io/docs/installation/).

```bash
VERSION=2.2.3
helm upgrade --install --create-namespace --namespace kuik-system kube-image-keeper oci://quay.io/enix/charts/kube-image-keeper:$VERSION
```

<!-- HELM_DOCS_END -->

Custom Resource Definitions (CRDs) are used to configure the behavior of kuik such as its routing and mirroring features. Those are described in the [CRD reference](./crds.md).

To setup an [*ImageSetMirror* (or a *ClusterImageSetMirror*)](./crds.md#clusterimagesetmirror), you will first need to configure a registry where kuik will copy matched images. Then generate a token with permission to pull, push and delete (if cleanup enabled) in this registry and create the secret to use in your *ImageSetMirror* with:

```bash
kubectl create secret docker-registry my-registry-secret --docker-server=my-registry.company.com --docker-username=my-username --docker-password=my-token
```

If you let kuik cleanup expired images in your registry, you still have to configure garbage collection on your own as kuik only delete images reference.

## Hybrid Linux/Windows clusters

kuik is shipped as a Linux-only image, so the chart restricts the manager to Linux nodes by default:

```yaml
manager:
  nodeSelector:
    kubernetes.io/os: linux
```

Without this constraint, the scheduler is free to place the manager on a Windows node of a hybrid cluster, where it fails with `ImagePullBackOff`.

Helm merges maps, so adding your own keys keeps the constraint:

```yaml
manager:
  nodeSelector:
    node.kubernetes.io/instance-type: m5.large # scheduled on Linux m5.large nodes
```

Should you need to lift it entirely, set the key to `null`:

```yaml
manager:
  nodeSelector:
    kubernetes.io/os: null
```

> [!NOTE]
> No default `tolerations` are set: since the manager never lands on a Windows node, taints commonly applied to those nodes (such as `os=windows:NoSchedule`) are irrelevant. Add `manager.tolerations` if your Linux nodes carry taints of their own.

Routing and mirroring themselves are OS-agnostic: kuik rewrites images for Windows pods like any other, and the default [`mirroring.platforms`](./configuration.md#mirroringplatforms) (`[{architecture: amd64}]`, with `os` unset) matches any operating system, so `windows/amd64` manifests are mirrored out of the box.

Beware that setting `mirroring.platforms` **replaces** that default. Restricting it to Linux breaks the mirroring of Windows images: the copy fails with a no-matching-platform error, and pods fall back to their original image. On a cluster running both, list both:

```yaml
configuration:
  mirroring:
    platforms:
      - os: linux
        architecture: amd64
      - os: windows
        architecture: amd64
```

---
sidebar:
  order: 1
---

# Installation

We rely on [cert-manager Custom Resources](./helm/kube-image-keeper/templates/webhook-certificate.yaml) to manage the kuik mutating webhook certificate, so you need to [install it first](https://cert-manager.io/docs/installation/).

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

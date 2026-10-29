---
sidebar:
  order: 1
---

# Installation

kuik requires Kubernetes 1.30 or later: the chart installs a `ValidatingAdmissionPolicy`, GA from that version.

A cluster runs one kuik install: the Secret names its secret syncer reserves are cluster-wide, so the syncers of two installs would refuse each other's writes.

We rely on [cert-manager Custom Resources](https://github.com/enix/kube-image-keeper/blob/v3.0.0-beta.1/helm/kube-image-keeper/templates/webhook.yaml) to manage the kuik mutating webhook certificate, so you need to [install it first](https://cert-manager.io/docs/installation/).

kuik v3 is in beta: try it on a test or staging cluster, not in production, and read its [known limits](./known-limits.md) first. Pick the latest version from the [releases](https://github.com/enix/kube-image-keeper/releases).

```bash
VERSION=3.0.0-beta.1
helm upgrade --install --create-namespace --namespace kuik-system kube-image-keeper oci://quay.io/enix/charts/kube-image-keeper:$VERSION
```

To install kuik in another namespace, replace `kuik-system` in this command and in the registry Secret command below.

Helm installs the CRDs of the chart's `crds/` directory on the first install only: `helm upgrade` leaves them as they are. When you upgrade kuik, apply the CRDs of the new version first:

```bash
VERSION=3.0.0-beta.1 # the version you upgrade to
helm show crds oci://quay.io/enix/charts/kube-image-keeper --version $VERSION | kubectl apply --server-side -f -
```

With Flux, set `install.crds` and `upgrade.crds` to `CreateReplace` on the HelmRelease instead.

<!-- HELM_DOCS_END -->

Custom Resource Definitions (CRDs) are used to configure the behavior of kuik such as its routing and mirroring features. Those are described in the [CRD reference](./crds.md).

To setup an [*ImageMirror*](./crds.md#imagemirror), you will first need to configure a registry where kuik will copy matched images. Then generate a token with permission to pull, push and delete (if cleanup enabled) in this registry and create the secret to use in your *ImageMirror* with:

```bash
kubectl create secret docker-registry my-registry-secret --namespace kuik-system --docker-server=my-registry.company.com --docker-username=my-username --docker-password=my-token
```

If you let kuik cleanup expired images in your registry, you still have to configure garbage collection on your own as kuik only delete images reference.

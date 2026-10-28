---
description: Install kuik on a test cluster, list the images your cluster runs, and watch kuik route a pod around a broken image, in 10 minutes.
sidebar:
  order: 2
---

# Getting started

In 10 minutes, on a test cluster, you will install kuik, see which images your cluster runs and whether they can still be pulled, then watch kuik save a pod whose image cannot be pulled. Nothing here changes your existing workloads.

## What you need

- A Kubernetes cluster, 1.30 or later, that you can break: a [Kind](https://kind.sigs.k8s.io/) cluster is enough (`kind create cluster`)
- `kubectl` and `helm`
- Nodes and pods that can reach `quay.io`: kuik's webhook checks the images itself before the node pulls them

This tutorial needs nothing else. For your own use cases, kuik relies on what you provide:

- for an `ImageMirror`, a registry kuik copies images to, which accepts nested repository paths, see [Destination requirements](./crds.md#destination-requirements)
- registries whose certificates your nodes trust: kuik's `tls` and `insecure` settings apply to kuik only, the nodes' container runtime pulls on its own, see [Registry TLS](./configuration.md#registry-tls)
- the credentials of your private registries, as Kubernetes Secrets

## 1. Install kuik

kuik's webhook gets its certificate from [cert-manager](https://cert-manager.io). Install it first, if your cluster does not run it yet. The command below takes the latest cert-manager, which may need a more recent Kubernetes than kuik does: on an older cluster, add `--version` with a release its [supported releases](https://cert-manager.io/docs/releases/) table lists for your Kubernetes version.

```bash
helm install cert-manager oci://quay.io/jetstack/charts/cert-manager \
  --namespace cert-manager --create-namespace --set crds.enabled=true --wait
```

Then install kuik:

```bash
VERSION=3.0.0-beta.1
helm upgrade --install --create-namespace --namespace kuik-system \
  kube-image-keeper oci://quay.io/enix/charts/kube-image-keeper:$VERSION --wait
```

kuik runs 3 processes: the webhook, which rewrites pods when they are created, the reconciler, which copies and checks images in the background, and the secret syncer, which writes the pull Secrets the kubelet needs into the pods' namespaces. The chart runs 2 replicas of each; the reconciler and the secret syncer keep one on standby. All 6 pods should be running:

```bash
kubectl get pods -n kuik-system
# NAME                                              READY   STATUS    RESTARTS   AGE
# kube-image-keeper-reconciler-6c9f7d8b5d-x2k8p     1/1     Running   0          40s
# kube-image-keeper-reconciler-6c9f7d8b5d-r7m4c     1/1     Running   0          40s
# kube-image-keeper-secret-syncer-7b6d9c4f8-q4n7z   1/1     Running   0          40s
# kube-image-keeper-secret-syncer-7b6d9c4f8-w9d2k   1/1     Running   0          40s
# kube-image-keeper-webhook-5f8c6b7d9-h8l2m         1/1     Running   0          40s
# kube-image-keeper-webhook-5f8c6b7d9-t6b5n         1/1     Running   0          40s
```

The full installation guide, upgrades included, is in [Installation](./installation.md).

## 2. See which images your cluster runs

An [`ImageMonitor`](./crds.md#imagemonitor) checks, in the background, that the images of the pods it selects can still be pulled from their registry. It only reads: it never changes a pod. Create one over the whole cluster:

```bash
kubectl apply -f - <<EOF
apiVersion: kuik.enix.io/v1alpha1
kind: ImageMonitor
metadata:
  name: cluster-images
spec: {}
EOF
```

kuik checks one image per registry per minute, so as not to exhaust any rate limit: give it a few minutes. Then:

```bash
kubectl get imagemonitors
# NAME             TRACKED   UNAVAILABLE   DRIFTED   READY   AGE
# cluster-images   13        0             0         True    4m
```

`TRACKED` is the number of distinct images your pods run, `UNAVAILABLE` those that can no longer be pulled. `UNAVAILABLE 0` means nothing failed among the images checked so far: on a real cluster, wait for a full round on each registry (`status.checks.registries[].cycleDuration`) before relying on it. That list is what you need before a maintenance or an upgrade: see [Detect missing images before an outage](./use-cases/detect-missing-images-before-outage.md).

## 3. Route a pod around a broken image

Say a pod uses `kuik-demo.invalid/nginx/nginx-unprivileged`, a registry that does not exist (`.invalid` never resolves), and the same image is published on `quay.io`. Without kuik, the pod stays in `ImagePullBackOff`.

Create a namespace for the demo:

```bash
kubectl create namespace kuik-demo
```

Declare that the two repositories hold the same images, for the pods of that namespace only, with an [`ImageAlternative`](./crds.md#imagealternative). Under `rewritePolicy: OnFailure`, kuik keeps the original image whenever it answers, and uses the alternative only when it does not:

```bash
kubectl apply -f - <<EOF
apiVersion: kuik.enix.io/v1alpha1
kind: ImageAlternative
metadata:
  name: kuik-demo
spec:
  namespaceSelector:
    matchLabels:
      kubernetes.io/metadata.name: kuik-demo
  rewritePolicy: OnFailure
  alternatives:
  - repository: kuik-demo.invalid/nginx/nginx-unprivileged
  - repository: quay.io/nginx/nginx-unprivileged
EOF
```

Now create a pod with the broken image:

```bash
kubectl run nginx --namespace kuik-demo --image=kuik-demo.invalid/nginx/nginx-unprivileged:1.31.6-alpine
```

The pod runs. When it was created, kuik checked the original image, found it unreachable, checked the alternative, and rewrote the pod to it:

```bash
kubectl get pod nginx --namespace kuik-demo -o jsonpath='{.spec.containers[0].image}'
# quay.io/nginx/nginx-unprivileged:1.31.6-alpine
```

kuik records its decision on the pod, with the original image. The reconciler then tells why in an event, and updates the `ImageAlternative` status, a few seconds later:

```bash
kubectl get pod nginx --namespace kuik-demo -o jsonpath='{.metadata.annotations.kuik\.enix\.io/rewrites}'
# {"nginx":{"by":"ImageAlternative/kuik-demo","origin":"kuik-demo.invalid/nginx/nginx-unprivileged:1.31.6-alpine","rewrittenTo":"quay.io/nginx/nginx-unprivileged:1.31.6-alpine","policy":"OnFailure"}}
kubectl get events --namespace kuik-demo --field-selector reason=ImageFallback
```

The `ImageAlternative` reports that one of its fallbacks is in use. If `FALLBACK` is still empty and `REWRITTEN` still `0`, wait a few seconds:

```bash
kubectl get imagealternatives
# NAME        POLICY      READY   FALLBACK   REWRITTEN   AGE
# kuik-demo   OnFailure   True    True       1           1m
```

After a minute or so, the monitor of step 2 lists the broken origin too, with the reason `Unreachable`:

```bash
kubectl get imagemonitor cluster-images -o jsonpath='{.status.unavailableImages}'
# [{"pods":1,"reason":"Unreachable","ref":"kuik-demo.invalid/nginx/nginx-unprivileged:1.31.6-alpine","since":"..."}]
```

## 4. Clean up

```bash
kubectl delete namespace kuik-demo
kubectl delete imagealternative kuik-demo
kubectl delete imagemonitor cluster-images
```

To remove kuik itself from a test cluster, the simplest is to delete the cluster: `kind delete cluster`. Otherwise, `helm uninstall` leaves the 3 CRDs, the ConfigMap `kuik-cluster-id` and the namespace `kuik-system`. Remove them too:

> [!WARNING]
> Deleting the CRDs deletes every `ImageAlternative`, `ImageMirror` and `ImageMonitor` of the cluster. Run these commands only on a cluster where nothing else uses kuik.

```bash
helm uninstall kube-image-keeper --namespace kuik-system
kubectl delete namespace kuik-system
kubectl delete crd imagealternatives.kuik.enix.io imagemirrors.kuik.enix.io imagemonitors.kuik.enix.io
```

Deleting `kuik-cluster-id` changes the cluster ID, which matters only on a cluster whose mirrors you keep, see [Cluster ID](./configuration.md#cluster-id).

## Next steps

You have used 2 of kuik's 3 resources. Pick the use case that matches your need:

- [Detect missing images before an outage](./use-cases/detect-missing-images-before-outage.md), with alerts
- [Overcome public registry limitations](./use-cases/overcome-public-registry-limitations.md): rate limits, outages, moved images
- [Automatically route images to a proxy cache registry](./use-cases/automatically-route-images-to-a-proxy-cache-registry.md)
- [Pull images faster from a local registry](./use-cases/better-performance-with-local-registry.md), with an `ImageMirror`
- [Protect images from garbage collection](./use-cases/protect-images-from-garbage-collection.md)

When something does not behave as expected, see [Troubleshooting](./guides/troubleshooting.md).

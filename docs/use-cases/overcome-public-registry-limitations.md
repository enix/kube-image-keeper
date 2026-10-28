---
description: Route around rate limits and outages by replicating or mirroring the images you depend on.
sidebar:
  order: 2
---

# Overcome public registry limitations

When a public registry rate-limits your nodes, goes down, or no longer serves an image, kuik sends new pods to another registry that holds the same image: one where the publisher also pushes it, or a registry of yours where kuik copied it.

## Best suited for

- Your nodes hit a pull rate limit (Docker Hub, public ECR...), often because they share one egress IP
- Your upstream registry has outages, or is gone for good
- A publisher moved its images, and your manifests or third-party charts still name the old location
- Your images are already pushed to several registries, or you let kuik copy them to yours with an [ImageMirror](../crds.md#imagemirror)

## Benefits

Your cluster **seamlessly** pulls from another registry, with no change to your workloads, and only when it needs to.

kuik's background reads keep their share of the quota small: they read each registry at the pace you configure, and one request serves every monitor and mirror tracking the same image. See [Registry pacing](../concepts/pacing.md). The webhook's check when a pod is created is not paced: it is one manifest `HEAD` per candidate, cached for 10 seconds by default ([`webhook.availabilityCheck.activeCheckCache.ttl`](../configuration.md#keys)).

## Pick the right resource

| Your situation | Resource | Policy |
| -------------- | -------- | ------ |
| The image is published on several registries, the main one fails sometimes | [`ImageAlternative`](#the-image-is-published-on-several-registries) | `OnFailure` |
| The image is published on several registries, the main one rate-limits you | [`ImageAlternative`](#the-image-is-published-on-several-registries) | `Always` |
| The image exists on one public registry only, which fails sometimes | [`ImageMirror`](#the-image-exists-on-one-registry-only) | `OnFailure` |
| The image exists on one public registry only, which rate-limits you | [`ImageMirror`](#the-image-exists-on-one-registry-only) | `Always` |
| The image moved, or its registry is gone | [`ImageAlternative`](#the-image-moved-or-its-registry-is-gone) with `unavailable: true` | `OnFailure` |

In every case, kuik's webhook checks each candidate when a pod is created (a manifest `HEAD`, 2 seconds at most by default, see [`webhook.availabilityCheck.timeout`](../configuration.md#keys)) and rewrites the image to the first one that answers. It only changes pods at creation: running pods keep their image.

## The image is published on several registries

Many projects push the same image to more than one registry. `nginx-unprivileged`, for instance, is on Docker Hub as `nginxinc/nginx-unprivileged` and on Quay as `nginx/nginx-unprivileged`. Declare them equivalent with an [`ImageAlternative`](../crds.md#imagealternative):

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ImageAlternative
metadata:
  name: nginx-unprivileged
spec:
  rewritePolicy: OnFailure
  alternatives:
  - repository: docker.io/nginxinc/nginx-unprivileged
  - repository: quay.io/nginx/nginx-unprivileged
```

A pod running `nginxinc/nginx-unprivileged:1.27` pulls from Docker Hub as before. When Docker Hub does not answer, or no longer holds the tag, kuik rewrites it to `quay.io/nginx/nginx-unprivileged:1.27`, emits an `ImageFallback` event on the pod, and the `ImageAlternative` turns its `FallbackActive` condition to `True`.

With `rewritePolicy: Always`, the other registries come first, and Docker Hub is only the fallback: use it to keep your pulls off a rate-limited registry altogether. `OnFailure` falls back only once the quota is spent: kuik's check reads the `RateLimit-Remaining` header Docker Hub returns, and treats a spent quota as a failure.

`repositoryGroup` covers a whole organization in one entry, every repository under it, with the rest of the path carried over:

```yaml
  alternatives:
  - repositoryGroup: docker.io/grafana
  - repositoryGroup: mirror.example.com/grafana
```

Every entry of a resource is treated as holding the same images as the others, in both directions. Check that every image you route actually exists at the alternative: a candidate that does not hold the tag answers not found, and the next one is tried.

## The image exists on one registry only

Let kuik copy the images your pods use to a registry of yours with an [`ImageMirror`](../crds.md#imagemirror), and fall back to the copy when the public registry fails:

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ImageMirror
metadata:
  name: public-images
spec:
  rewritePolicy: OnFailure
  destination:
    path: registry.example.com/kuik
    manage:
      auth:
        secretRef:
          name: mirror-push    # push, read, list and delete tags
    pull:
      auth:
        secretRef:
          name: mirror-pull    # what the kubelet pulls the copies with
```

kuik copies each image once a pod uses it, at the copy pace of its registry, so the copy is usually there before the public registry fails. With `rewritePolicy: Always`, new pods pull the copy and no longer touch the public registry at all: that is the end of rate limits on the nodes. The full setup (credentials, copy pace, checks) is in [Pull images faster from a local registry](./better-performance-with-local-registry.md).

## The image moved, or its registry is gone

When a publisher moves its images, or a registry shuts down, mark the old location `unavailable`. Its images are still matched, so pods naming them are routed, but kuik never offers it as a candidate and tries the original image last:

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ImageAlternative
metadata:
  name: acme-moved
spec:
  alternatives:
  - repositoryGroup: registry.acme.example/charts
    unavailable: true
  - repositoryGroup: ghcr.io/acme
```

A pod running `registry.acme.example/charts/api:3.2` is rewritten to `ghcr.io/acme/api:3.2`, with no request to the old registry first. Keep the resource until your manifests name the new location.

## Check it works

```bash
kubectl get imagealternatives
# NAME                 POLICY      READY   FALLBACK   REWRITTEN   AGE
# nginx-unprivileged   OnFailure   True    True       4           12d
kubectl get imagealternative nginx-unprivileged -o jsonpath='{.status.activeFallbacks}'
```

`status.activeFallbacks` lists the images currently served by a fallback, and how many pods run them. Each rewritten pod records its original image in the `kuik.enix.io/rewrites` annotation, see [What the webhook records on a pod](../crds.md#what-the-webhook-records-on-a-pod). Alert on [`kuik_fallback_active_pods`](../observability.md#anomalies) to know when a fallback is in use, and on `kuik_alternatives_exhausted_pods` when no candidate could serve an image.

## Good to know

- To know before a pod needs it that an alternative no longer serves an image, add an [`ImageMonitor`](./detect-missing-images-before-outage.md) with `monitorAlternatives: true`.
- An alternative is trusted to hold the same images: kuik does not compare their content. Pin images by digest where it matters: the digest is carried over, and a registry that does not hold it answers not found.
- A private alternative takes an `auth`, which kuik also copies as a pull secret into the pods' namespaces, see [`auth`](../crds.md#auth).

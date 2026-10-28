---
description: Rewrite Pod images on the fly to a proxy cache registry, with no spec changes to your workloads.
---

# Automatically route images to a proxy cache registry

kuik rewrites the image of every new pod to your proxy cache registry (Harbor, GitLab dependency proxy, Nexus, a `registry:2` pull-through cache...), without changing any Deployment, StatefulSet or Helm chart. When the proxy does not answer, the pod keeps its original image.

## Best suited for

- You already run a proxy cache registry, but your workloads still pull from Docker Hub, Quay or GHCR
- You do not want to edit the image of every workload, nor every third-party Helm chart, to go through the proxy

## Benefits

- Every pull goes through the proxy: fewer Docker Hub rate limits, less egress, faster pulls of cached images
- No workload changes: the image is rewritten when the pod is created
- No single point of failure: a proxy that is down or slow is skipped, and the pod pulls from the original registry

## How it works

An [`ImageAlternative`](../crds.md#imagealternative) declares that several repositories hold the same images. When a pod is created, kuik's webhook matches each container image against the entries: an image matching one entry gets the other entries as candidates, with the rest of its path and its tag carried over. With `repositoryGroup: docker.io` and `repositoryGroup: harbor.example.com/dockerhub`, `nginx:1.27` (that is `docker.io/library/nginx:1.27`) gets the candidate `harbor.example.com/dockerhub/library/nginx:1.27`.

`rewritePolicy: Always` puts the proxy ahead of the original. The webhook checks that the proxy answers for that image (a manifest `HEAD`, 2 seconds at most by default, see [`webhook.availabilityCheck.timeout`](../configuration.md#keys)) and rewrites the image to it; otherwise the pod keeps its original image.

## Set it up

This example uses a Harbor project named `dockerhub`, configured as a proxy cache of Docker Hub, at `harbor.example.com`.

1. [Install kuik](../installation.md).

2. If the proxy project is private, create a pull credential in kuik's namespace (`kuik-system` by default, adapt the command otherwise). kuik uses it to check the proxy, and copies it as a pull secret into every namespace the resource's `namespaceSelector` selects, so the kubelet can pull (see [Injected pull secrets](../concepts/pull-secrets.md)). With no `namespaceSelector`, that is every namespace of the cluster, and anyone who can read Secrets in one can read the credential: give it read access only, or set a `namespaceSelector`. Skip this step for a public project.

   ```bash
   kubectl create secret docker-registry harbor-pull --namespace kuik-system \
     --docker-server=harbor.example.com --docker-username=<robot account> --docker-password=<token>
   ```

3. Create the `ImageAlternative`. Drop the `auth` block for a public project.

   ```yaml
   apiVersion: kuik.enix.io/v1alpha1
   kind: ImageAlternative
   metadata:
     name: dockerhub-proxy
   spec:
     rewritePolicy: Always
     alternatives:
     - repositoryGroup: docker.io
     - repositoryGroup: harbor.example.com/dockerhub
       auth:
         secretRef:
           name: harbor-pull
   ```

4. kuik only rewrites pods at creation. Restart your workloads (Deployments, StatefulSets, DaemonSets) in the namespaces the resource covers to route the running ones, for instance:

   ```bash
   kubectl rollout restart deployment --namespace <namespace>
   ```

To proxy several upstream registries, create one `ImageAlternative` per upstream, each pairing the upstream with its own proxy project: every entry of a resource is treated as holding the same images as the others, so `quay.io` and `docker.io` in one resource would route Quay images to the Docker Hub proxy.

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ImageAlternative
metadata:
  name: quay-proxy
spec:
  rewritePolicy: Always
  alternatives:
  - repositoryGroup: quay.io
  - repositoryGroup: harbor.example.com/quay
```

To route only some workloads, restrict the resource with `namespaceSelector` and `podSelector`, see [Selecting workloads](../crds.md#selecting-workloads).

## Check it works

The image of a new pod points to the proxy:

```bash
kubectl get pod <pod> -o jsonpath='{.spec.containers[*].image}'
# harbor.example.com/dockerhub/library/nginx:1.27
```

kuik records each decision on the pod, with the original image:

```bash
kubectl get pod <pod> -o jsonpath='{.metadata.annotations.kuik\.enix\.io/rewrites}'
# {"nginx":{"by":"ImageAlternative/dockerhub-proxy","origin":"docker.io/library/nginx:1.27","rewrittenTo":"harbor.example.com/dockerhub/library/nginx:1.27","policy":"Always"}}
```

The resource counts the containers it rewrote:

```bash
kubectl get imagealternatives
# NAME              POLICY   READY   FALLBACK   REWRITTEN   AGE
# dockerhub-proxy   Always   True    False      42          3d
```

A container with no `kuik.enix.io/rewrites` entry kept its original image: most often, the proxy did not answer for it in time. When neither the proxy nor the original answered, the pod carries a `kuik.enix.io/no-alternatives` annotation instead, see [What the webhook records on a pod](../crds.md#what-the-webhook-records-on-a-pod). The webhook logs each candidate it tried at the `debug` level, see [Events, metrics and logs](../observability.md#logs).

## Good to know

- The entries of an `ImageAlternative` work both ways: a pod that already names `harbor.example.com/dockerhub/...` is offered `docker.io` too, ahead of its own image under `Always`. To send images one way only, to a registry you own, use an [`ImageMirror`](./better-performance-with-local-registry.md) instead: kuik copies the images there itself.
- A proxy that pulls the image from upstream on its first request may answer the check slower than the 2-second timeout: the first pod of a new image may then keep its original image, and so may the pods admitted in the next 10 seconds ([`webhook.availabilityCheck.activeCheckCache.ttl`](../configuration.md#keys)). Later ones go through the proxy.
- The nodes' container runtime pulls from the proxy itself: it must trust the proxy's TLS certificate. See [Registry TLS](../configuration.md#registry-tls) for kuik's side.

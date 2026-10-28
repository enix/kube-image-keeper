---
description: Mirror images to a registry close to your cluster to speed up pulls and reduce egress.
---

# Pull images faster from a local registry

kuik copies the images your pods use to a registry close to your cluster, and routes new pods to the copy. Pulls get faster and steadier, and the distant registry is read once per image instead of once per node.

## Best suited for

- Your production clusters pull from a development registry (GitLab, Artifactory...) that is far away or busy
- Your source registry is overloaded, or rations pulls
- Image pulls are slow, and slow down rollouts, scale-ups and node replacements
- Your source registry is far from the cluster, in network, latency or egress cost

## Benefits

- Pulls come from a registry next to the nodes: quicker and more stable
- Only the images your pods actually use are copied, never a whole registry
- The source stays a fallback: a copy that is not ready, or a local registry that is down, sends the pod to its original image

## How it works

An [`ImageMirror`](../crds.md#imagemirror) watches the pods it selects and copies their images under `destination.path`, the full original reference appended, with this cluster's [ID](../configuration.md#cluster-id) as a tag suffix: `gitlab.example.com/team/app:1.4` is copied to `registry.local.example.com/kuik/gitlab.example.com/team/app:1.4_<clusterID>`.

With `rewritePolicy: Always`, the webhook places the copy ahead of the original on every new pod, once it checked the copy is there. Until the copy is made, the pod keeps its original image. A container with `imagePullPolicy: Always` is the exception, see [Good to know](#good-to-know).

## Set it up

1. [Install kuik](../installation.md).

2. Prepare the local registry. It must accept nested repository paths (Harbor, Zot and the CNCF `distribution` registry do), and with cleanup on, tag deletion and its own garbage collection: see [Destination requirements](../crds.md#destination-requirements).

3. Create 2 credentials in kuik's namespace (`kuik-system` by default, adapt the commands otherwise): one for kuik to push, read, list and delete tags, one for the kubelet to pull. kuik copies the pull credential into every namespace the mirror's `namespaceSelector` selects, all of them when it is empty: anyone who can read Secrets there can read it. Set a `namespaceSelector` to bound it (see [Injected pull secrets](../concepts/pull-secrets.md)). Skip the pull credential if the registry allows anonymous pulls.

   ```bash
   kubectl create secret docker-registry local-registry-push --namespace kuik-system \
     --docker-server=registry.local.example.com --docker-username=<push user> --docker-password=<token>
   kubectl create secret docker-registry local-registry-pull --namespace kuik-system \
     --docker-server=registry.local.example.com --docker-username=<pull user> --docker-password=<token>
   ```

4. Create the `ImageMirror`:

   ```yaml
   apiVersion: kuik.enix.io/v1alpha1
   kind: ImageMirror
   metadata:
     name: local-registry
   spec:
     rewritePolicy: Always
     destination:
       path: registry.local.example.com/kuik
       manage:
         auth:
           secretRef:
             name: local-registry-push
       pull:
         auth:
           secretRef:
             name: local-registry-pull
     cleanup:
       retention: 168h
   ```

5. Adjust the copy pace. kuik copies at most one image per source registry per `copy.interval`, 3 minutes by default, to keep its share of a rate limit small: 100 images from one registry take 5 hours. For a registry you own, which does not ration pulls, lower it in the chart values (see [Registry pacing](../concepts/pacing.md)):

   ```yaml
   # values.yaml
   config:
     registries:
       gitlab.example.com:
         copy:
           interval: 10s
   ```

6. Once the copies are made, restart your workloads: kuik only rewrites pods at creation. The copies are made when `available` reaches `tracked`:

   ```bash
   kubectl get imagemirror local-registry -o jsonpath='{.status.images.copy}'
   # {"available":57,...,"tracked":57,...}
   ```

To mirror only some workloads, restrict the resource with `namespaceSelector` and `podSelector`, see [Selecting workloads](../crds.md#selecting-workloads). To keep some images out, list them in [`excludeImages`](../crds.md#excluding-images).

## Check it works

The mirror reports how many images it holds, and which copies fail:

```bash
kubectl get imagemirrors
# NAME             DESTINATION                       POLICY   READY   OUTOFSYNC   TRACKED   AGE
# local-registry   registry.local.example.com/kuik   Always   True    False       57        2d
kubectl get imagemirror local-registry -o jsonpath='{.status.failedImageCopies}'
```

A new pod runs the copy, and records the original image:

```bash
kubectl get pod <pod> -o jsonpath='{.metadata.annotations.kuik\.enix\.io/rewrites}'
# {"app":{"by":"ImageMirror/local-registry","origin":"gitlab.example.com/team/app:1.4","rewrittenTo":"registry.local.example.com/kuik/gitlab.example.com/team/app:1.4_focused-turing-7f3a","policy":"Always"}}
```

The first copy of an image emits an `ImageCopied` event on the mirror. Failures emit `ImageCopyFailed`, one event per reason and registry. Alert on [`kuik_image_copy_failed`](../observability.md#anomalies).

## Good to know

- kuik reads a private source with the pull secrets of the pods that use the image. With the chart value `secretAccess.mode: restricted`, declare the source's credentials in [`fallbackAuth`](../configuration.md#fallbackauth).
- A container with `imagePullPolicy: Always` asks for the latest digest of its tag: kuik tries its original first and the copy only as a fallback. Set [`webhook.demoteMirrorWithPullPolicyAlways`](../configuration.md#keys) to `false` to route it to the copy too.
- A tag is copied once. If your tags move (`latest`, `main`), set `driftPolicy: Sync` to copy them again when their upstream digest changes, or `Warn` to be told.
- A tag no pod uses any more is deleted from the local registry once `cleanup.retention` has elapsed, so that a CronJob's image survives between runs. kuik deletes tags only: the registry's garbage collection frees the space.
- Several clusters can share one local registry: each one writes its own tags, under its own cluster ID. Never give 2 clusters the same ID, see [Cluster ID](../configuration.md#cluster-id).

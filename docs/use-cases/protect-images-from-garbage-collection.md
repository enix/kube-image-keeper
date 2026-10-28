---
description: Back up the images currently used by running Pods to a second registry before upstream garbage collection removes them.
sidebar:
  order: 3
---

# Protect images from garbage collection

kuik keeps a copy of the images your pods use in a second registry, for the pods you select. When the garbage collection or the retention policy of the origin registry deletes an image that still runs, the next pod that needs it pulls the copy instead of failing with `ImagePullBackOff`.

## Best suited for

- Your origin registry runs a garbage collection or a retention policy (GitLab cleanup policies, Harbor tag retention, ECR lifecycle rules...), and you fear it deletes images still in use
- Your registry holds plenty of images (outdated, prior versions, development builds) and you want to clean it up aggressively
- You would like to keep only the subset of useful images in your production registry

## Benefits

- The images your pods use stay available, whatever the origin registry deletes
- The backup holds only what the cluster uses: kuik deletes the copies no pod has used for a week (`cleanup.retention`)
- Pods keep pulling from the origin while it holds the image: the copy is only a fallback

## How it works

An [`ImageMirror`](../crds.md#imagemirror) copies the image of every pod it selects to `destination.path`, once a pod uses it, at the copy pace of its registry. With `rewritePolicy: OnFailure`, the webhook keeps the original image on new pods while it answers, and rewrites a pod to the copy only when the original does not answer: deleted, or unreachable, or refusing the pod's credentials.

When no pod uses an image any more, its copy is kept for `cleanup.retention` (a week by default, so that a CronJob's image survives between runs), then its tag is deleted from the backup registry.

## Set it up

1. [Install kuik](../installation.md).

2. Prepare the backup registry. It must accept nested repository paths (Harbor, Zot and the CNCF `distribution` registry do), support tag deletion, and run its own garbage collection: kuik deletes tags, the registry frees the space. See [Destination requirements](../crds.md#destination-requirements).

3. Create 2 credentials in kuik's namespace (`kuik-system` by default, adapt the commands otherwise): one for kuik to push, read, list and delete tags, one for the kubelet to pull the copies. kuik copies the pull credential into the namespaces of the pods it rewrites (see [Injected pull secrets](../concepts/pull-secrets.md)).

   ```bash
   kubectl create secret docker-registry backup-push --namespace kuik-system \
     --docker-server=backup.example.com --docker-username=<push user> --docker-password=<token>
   kubectl create secret docker-registry backup-pull --namespace kuik-system \
     --docker-server=backup.example.com --docker-username=<pull user> --docker-password=<token>
   ```

4. Create the `ImageMirror`. This one covers the namespaces labelled `env: production`, and leaves out the images that are never garbage collected:

   ```yaml
   apiVersion: kuik.enix.io/v1alpha1
   kind: ImageMirror
   metadata:
     name: production-backup
   spec:
     namespaceSelector:
       matchLabels:
         env: production
     excludeImages:
     - docker.io/**
     - registry.k8s.io/**
     rewritePolicy: OnFailure
     destination:
       path: backup.example.com/kuik
       manage:
         auth:
           secretRef:
             name: backup-push
       pull:
         auth:
           secretRef:
             name: backup-pull
     cleanup:
       retention: 168h
   ```

5. kuik copies at most one image per source registry per `copy.interval`, 3 minutes by default. For your own registry, which does not ration pulls, lower it so the first copies land quickly (see [Registry pacing](../concepts/pacing.md)):

   ```yaml
   # values.yaml
   config:
     registries:
       gitlab.example.com:
         copy:
           interval: 10s
   ```

## Check it works

The mirror counts the images it holds, and lists the copies that failed:

```bash
kubectl get imagemirrors
# NAME                DESTINATION               POLICY      READY   OUTOFSYNC   TRACKED   AGE
# production-backup   backup.example.com/kuik   OnFailure   True    False       38          9d
kubectl get imagemirror production-backup -o jsonpath='{.status.failedImageCopies}'
```

A copy fails with `SourceNotFound` when the origin had already deleted the image before kuik copied it: the mirror cannot protect it any more. Alert on [`kuik_image_copy_failed`](../observability.md#anomalies).

When the origin deletes an image, the next pod that needs it runs the copy: it gets an `ImageFallback` event, and the mirror turns its `FallbackActive` condition to `True` and lists the image in `status.activeFallbacks`. Alert on [`kuik_fallback_active_pods`](../observability.md#anomalies) to rebuild the image, or to fix the retention policy that deleted it.

## Good to know

- kuik reads a private origin with the pull secrets of the pods that use the image. With the chart value `secretAccess.mode: restricted`, declare the origin's credentials in [`fallbackAuth`](../configuration.md#fallbackauth) for the copies, and list the namespaces of those pods in `secretAccess.namespaces`: the webhook never uses `fallbackAuth`, so elsewhere it checks a private origin anonymously, and routes every new pod to the copy as if the origin were gone.
- Start the mirror before tightening the retention policy of the origin: an image is only protected once its copy exists.
- Excluding an image, or narrowing a selector, deletes its copy once `cleanup.retention` has elapsed. `cleanup.enabled: false` keeps every copy forever.
- A deleted `ImageMirror` waits until no pod runs one of its copies, then deletes its tags, unless `cleanup.enabled` is `false`, see [Deleting an ImageMirror](../crds.md#deleting-an-imagemirror).
- To see the missing images that are not mirrored, add an [`ImageMonitor`](./detect-missing-images-before-outage.md).

---
title: Protect images from garbage collection
description: Back up the images currently used by running Pods to a second registry before upstream garbage collection removes them.
---

This documentation will help you configure Kuik in order to "backup" useful (used by a running Pod) images on another registry, prior to a garbace collect on your origin registry.

## Best suited for

- You configured a garbage collect on your origin registry, and you feel that it is too aggressive in terms of image deletion.
- You have plenty of images (outdated, prior versions, development version).
- You would like to keep only the subset of useful images in your production registry.

## Benefits

- Kuik will ensure that useful images stays replicated on a new registry.
- and will garbage collect images that are no longer used in your Kubernetes cluster.

## Implementation

### Kuik custom resource to use

- [ClusterImageSetMirror](/crds/#clusterimagesetmirror)
- or [ImageSetMirror](/crds/#clusterimagesetmirror)

### Configuration example

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ImageSetMirror
metadata:
  name: smart-replication-gc
  namespace: myproject
spec:
  filter:
    include:
    - image: "myregistry.mydomain/myproject/myimage:.+" # protect these images from aggressive garbage collect on origin registry
  mirrors:
  - registry: backup.custom.domain # an already existing (destination) registry
    path: /mirгог
    credentialSecret:
      name: backup-registry-secret # the secret must be located in the same namespace
  cleanup:
    enabled: true
    retention: 24h # delete image on mirror 24h after an image is no longer used on kube
```

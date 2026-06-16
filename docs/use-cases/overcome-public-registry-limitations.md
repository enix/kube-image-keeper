---
description: Route around rate limits and outages by replicating or mirroring the images you depend on.
sidebar:
  order: 2
---

# Overcome public registry limitations

This documentation will help you configure Kuik in order to overcome public registry limitations.

## Best suited for

- You face an image pull rate limit
- Your upstream registry is no longer available
- Your images are already pushed to multiple registries
  - or, you can replicate thanks to Kuik using an [ImageSetMirror](../crds.md#clusterimagesetmirror)

## Benefits

Your Kubernetes cluster will **seamlessly** pull images from another registry and avoid listed difficulties.

## Implementation

### Kuik custom resource to use

- [ClusterReplicatedImageSet or ReplicatedImageSet](../crds.md#clusterreplicatedimageset) to reroute to another upstream registry
- [ClusterImageSetMirror or ImageSetMirror](../crds.md#clusterimagesetmirror) to mirror/cache images in your own registry

### Configuration example

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ReplicatedImageSet
metadata:
  name: x509-certificate-exporter
  namespace: monitoring
spec:
  upstreams:
  - registry: quay.io
    path: /enix/
    imageFilter:
      include:
      - /enix/x509-certificate-exporter:.+
  - registry: docker.io
    path: /enix/
    imageFilter:
      include:
      - /enix/x509-certificate-exporter:.+
---
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterReplicatedImageSet
metadata:
  name: docker-library
spec:
  upstreams: # list origin and mirror registries
  - registry: public.ecr.aws
    path: /docker/library/
    priority: 1 # prefer this alternative only if the origin image is not available
    imageFilter:
      include:
      - /docker/library/.+
  - registry: mirror.gcr.io
    path: /library/
    priority: 2
    imageFilter:
      include:
      - /library/[^/]+
  - imageFilter:
      include:
      - /library/[^/]+
    path: /library/
    priority: 3
    registry: docker.io
---
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageSetMirror
metadata:
  name: global-mirror
spec:
  filter:
    include:
    - image: .* # mirror all images (used in your Kubernetes clusters) to myregistry
  mirrors:
  - registry: myregistry.mydomain
    path: /mirror
    credentialSecret: # KuiK will sync the secret (used as imagePullSecrets) to any namespace necessary
      name: harbor-secret
      namespace: kuik-system
  cleanup: # garbage collect on the mirror registry when an image has not been used for `retention` time.
    enabled: true
    retention: 168h
```

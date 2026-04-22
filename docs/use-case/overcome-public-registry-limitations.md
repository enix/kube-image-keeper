# Overcome public registry limitations

This documentation wil help you configure Kuik in order to overcome public registry limitations.

## Best suited for

- You face an image pull rate limit
- Your upstream registry is no longer available
- Your images are already pushed to multiple registries
  - or, you can replicate thanks to Kuik using a [ImageSetMirror](https://github.com/enix/kube-image-keeper/blob/docs/use-cases/docs/crds.md#clusterimagesetmirror)

## Benefits

Your Kubernetes cluster will **seamlessly** pull images from another registry and avoid listed difficulties.

## Implementation

### Kuik custom resource to use

- [ClusterReplicatedImageSet or ReplicatedImageSet](/docs/crds.md#clusterreplicatedimageset) to reroute to another upstream registry
- [ClusterImageSetMirror or ImageSetMirror](/docs/crds.md#clusterimagesetmirror) to mirror/cache images in your own registry

### Configuration example

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ReplicatedImageSet
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
  imageFilter:
    include:
    - .* # mirror all images (used in your Kubernetes clusters) to myregistry
  mirrors:
  - registry: myregistry.mydomain
    path: /mirгог
    credentialSecret: # KuiK will sync the secret (used as imagePullSecrets) to any namespace necessary
      name: harbor-secret
      namespace: kuik-system
  cleanup: # garbage collect on the mirror registry when an image has not been used for `retention` time.
    enabled: true
    retention: 168h
```

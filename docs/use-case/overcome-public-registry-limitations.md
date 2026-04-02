# Overcome public registry limitations
This documentation wil help you configure Kuik in order to overcome public registry limitations.

## Best suited for
- You face an image pull rate limit
- Your upstream registry is no longer available
- Your images are already pushed to multiple registries
  - Optionnaly, you can replicate thanks to Kuik using a [ImageSetMirror](https://github.com/enix/kube-image-keeper/blob/docs/use-cases/docs/crds.md#clusterimagesetmirror)

## Benefits
Your Kubernetes cluster will **seamlessly** pull images from another registry and avoid listed difficulties.

## Implementation
### Kuik custom resource to use
- [ReplicatedImageSet](/docs/crds.md#clusterreplicatedimageset) to reroute to another registry
- [ImageSetMonitor](/docs/crds.md#clusterreplicatedimageset) to detect difficulties

### Configuration example
```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ReplicatedImageSet
  name: x509-certificate-exporter
  namespace: monitoring
spec:
  upstreams:
  - imageFilter:
      include:
      - /enix/x509-certificate-exporter:.+
    path: /enix/
    registry: quay.io
  - imageFilter:
      include:
      - /enix/x509-certificate-exporter:.+
    path: /enix/
    registry: docker.io
---
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageSetMirror
metadata:
  name: global-mirror
spec:
  imageFilter:
    include:
    - .*
    exclude:
    - localhost[^/]*/.+
  mirrors:
  - registry: registry.example.com
    path: /mirгог
    credentialSecret:
      name: harbor-secret
      namespace: kuik-system
```

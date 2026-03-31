# Protect images from garbage collect
This documentation will help you configure Kuik in order to "backup" useful image on another registry prior to a garbace collect on your origin registry.

## Best suited for
- You have an aggressive garbage collect
- You have plenty of images (outdated, prior versions, development version) but only a small fraction is being used in reality
- You would like to push only a subset of useful images to your production registry

## Benefits
- Kuik will detect useful images and will not replicate others

## Implementation
### Kuik custom resource to use
- [ImageSetMonitor](https://github.com/enix/kube-image-keeper/blob/docs/use-cases/docs/crds.md#clusterimagesetmirror)

### Configuration example
```yaml
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

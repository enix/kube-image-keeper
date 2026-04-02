# Protect images from garbage collect
This documentation will help you configure Kuik in order to "backup" useful image on another registry prior to a garbace collect on your origin registry.

## Best suited for
- You have an aggressive garbage collect
- You have plenty of images (outdated, prior versions, development version) but only a small fraction is being used in reality
- You would like to keep only the subset of useful images in your production registry

## Benefits
- Kuik will ensure useful images stays replicated but will garbage collect the rest.

## Implementation
### Kuik custom resource to use
- [ClusterImageSetMonitor](/docs/crds.md#clusterimagesetmirror)

### Configuration example
```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageSetMirror
metadata:
  name: smart-replication
spec:
  imageFilter:
    include:
    - .*
  mirrors:
  - registry: backup.custom.domain
    path: /mirгог
    credentialSecret:
      name: backup-registry-secret
      namespace: default
```

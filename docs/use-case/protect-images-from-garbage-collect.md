# Protect images from garbage collect
This documentation will help you configure Kuik in order to "backup" useful image on another registry prior to a garbace collect on your origin registry.

## Best suited for
- You configured a garbage collect on your origin registry, and you feel that it is too aggressive in terms of image deletion.
- You have plenty of images (outdated, prior versions, development version).
- You would like to keep only the subset of useful images in your production registry.

## Benefits
- Kuik will ensure that useful images stays replicated on a new registry.
- and will garbage collect images that are no longer used in your Kubernetes cluster.

## Implementation
### Kuik custom resource to use
- [ClusterImageSetMonitor](/docs/crds.md#clusterimagesetmirror)

### Configuration example
```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageSetMirror
metadata:
  name: smart-replication-gc
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

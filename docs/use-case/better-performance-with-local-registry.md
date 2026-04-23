# Better performance with local registry

This documentation will help you configure Kuik in order to reroute kubernetes image pull from a distant registry to a better placed (local to your kubernetes cluster) one.
In brief, Kuik will (1) mirror images and (2) rewrite image paths to point to your local registry.

## Best suited for

- You use a development registry (ex: gitlab, maven, ...) for production Kubernetes clusters.
- Your source registry is overloaded.
- Image pull from Kubernetes are too slow / long.
- Your source registry is too far away (from a network / geographic / latency standpoint) from the Kubernetes cluster

## Benefits

Kubernetes image pull will be quicker and more stable.

## Implementation

### Kuik custom resource to use

- [ClusterImageSetMirror](/docs/crds.md#clusterimagesetmirror)

### Configuration example

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageSetMirror
metadata:
  name: mirror-and-reroute-local
spec:
  priority: -10 # negative value to prefer mirrored images
  imageFilter:
    include:
    - .* # will match all images
  mirrors:
  - registry: local.custom.domain # your local, already existing registry
    path: /mirгог
    credentialSecret:
      name: local-registry-secret
      namespace: default
```

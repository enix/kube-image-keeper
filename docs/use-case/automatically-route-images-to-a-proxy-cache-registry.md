# Automatically route images to a proxy cache registry
This documentation will help you configure Kuik in order to simplify a proxy cache registry implementation in Kubernetes.
In other words, Kuik will automatically rewrite calls for images to a proxy cache; without any modification of the Pod specifications on your hand.

## Best suited for
- You already have setup a proxy cache registry (like Harbor or Gitlab proxy cache) but do not know how to use it
- You do not want to review all workloads deployments (and change their image path)

## Benefits
Kuik will manage the burden of rerouting calls to your proxy cache

## Implementation
### Kuik custom resource to use
- [ClusterReplicatedImageSet](/docs/crds.md#clusterreplicatedimageset)

### Configuration example
```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterReplicatedImageSet
metadata:
  name: proxy-cache-reroute
spec:
  upstreams:
  - registry: docker.io
    imageFilter:
      include:
      - .*
    path: /
  - registry: harbor.custom.domain
    imageFilter:
      include:
      - .*
    path: /docker-mirror
```

# Automatically route images to a proxy cache registry
This documentation will help you configure Kuik in order to simplify a proxy cache registry implementation in Kubernetes.
In other words, Kuik will automatically rewrite image paths to use a proxy cache; without requiring any `spec` customization (Deployment, StatefulSet, ...).

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
  name: dockerhub-proxy-cache
spec:
  priority: -1 # prefer alternative images (proxy cached on gitlab in this example) rather than original one
  upstreams:
  - registry: docker.io
    imageFilter:
      include:
      - /library/[^/]+
    path: /library/ 
  - registry: gitlab.example.com
    imageFilter:
      include:
      - /my-group/dependency_proxy/containers/library/.+
    path: /my-group/dependency_proxy/containers/
```

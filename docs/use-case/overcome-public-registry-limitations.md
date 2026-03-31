# Overcome public registry limitations
This documentation wil help you configure Kuik in order to overcome public registry limitations.

## Best suited for
- You face an image pull rate limit
- Your upstream registry is no longer available

## Benefits
Your Kubernetes cluster will **seamlessly** pull images from another registry and avoid listed difficulties.

## Implementation
### Kuik custom resource to use
- [ReplicatedImageSet](/docs/crds.md#clusterreplicatedimageset) to reroute to another registry
- [ImageSetMonitor](/docs/crds.md#clusterreplicatedimageset) to detect difficulties

### Configuration example
```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterReplicatedImageSet
metadata:
  name: thanos
spec:
  upstreams:
  - registry: docker.io
    imageFilter:
      include:
      - /thanosio/thanos:.+
    path: /thanosio/thanos
  - registry: quay.io
    imageFilter:
      include:
      - /thanos/thanos:.+
    path: /thanos/thanos/
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

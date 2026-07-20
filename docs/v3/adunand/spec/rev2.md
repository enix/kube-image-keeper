# ClusterImageRouter

```yaml
kind: ClusterImageRouter
metadata:
  name: acme-foo
spec:
  # Generic Kubernetes labels selector field to restrict pods where this CR apply
  # https://kubernetes.io/docs/reference/generated/kubernetes-api/latest/#labelselector-v1-meta
  # Empty/Nothing match all pod
  podSelector: {}
  namespaceSelector: {}

  # Image rewrite policy used in mutating webhook
  #   Fallback: Default. Keep using original image if available, else use first available
  #             image in `alternatives` list
  #   Force: Always rewrite image to first available in `alternatives` list
  #          (bypass quota, latency, network cost, …)
  rewritePolicy: Fallback    # Fallback | Force

  # Ordered list of equivalent image that could be used if one is unavailable
  alternatives:
  - prefix: quay.io/acme/foo
  - prefix: docker.io/acme-org/foo
  - copierRef:                   # reference images from a destination registry of a ClusterImageCopier
      name: prod-mirror
    auth:                        # credentials to pull images from the registry
      secretRef:
        name: mirror-pull
        injectPullSecret: true   # Default: true (for secretRef)
                                 # inject secret in pod namespace if this image is used as alternative
                                 # so kubelet could pull the image from the registry
  - prefix: 123456.dkr.ecr.eu-west-3.amazonaws.com/repo/acme/foo
    auth:
      provider:                  # Provider specific auth, like AWS IRSA, same logic as
        name: aws                # https://fluxcd.io/flux/components/source/ocirepositories/#provider
        serviceAccountRef:
          name: kuik-ecr-access
      injectPullSecret: false    # Default: false (for provider) - kubelet already have permission
                                 # to pull from provider registry without additional secret
  - prefix: registry.local:5000/mirror/acme/foo
    insecure: true               # HTTP registry
    unavailable: true            # Image no longer available in this repository but if a pod
                                 # use this image, we'll try to substitute an alternative
```

# ClusterImageCopier

```yaml
kind: ClusterImageCopier
metadata:
  name: prod-mirror
spec:
  # Generic Kubernetes labels selector field to restrict pods where this CR apply
  # https://kubernetes.io/docs/reference/generated/kubernetes-api/latest/#labelselector-v1-meta
  # Empty/Nothing match all pod
  podSelector: {}
  namespaceSelector: {}

  destination:
    registry: registry.tld
    pathPrefix: /mirror/
    insecure: false            # Default: false - Allow HTTP registry
    auth:                      # Credentials for controller to push images, delete unused tags and
      secretRef:               # check images status on destination registry
        name: mirror-credentials

  cleanup:
    enabled: true              # Default: true - Delete image tag no longer referenced by any pod
    retention: 24h             # Image tag hold duration before deleting them, to deal with cronjob for instance


  # Detect and reconcile image tag drift (digest change), e.g. tag `latest`
  #   SyncOnce: Image is copied once on destination registry and not updated if upstream tag digest change
  #   KeepSynced: Periodically check if tag digest is still the same and resync image in destination if different
  driftDetection
    mode: SyncOnce             # SyncOnce (default) | KeepSynced

  # Multi-arch support
  #   Auto: Copy images for arch retrieved from node labels
  #   All: Copy all arch referenced for an image
  #   List: Explicit list of arch we need to copy images
  platforms:
    mode: Auto                 # Auto (default) | All | List
    #list: []                  # Only used with `mode: List`
```

# ClusterImageMonitor

```yaml
kind: ClusterImageMonitor
metadata:
  name: cluster-images
spec:
  # Generic Kubernetes labels selector field to restrict pods where this CR apply
  # https://kubernetes.io/docs/reference/generated/kubernetes-api/latest/#labelselector-v1-meta
  # Empty/Nothing match all pod
  podSelector: {}
  namespaceSelector: {}

  unusedImageExpiry: 24h       # keep monitoring for a given time after no longer used in cluster
                               # useful for cronjob

  driftDetection:
    enabled: true              # Default: true - Detect if an image tag digest differ from pod running in cluster

  monitorAlternatives: false   # Default: false - Also monitor alternatives images instead of only original ones

```

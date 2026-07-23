# spec v3

## ImageAlternative

```yaml
kind: ImageAlternative
metadata:
  name: acme-foo
spec:
  # Generic Kubernetes labels selector field to restrict pods where this CR apply
  # https://kubernetes.io/docs/reference/generated/kubernetes-api/latest/#labelselector-v1-meta
  # Empty/Nothing match all pod
  podSelector: {}
  namespaceSelector: {}

  # Image rewrite policy used in mutating webhook
  #   OnFailure: Default. Keep using original image if available, else use first available
  #             image in `alternatives` list
  #   Always: Always rewrite image to first available in `alternatives` list
  #          (bypass quota, latency, network cost, …)
  rewritePolicy: OnFailure    # OnFailure | Always

  # Ordered list of equivalent image prefix that could be used if one is unavailable
  alternatives:
  - quay.io/acme/foo
  - docker.io/acme-org/foo
  - 123456.dkr.ecr.eu-west-3.amazonaws.com/repo/acme/foo
  - registry.local:5000/mirror/acme/foo

  # Optional, in most use case with public registry this will be empty
  # Use a prefix from `alternatives` field as key to provide extra config
  config:
    123456.dkr.ecr.eu-west-3.amazonaws.com/repo/acme/foo:
      auth:
        provider:                  # Provider specific auth, like AWS IRSA, same logic as
          name: aws                # https://fluxcd.io/flux/components/source/ocirepositories/#provider
          serviceAccountRef:
            name: kuik-ecr-access
        injectPullSecret: false    # Default: false (for provider) - kubelet already have permission
                                   # to pull from provider registry without additional secret
    "registry.local:5000/mirror/acme/foo":
      insecure: true               # HTTP registry
      unavailable: true            # Image no longer available in this repository but if a pod
                                   # use this image, we'll try to substitute an alternative
      auth:                        # credentials to pull images from the registry
        secretRef:
          name: local-registry
          injectPullSecret: true   # Default: true (for secretRef)
                                   # inject secret in pod namespace if this image is used as alternative
                                   # so kubelet could pull the image from the registry
```

## ImageMirror

```yaml
kind: ImageMirror
metadata:
  name: prod-mirror
spec:
  # Generic Kubernetes labels selector field to restrict pods where this CR apply
  # https://kubernetes.io/docs/reference/generated/kubernetes-api/latest/#labelselector-v1-meta
  # Empty/Nothing match all pod
  podSelector: {}
  namespaceSelector: {}
  # List of image prefix to explicitly exclude from mirroring (e.g. huge images)
  imageExclude: []

  # Image rewrite policy used in mutating webhook
  #   OnFailure: Default. Keep using original image if available, else use the mirrored
  #             image in `destination`
  #   Always: Always rewrite image to the mirrored image in `destination`
  #          (bypass quota, latency, network cost, …)
  #   None: Only copy image to mirror, don't use it as an image alternative (archiving, compliance, security scan, …)
  rewritePolicy: OnFailure    # OnFailure (default) | Always | None

  destination:
    path: registry.tld/mirror/
    insecure: false            # Default: false - Allow HTTP registry
    push:                      # Credentials for controller to push images and delete unused tags
      auth:
        secretRef:
          name: mirror-write-credentials
    pull:                      # Credentials to pull image that will be synced in namespaces
      auth:                    # with rewritten image to use destination registry
        secretRef:
          name: mirror-read-credentials

  cleanup:
    enabled: true              # Default: true - Delete image tag no longer referenced by any pod
    retention: 24h             # Image tag hold duration before deleting them, to deal with cronjob for instance


  # Detect and reconcile image tag drift (digest change), e.g. tag `latest`
  #   Ignore: Image is copied once on destination registry and not updated if upstream tag digest change
  #   Warn: Periodically check if tag digest is still the same and warn if different
  #   Sync: Periodically check if tag digest is still the same and resync image in destination if different
  driftPolicy: Ignore          # Ignore (default) | Warn | Sync

  # Multi-arch support
  #   Auto: Copy images for arch retrieved from node labels
  #   All: Copy all arch referenced for an image
  #   List: Explicit list of arch we need to copy images
  platforms:
    mode: Auto                 # Auto (default) | All | List
    #list: []                  # Only used with `mode: List`
```

## ImageMonitor

```yaml
kind: ImageMonitor
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

## Annotations

Annotations added to pod by mutating webhook:

```yaml
metadata:
  annotations:
    # Same as KuiK v2, keep original images name referenced when we rewrite pod spec
    kuik.enix.io/original-images: |
      {"config-reloader":"quay.io/prometheus-operator/prometheus-config-reloader:v0.91.0","prometheus":"quay.io/prometheus/prometheus:v3.13.1-distroless","thanos-sidecar":"quay.io/thanos/thanos:v0.42.2"}'
    # CR that rewritten the image
    kuik.enix.io/rewritten-by: |
      {"config-reloader":"ImageMirror/prod-mirror","prometheus":"ImageAlternative/prometheus","thanos":"ImageAlternative/thanos"}
    # Reason of KuiK action (image rewrite) or inaction if original image isn't available
    #   OnFailure: Original image wasn't available, rewritten to first available alternative
    #   Always: Rewrite to first available alternative requested by rewritePolicy
    #   NoAlternatives: Could not find an available alternative for the image
    kuik.enix.io/reason:
      {"config-reloader":"NoAlternatives","prometheus":"Always","thanos":"OnFailure"}
```

## Global config

```yaml
webhook:
  availabilityCheck:
    timeout: 2s              # max time before considering a registry as unavailble
    # Cache per controller replica to avoid querying registry multiple time on burst
    # A single image used by 50 pods scheduled in a short period should result in 1 check, not 50
    activeCheckCache:
      ttl: 10s
    # Use image negative check result from ImageMirror and ImageMonitor to tests
    # other alternatives first to optimize
    skipHints:
      enabled: true
      maxAge: 30m
registries:
  default:
    check:
      method: HEAD   # HEAD (default) | GET
      interval: 10m
      maxPerInterval: 20
      timeout: 10s
    copy:
      interval: 1h
      maxPerInterval: 20
      timeout: 10s

  private-registry.tld:
    copy:
      interval: 10m
    # Auth used by KuiK ImageMonitor (and eventually ImageAlternative if not provided)
    # to check image availability if we don't have access to image pull secret
    # (if KuiK is configured without cluster-wide secret access for security)
    perPrefixFallbackAuth:
    - prefix: /project1
      secretRef:
        name: project1-creds
    - prefix: /project2
      secretRef:
        name: project2-creds

  123456.dkr.ecr.eu-west-3.amazonaws.com:
    perPrefixFallbackAuth:
    - prefix: /acme
      provider:
        name: aws
        serviceAccountRef:
          name: kuik-ecr-access

  docker.io:
    copy:
      interval: 1h
      maxPerInterval: 6
    perPrefixFallbackAuth:
    - prefix: /
      secretRef:
        name: dockerhub-creds

  public.ecr.aws:
    check:
      interval: 30m
```

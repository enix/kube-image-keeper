# spec v3

## Scope and filtering

The three custom resources (`ImageAlternative`, `ImageMirror`, `ImageMonitor`) are all
**cluster-scoped**. There is no namespaced variant: in v2 the mirror and replication kinds each
had a namespaced peer (`ImageSetMirror`, `ReplicatedImageSet`), v3 drops them. Restricting a
resource to a subset of the cluster is done with `namespaceSelector`, not by creating the object
in a given namespace:

```yaml
namespaceSelector:
  matchLabels:
    kubernetes.io/metadata.name: monitoring
```

Filtering is expressed on **workloads**, not on images: `podSelector` and `namespaceSelector`
select the pods a resource applies to, and an empty selector matches everything. The single
exception is `ImageMirror`'s `spec.excludeImagePrefixes`, which keeps specific images out of a
mirror (e.g. huge images) without changing which pods the mirror applies to.

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

  # Ordered list of equivalent images (or image subpaths) that could be used if one is
  # unavailable. All entries of a list must use the same form, see "Alternatives matching"
  # Every field besides `imagePrefix` is optional, and with public registries only
  # `imagePrefix` is usually needed
  alternatives:
  - imagePrefix: quay.io/acme/foo
  - imagePrefix: docker.io/acme-org/foo
  - imagePrefix: 123456.dkr.ecr.eu-west-3.amazonaws.com/repo/acme/foo
    auth:
      provider:                    # Provider specific auth, like AWS IRSA, same logic as
        name: aws                  # https://fluxcd.io/flux/components/source/ocirepositories/#provider
        serviceAccountRef:
          name: kuik-ecr-access
      injectPullSecret: false      # Default: false (for provider) - kubelet already have permission
                                   # to pull from provider registry without additional secret
  - imagePrefix: "registry.local:5000/mirror/acme/foo"
    insecure: true                 # HTTP registry
    unavailable: true              # Image no longer available in this repository but if a pod
                                   # use this image, we'll try to substitute an alternative
    auth:                          # credentials to pull images from the registry
      secretRef:
        name: local-registry
      injectPullSecret: true       # Default: true (for secretRef)
                                   # inject secret in pod namespace if this image is used as alternative
                                   # so kubelet could pull the image from the registry
```

### Alternatives matching

The `imagePrefix` of an entry is **not** a glob: there is no implicit wildcard, and no glob
marker is supported. It is either a **single image** or a **subpath**, and the form is given
by the trailing character:

| Form | Written as | Matches |
| ---- | ---------- | ------- |
| Single image | no trailing separator (`quay.io/acme/foo`) | that exact repository only, whatever the tag or digest |
| Subpath | significant trailing `/` (`quay.io/acme/foo/`) | repositories located **directly** under that path (one level only), like a trailing slash for rsync directories |

An `imagePrefix` is a prefix of the image reference at path segment granularity, not a free
form string prefix: `quay.io/acme/foo` matches `quay.io/acme/foo:v1` but never
`quay.io/acme/foo-bar:v1`.

When an image matches, the tag or digest is always preserved, and for the subpath form the
matched remainder (the single path segment below the subpath) is preserved too.

#### Single image form

```yaml
alternatives:
- imagePrefix: quay.io/acme/foo
- imagePrefix: docker.io/acme-org/foo
```

| Image | Result |
| ----- | ------ |
| `quay.io/acme/foo:latest` | matches, rewritten to `docker.io/acme-org/foo:latest` |
| `quay.io/acme/foo-bar:latest` | doesn't match (no prefix matching on a segment) |
| `quay.io/acme/foo/bar:latest` | doesn't match (deeper than the entry) |
| `quay.io/acme/foo/bar/oni:latest` | doesn't match |

#### Subpath form

```yaml
alternatives:
- imagePrefix: quay.io/acme/foo/
- imagePrefix: docker.io/acme-org/foo/
```

| Image | Result |
| ----- | ------ |
| `quay.io/acme/foo:latest` | doesn't match (the subpath itself is not an image) |
| `quay.io/acme/foo-bar:latest` | doesn't match |
| `quay.io/acme/foo/bar:latest` | matches, rewritten to `docker.io/acme-org/foo/bar:latest` |
| `quay.io/acme/foo/bar/oni:latest` | doesn't match (only one level below the subpath) |

#### Invalid `alternatives`

Rejected at admission (validation webhook or CEL rules):

- an `imagePrefix` ending with `:` (it looks like a "any tag of this image" marker, but the
  single image form already covers it):

  ```yaml
  alternatives:
  - imagePrefix: "quay.io/acme/foo:"        # invalid
  - imagePrefix: "docker.io/acme-org/foo:"  # invalid
  ```

- an `imagePrefix` carrying a tag or a digest (`quay.io/acme/foo:v1`,
  `quay.io/acme/foo@sha256:…`): alternatives describe repositories, the tag or digest comes
  from the pod
- mixing the two forms in the same list, since the two sides would not describe the same
  set of images:

  ```yaml
  alternatives:
  - imagePrefix: "quay.io/acme/foo:"       # invalid (trailing `:`) and mixed forms
  - imagePrefix: docker.io/acme-org/foo/
  ```

  ```yaml
  alternatives:
  - imagePrefix: quay.io/acme/foo          # single image form
  - imagePrefix: docker.io/acme-org/foo/   # subpath form => invalid
  ```

Keeping the two forms explicit and non mixable keeps the CR readable and avoids rewriting an
image to an unrelated one because two repository names happen to share a prefix.

#### Overlapping alternatives across several CRs

Several `ImageAlternative` may match the same image, for instance one declaring
`quay.io/acme/` and another declaring `quay.io/acme/foo`. The **most specific** entry wins,
which is the longest matching prefix, so a single image entry
always wins over a subpath entry that would also match it. Only that CR provides the
alternatives list for the image, lists are not concatenated.

If two CRs match with the same specificity (typically the very same entry declared twice),
the conflict is resolved deterministically (lexicographic order of the CR name) and both CRs
report it in their status, so the ambiguity is visible instead of silently depending on
reconcile order.

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
  excludeImagePrefixes:
  - ghcr.io/foo-bar/

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
      auth:                    # Same schema as `ImageAlternative` `spec.alternatives[].auth`,
                               # except `injectPullSecret` which is ignored here: push credentials
                               # are only used by the controller, never injected in namespaces
        secretRef:
          name: mirror-write-credentials
    pull:                      # Credentials to pull image that will be synced in namespaces
      auth:                    # with rewritten image to use destination registry
                               # Same schema as `ImageAlternative` `spec.alternatives[].auth`
        secretRef:
          name: mirror-read-credentials
        injectPullSecret: true # Default: true (for secretRef), false for `provider`
                               # inject secret in pod namespace when an image is rewritten to this
                               # mirror, so kubelet could pull it from the destination registry

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

  driftDetection: true         # Default: true - Detect if an image tag digest differ from pod running in cluster

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

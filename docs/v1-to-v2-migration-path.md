# v1 to v2 migration path

kube-image-keeper v1 is reaching end of life and will not be maintained anymore. This document describes the key differences between v1 and v2 and how to migrate your setup.

## Key differences

### No more proxy or in-cluster registry

v1 deployed an in-cluster container registry used to cache mirrored images, along with a proxy that served images from either the local registry or the original source if the image was not cached yet. v2 removes both components entirely. There is no more proxy and no more in-cluster registry. This greatly improves stability and simplicity, as image routing is now handled at the mutating webhook level. Images are pulled directly from external registries (mirrors or replicas) without any intermediate layer inside the cluster.

### User-defined CRDs instead of automatic CachedImages

In v1, kube-image-keeper automatically created a `CachedImage` custom resource for each container image it encountered. This was fully automatic, users did not need to declare anything, but re-configuration required to re-deploy kuik.

In v2, **nothing is automatic**. Users must explicitly create their own custom resources to define how images should be mirrored or replicated. The available CRDs are:

- `ClusterImageSetMirror` / `ImageSetMirror` — define mirror destinations for images
- `ClusterReplicatedImageSet` / `ReplicatedImageSet` — declare image equivalence sets

Even if we still use a configuration file, most of the configuration of the tool is done with those CRD, which greatly improve usability.

### ImageSets instead of individual image CRs

In v1, there was a one-to-one relationship: one `CachedImage` CR per container image.

In v2, the model shifts to **ImageSets**. Each CR defines a group of images built through filtering rules (include/exclude patterns) rather than referencing individual images. The list of individual images matched by these rules is presented in the `status` field of each CR.

## Migration path

👷 Work in progress...

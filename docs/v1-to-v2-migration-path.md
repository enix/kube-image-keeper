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

👷 Work in progress... (`excludeLabels` will be implemented in v2.3)

- Setup a registry to replace the one deployed by kuik v1 and configure periodic garbage collect on it
- Create a token to pull, push and delete on the registry and configure as secret with:
```
kubectl -n kuik-system create secret docker-registry my-registry-secret --docker-server=my-registry.company.com --docker-username=my-username --docker-password=my-token
```
- Either you will let kuik progressively populate your new registry as you re-deploy images or if you have image that no longer exist upstream, you can use a tool like [regsync](https://regclient.org/usage/regsync/) to copy images from kuik v1 registry to your new one
- Uninstall KuiK v1 and install KuiK v2
- Create a [*ClusterImageSetMirror*](/docs/crds.md#clusterimagesetmirror) which mirror all images and force rewrite in any case as in kuik v1:
```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageSetMirror
metadata:
  name: global-mirror
spec:
  priority: -1 # Rewrite image even if original one is available
  imageFilter:
    include:
      - ".*" # Match every images
    exclude:
    - localhost[^/]*/.+ # Exclude kuik v1 rewritten images we couldn't mirror
    excludeLabels: # WIP: will be in kuik v2.3
      - kube-image-keeper.enix.io/image-caching-policy=ignore # mirror exclude label from kuik v1
  cleanup: # Cleanup image no longer referenced in cluster after a retetention period
    enabled: true
    retention: 168h # 7d
  mirrors:
    - registry: my-registry.company.com
      path: "/my-project"
      credentialSecret:
        name: my-registry-secret
        namespace: kuik-system
```

Any images deployed on cluster (not already rewritten to kuik v1 image path) will be handled by your new ClusterImageSetMirror and mirrored.

For pods with images already rewritten to localhost kuik v1 registry, the next time the pod will be re-deployed by it's owner (Deployment, DaemonSet, …) kuik will match the original image and will copy it to `my-registry.company.com/my-project` and try to rewrite pod image to use new path.

If you didn't synced from kuik v1 registry, the first time an image is seen, as kuik detect it as not available on our registry (the copy didn't happen yet), it will keep using the original image.

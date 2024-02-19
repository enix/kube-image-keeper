# How to pre-heat to cache

Sometimes, you may want to pre-heat the cache before deploying an application. In order to do this, you need to manually create corresponding `CachedImages`. For instance, if you need to use an nginx image, you could create the following `CachedImage`:

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: CachedImage
metadata:
  name: nginx
spec:
  sourceImage: nginx:1.25
  retain: true
```

## Retain flag / expiration

If you plan to use the image in a long time (more days than configured in the value `cachedImagesExpiryDelay`) set the `spec.retain` flag to `true` to prevent the image from expiring.

## Naming convention

`CachedImage` follow a strict naming convention that is `<registry>-<image>-<tag>`. Giving any name that doesn't match this convention will make the controller recreate the `CachedImage` with the right name. For instance here, it will be renamed `docker.io-library-nginx-1.25`. The reason why the controller do this is to ensure that only one `CachedImage` exists for a particular image and to ensure consistency.

## Pull secrets

If the image you want to put in cache needs a pull secret, you will have to configure it in the corresponding `Repository` (following the same naming convention than a `CachedImage`). For instance:

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: Repository
metadata:
  name: docker.io-library-nginx
spec:
  name: docker.io/library/nginx
  pullSecretsNamespace: secret-namespace
  pullSecretNames:
    - secret-name
```

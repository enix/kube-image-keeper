# How to handle mutable tags

> If one deploys a statefulset for an image like postgres:15 (PostgreSQL database), kube-image-keeper will cache the postgres:15 image the moment a corresponding pod gets created. This exact image is stored inside the kuik registry.
> Now if postgres:15 gets an update, which might be important for security reasons, and a developer tries to upgrade the pods, the cached version will be used and it won't be updated to the newer, security fixed version of postgres:15.
> And that person has to watch the log outputs in depth to find out that there was no update.
>
> For mutable tags like :latest the situation can be even worse as an developer assumes imagePullPolicy: Always. But unfortunately the image never gets an update in the future while kube-image-keeper is actively caching that image. This behavior is clearly completely different from the expected default behavior of imagePullPolicy: Always.
>
> @BernhardGruen in [#156](https://github.com/enix/kube-image-keeper/issues/156)

As described in the above issue, you may want to use mutable tags, but using kuik prevent you from getting updates on those images. We have implemented two features to tackle this issue.

## Filter containers based on their pull policy

Using the value `.Values.controllers.webhook.ignorePullPolicyAlways`, you can ignore rewriting of containers that use the `imagePullPolicy: Always`, keeping them out of kuik and thus staying on the original behavior. It will also ignore images with the tag `:latest`.

The caveat with this method is that you no longer cache corresponding images, so you should enable it carefully.

## Periodic updates

Another option is to periodically update images in the cache based on rules defined in the corresponding `Repository`, using `spec.updateInterval` and `spec.updateFilters` to update `CachedImages` at regular interval. For instance, to update the `:latest` and `:15` tag every hour, you can configure your repository as following:

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: Repository
metadata:
  name: docker.io-library-postgres:15
spec:
  name: docker.io/library/postgres
  updateInterval: 1h
  updateFilters:
    - 'latest'
    - '15'
```

You can also use regexps to match images, for instance if you want to match all major versions, you could use `:\d+$`, which wil match `:14`, `:15`, `:16` (and so on...) but not `:15.8`.

It will then check every hour for updates on tags matching the updateFilters and pull new version of the image if the digest has changed.

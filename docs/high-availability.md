# Storage & high availability

kube-image-keeper supports multiple storage solutions, some of them allowing for high availability of the registry component.

| Name          | HA-compatible | Enable                              |
|---------------|---------------|-------------------------------------|
| Tmpfs         |               | by default                          |
| PVC           |               | `registry.persistence.enabled=true` |
| Minio         |      Yes      | `minio.enabled=true`                |
| S3-compatible |      Yes      | `registry.persistence.s3=...`       |

HA-compatible backends uses a deployment whereas other backends relies on a statefulset.

To enable HA, set `registry.replicas` to a value greater than `1` and make sure to configure an HA-compatible storage backend.

## Tmpfs

This is the default mode, the registry don't use a volume so the data isn't persistent. Garbage collection is disabled.

## Persistent Volume Claim

To enable this mode you just have to set `registry.persistence.enabled` value to `true`.

## Minio

This install the [bitnami minio chart](https://artifacthub.io/packages/helm/bitnami/minio) as a dependency. Values of the subchart can be configured via the `minio` value. To enable this subchart, set `minio.enabled` to `true`. Be aware that minio uses PVCs to store data, so you will have to define a storageClass and a PVC size. It also requires you to set a root password.

Here is an example of values to enable minio (please refers to minio helm chart documentation for more details):

```yaml
minio:
  enabled: true
  auth:
    existingSecret: minio-root-auth
  persistence:
    storageClass: storage-class-name
    size: 10Gi
```

And the root authentication secret:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: minio-root-auth
type: Opaque
data:
  root-username: <base64-encoded-value>
  root-password: <base64-encoded-value>
```

## S3-compatible

Any s3-compatible service can be used as a storage backend, including but not limited to AWS S3 and Minio.

Here is an example of values to enable minio (please refers to [docker registry S3 documentation](https://github.com/docker/docs/blob/main/registry/storage-drivers/s3.md) for more details):

```yaml
registry:
  persistence:
    s3ExistingSecret: secret-name
    s3:
      region: us-east-1
      regionendpoint: http://minio:9000
      bucket: registry
```

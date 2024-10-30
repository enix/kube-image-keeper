# Storage & high availability

The kuik controller can run in active/standby mode. By default, the kuik Helm chart will run two replicas of the controller, which means that we automatically get high availability for that component without needing to configure anything else.

The kuik proxy (which runs on each node, thanks to a DaemonSet) doesn't require any particular HA configuration. Just like e.g. kube-proxy, the kuik proxy only serves its local node, so if a node failure takes down a node's kuik proxy, it doesn't affect other nodes.

The kuik registry, however, is a stateful component, and it requires extra steps if we want to run it in highly available fashion.

The registry supports various storage solutions, some of which enable high availability scenarios.

| Name          | HA-compatible | Enable                              |
|---------------|---------------|-------------------------------------|
| Tmpfs         |      No       | by default                          |
| PVC (RWO)     |      No       | `registry.persistence.enabled=true` |
| PVC (RWX)     |      Yes      | `registry.persistence.enabled=true`, `registry.persistence.accessModes='ReadWriteMany'` |
| MinIO         |      Yes      | `minio.enabled=true`                |
| S3-compatible |      Yes      | `registry.persistence.s3=...`       |
| GCS           |      Yes      | `registry.persistence.gcs=...`      |
| Azure         |      Yes      | `registry.persistence.azure=...`    |

HA-compatible backends uses a deployment whereas other backends relies on a statefulset.

To enable HA, set `registry.replicas` to a value greater than `1` and make sure to configure an HA-compatible storage backend.

## Tmpfs

This is the default mode, the registry don't use a volume so the data isn't persistent. Garbage collection is disabled. In this mode, if the registry Pod fails, a new Pod can be created, but the registry cache will be empty and will need to be re-populated.

## PersistentVolumeClaim (RWO)

By setting the `registry.persistence.enabled` value to `true`, the kuik registry will use a PersistentVolumeClaim. If the PVC itself is backed by a local volume, this won't improve the durability of the registry in case of e.g. complete node failure. However, if the PVC is backed by a network or cloud volume, then the content of the registry cache won't be lost in case of node outage. But with most setups, a node outage might still take down the registry for an extended period of time, until the node is restored or the volume is detached from the node to be reattached to another (the exact procedure may depend on your specific cluster setup). Therefore, the PVC mode is *not* considered highly available here.

## PersistentVolumeClaim (RWX)

By setting the `registry.persistence.enabled` value to `true` AND setting `registry.persistence.accessModes` to `ReadWriteMany` the kuik registry will use a PersistentVolumeClaim in ReadWriteMany mode. This allows multiple pods to use the same volume and therefore this approach is considered highly available.

Only select few storage providers support ReadWriteMany an example of one which does is the [EFS CSI](https://docs.aws.amazon.com/eks/latest/userguide/efs-csi.html) if you are running EKS. This can be useful if you are running kuik within AWS and do not want to run MinIO or create S3 credentials ontop of the deployment.

## S3-compatible

Any S3-compatible service can be used as a storage backend for the registry, including but not limited to AWS S3 and MinIO.

Here is an example of values to use an S3-compatible bucket:

```yaml
registry:
  persistence:
    s3ExistingSecret: secret-name
    s3:
      region: us-east-1
      regionendpoint: http://minio:9000
      bucket: registry
```

Please refer to the [Docker registry S3 documentation](https://github.com/distribution/distribution/blob/main/docs/content/storage-drivers/s3.md) for more details.

Note that when using AWS S3 buckets, you shouldn't prefix the bucket name with `s3://`:

```yaml
registry:
  persistence:
    s3ExistingSecret: secret-name
    s3:
      region: us-east-1
      bucket: mybucket
```

Furthermore, you will need to create a Secret holding the associated secret:

```
kubectl create secret generic secret-name \
        --from-literal=accessKey=${ACCESSKEY} \
        --from-literal=secretKey=${SECRETKEY}
```

If you want to use MinIO and self-host MinIO on your Kubernetes cluster, the kuik Helm chart can help with that! Check the next section for details.

## GCS

Google Cloud Storage can also be used as a storage backend for the registry. Here is an example of values to use GCS:

```yaml
registry:
  persistence:
    gcsExistingSecret: secret-name
    gcs:
      bucket: registry
```

Please refer to the [Docker registry documentation](https://distribution.github.io/distribution/about/configuration/) for more details.

Note that you will need to create a Secret holding the associated service account secret:

```
kubectl create secret generic secret-name \
        --from-literal=credentials.json=${GCS_KEY}
```

### Azure

Microsoft Azure can also be used as a storage backend for the registry. Here is an example of values to use Azure:

```yaml
registry:
  persistence:
    azureExistingSecret: secret-name
    azure:
      container: registry
```

Please refer to the [Docker registry documentation](https://distribution.github.io/distribution/about/configuration/) for more details.

Note that you will need to create a Secret holding the associated service account secret:

```
kubectl create secret generic secret-name \
        --from-literal=accountname=${ACCOUNTNAME} \
        --from-literal=accountkey=${ACCOUNTKEY}
```

## MinIO

The kuik Helm chart has an optional dependency on the [bitnami MinIO chart](https://artifacthub.io/packages/helm/bitnami/minio). The subchart can be enabled by setting `minio.enabled` to `true`, and it can be configured by passing values under the `minio.*` path; for instance, with the following values YAML:

```yaml
minio:
  enabled: true
  auth:
    existingSecret: minio-root-auth
  persistence:
    storageClass: storage-class-name
    size: 10Gi
```

Note that by default, MinIO uses PersistentVolumes to store data, and will obtain them thanks to PersistentVolumeClaims. You don't need to specify the `storageClass` if your cluster has a default StorageClass; and if you don't specify a size, it will use a default size - so you don't have specify these.

However, you **must** specify the `minio.auth.existingSecret` value (or set up authentication somehow) and create the corresponding Secret manually.

Here is an example to create the associated Secret:

```
kubectl create secret generic minio-root-auth \
        --from-literal=root-user=root \
        --from-literal=root-password=valid.cow.accumulator.paperclip
```

(Of course, you should generate your own secure password!)

It is NOT necessary to set `registry.persistence.enabled` to `true` to enable persistence through MinIO.

It is NOT necessary to configure the S3 endpoint when using this solution as it will be configured automatically by the chart.

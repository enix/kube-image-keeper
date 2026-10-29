---
description: What kuik v3 does not do yet, and the workaround when there is one.
---

# Known limits

This page lists what the current version of kuik does not do yet, and how to work around it. The release notes say when a release lifts or adds one.

## Install

- **Cloud credentials**: credentials from a cloud provider (`auth.provider`: `aws`, `gcp`, `azure`) are accepted but not used yet: kuik moves on to the next credential, and never injects them as pull Secrets. Planned for 3.1; use a `secretRef` meanwhile, see [Registry credentials](./configuration.md#registry-credentials).
- **One install per cluster**: the Secret names the secret syncer reserves are cluster-wide, so the syncers of 2 installs would refuse each other's writes, see [Installation](./installation.md).
- **Reconciler first**: the webhook admits pods unchanged until the reconciler has created the ConfigMap `kuik-cluster-id`, so never run with `reconciler.replicas: 0`, see [Cluster ID](./configuration.md#cluster-id).
- **Cluster ID**: deleting the ConfigMap `kuik-cluster-id`, or its namespace, generates a new cluster ID and orphans every tag already copied; never copy it to another cluster that shares a destination, or both delete each other's tags, see [Cluster ID](./configuration.md#cluster-id).
- **Backups**: a restore tool such as Velero is refused the pull Secrets kuik manages; the syncer recreates them, but the restore ends `PartiallyFailed`. Exclude these Secrets from the backup, see [What the syncer may write](./concepts/pull-secrets.md#what-the-syncer-may-write).

## Mirroring

- **Tag deletion**: a destination that never accepts tag deletion holds a deleted `ImageMirror` until `cleanup.enabled` is set to false, see [Deleting an ImageMirror](./crds.md#deleting-an-imagemirror). A refusal for credentials (401, 403) shows on a live `ImageMirror` only as a Warning `ImageDeletionFailed` event, with no condition, and on a deleted one only in the reconciler logs.
- **Push refused with 403**: a destination that refuses a push with a 403, such as a quota refusal, still gets it once per destination credential before the copy backs off, see [Copies](./concepts/pacing.md#copies).
- **Harbor**: Harbor refuses tag deletion through the registry API (any `DELETE` by tag answers 400), so `cleanup` deletes nothing on a Harbor destination. Set `cleanup.enabled: false` on a Harbor destination and use Harbor's tag retention rules; support for tag deletion is requested in [goharbor/harbor#24213](https://github.com/goharbor/harbor/issues/24213).

## Monitoring

- **Drift digests**: drift compares the digest the container runtime reports with the registry's: a runtime that reports a platform or config digest shows a tag as drifted, and a preloaded image is never checked for drift, see the [ImageMonitor reference](./crds.md#imagemonitor).
- **Retention after a restart**: an image whose last pod goes away while no reconciler runs (restart, leader change) is not retained by an `ImageMonitor`, see the [ImageMonitor reference](./crds.md#imagemonitor).

## Registries

- **Node runtime**: the `tls` key and `insecure: true` configure kuik only: configure the nodes' container runtime for the same hosts, or pods fail to pull, see [Registry TLS](./configuration.md#registry-tls).

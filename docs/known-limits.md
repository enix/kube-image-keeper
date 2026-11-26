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

- **Tag deletion**: a destination that never accepts tag deletion holds a deleted `ImageMirror` until `cleanup.enabled` is set to false, or `cleanup.deleteByDigest` to true when that `ImageMirror` is the only writer of its destination and the destination accepts the deletion of a manifest by digest (otherwise it stays held, with `Ready` `False` and reason `RegistryDeleteUnsupported`, see [An ImageMirror stays Terminating](./guides/troubleshooting.md#an-imagemirror-stays-terminating)), see [Deleting an ImageMirror](./crds.md#deleting-an-imagemirror). A refusal for credentials (401, 403) shows on a live `ImageMirror` only as a Warning `ImageDeletionFailed` event, with no condition, and on a deleted one only in the reconciler logs.
- **Retention before the first destination pass**: after a restart, until a first destination pass completes, an image whose last pod goes is retained when its destination repository was already listed in `status.repositories`, even if its own copy was never made: it counts in `images.copy.unavailable` until `cleanup.retention` elapses, see [Excluding images](./crds.md#excluding-images). This happens only when that first pass cannot complete (the destination unreachable or answering 429, or the `ImageMirror` blocked by its selectors or its `manage` credentials): otherwise the first reconcile after the restart runs it at once, before any image is released.
- **Push refused with 403**: a destination that refuses a push with a 403, such as a quota refusal, still gets it once per destination credential before the copy backs off, see [Copies](./concepts/pacing.md#copies).
- **Harbor**: Harbor refuses tag deletion through the registry API (any `DELETE` by tag answers 400), so `cleanup` deletes nothing on a Harbor destination by default; support for tag deletion is requested in [goharbor/harbor#24213](https://github.com/goharbor/harbor/issues/24213). On a Harbor destination the `ImageMirror` is the only writer of, set `cleanup.deleteByDigest: true`: kuik then deletes manifests by digest, which Harbor accepts. On a Harbor destination other clusters, other `ImageMirror`s or other tools write to, deleting by digest removes their tags too, see [Deleting by digest](./crds.md#deleting-by-digest): set `cleanup.enabled: false` there and use Harbor's tag retention rules. Both setups are described in [Harbor as an ImageMirror destination](./guides/harbor-destination.md).
- **Deletions after a restart**: the first destination pass after a reconciler restart deletes the tags whose retention elapsed meanwhile before Prometheus scrapes `kuik_mirror_tags_deleted_total` at 0, so `increase()` and `rate()` miss those deletions; the counter value itself is right, see [Mirror metrics](./observability.md#mirror).

## Monitoring

- **No drift detection**: an `ImageMonitor` does not tell when a tag moves upstream under the pods that run it, since Kubernetes exposes no reliable way to know which digest a container runs ([kubernetes#115199](https://github.com/kubernetes/kubernetes/issues/115199)). An `ImageMirror` under `driftPolicy: Warn` tells when a copied tag moves, see the [ImageMonitor reference](./crds.md#imagemonitor).
- **Retention after a restart**: an image whose last pod goes away while no reconciler runs (restart, leader change) is not retained by an `ImageMonitor`, see the [ImageMonitor reference](./crds.md#imagemonitor).
- **Credentials of a retained image**: an `ImageMonitor` reads a private image with the pull secrets of the pods declaring it (`secretAccess.mode: permissive`, or a namespace `restricted` lists). Once its last pod is gone, the image stays in `status.retainedImages` for `unusedImageRetention`, but no pod is left to take a pull secret from: its checks fall back to `fallbackAuth`, then anonymous. With no `fallbackAuth` entry matching it, the image turns `Unauthorized` in `status.unavailableImages` and `kuik_image_origin_unavailable`, and emits `AnonymousAccessDenied`, though nothing is broken. The alternatives only retained origins offer lose the same pull secrets and keep the `auth` of their entries and `fallbackAuth`: one that needs a pull secret turns `Unauthorized` too, and emits `AlternativeUnusable`. Declare a `fallbackAuth` entry for the registry of the image, and of such an alternative unless its entry carries an `auth`, see [`fallbackAuth`](./configuration.md#fallbackauth).

## Registries

- **Node runtime**: the `tls` key and `insecure: true` configure kuik only: configure the nodes' container runtime for the same hosts, or pods fail to pull, see [Registry TLS](./configuration.md#registry-tls).
- **Request interval and check timeout**: kuik does not check that `check.timeout` leaves room for `requestInterval`: a too large interval makes every check of that host time out as `Unreachable`. Give a paced host a `check.timeout` of at least 6 times its `requestInterval` per credential tried, see [Spacing the requests](./concepts/pacing.md#spacing-the-requests).

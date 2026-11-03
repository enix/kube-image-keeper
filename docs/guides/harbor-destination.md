---
description: Use a Harbor project as the destination of an ImageMirror, with the cleanup that fits whether kuik is its only writer or not.
---

# Harbor as an ImageMirror destination

Harbor refuses tag deletion through the registry API: any `DELETE /v2/<name>/manifests/<tag>` answers 400 ([goharbor/harbor#24213](https://github.com/goharbor/harbor/issues/24213)). With the default `cleanup` of an [`ImageMirror`](../crds.md#imagemirror), kuik therefore deletes nothing on Harbor: each deletion fails with an `ImageDeletionFailed` event, and `Ready` goes `False` with reason `RegistryDeleteUnsupported`. Pick one of the 2 setups below, depending on who else writes to the destination.

## Who writes to the destination

The destination is the Harbor project, or the path inside it, that `destination.path` names: `harbor.example.com/kuik-prod/` writes every copy under the project `kuik-prod`. kuik writes there with the `manage` credential, which needs to push, pull and delete in that project.

- **The `ImageMirror` is the only writer**: one cluster, one `ImageMirror`, no other tool and no tag pushed by hand under that path. Use [setup A](#setup-a-the-imagemirror-is-the-only-writer).
- **Anything else writes there**: several clusters sharing the destination, another `ImageMirror` on the same path, a CI pipeline or a replication rule. Use [setup B](#setup-b-the-destination-is-shared).

## Setup A: the ImageMirror is the only writer

Set `cleanup.deleteByDigest: true`. kuik then deletes a tag by deleting the manifest it points at, which Harbor accepts, and keeps a manifest another of its own tags still needs. See [Deleting by digest](../crds.md#deleting-by-digest) for how it works.

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ImageMirror
metadata:
  name: harbor-mirror
spec:
  destination:
    path: harbor.example.com/kuik-prod/
    manage:
      auth:
        secretRef:
          name: harbor-manage
  cleanup:
    retention: 168h
    deleteByDigest: true
```

Deleting a manifest frees no space by itself: Harbor's own garbage collection removes the blobs no manifest uses any more, so schedule it on the Harbor side.

> [!WARNING]
> Deleting a manifest removes every tag pointing at it, whoever wrote them. As soon as a second cluster, another `ImageMirror` or another tool writes under the same path, deleting by digest removes their tags too: switch to setup B. See the warning of [Deleting by digest](../crds.md#deleting-by-digest).

## Setup B: the destination is shared

Turn kuik's cleanup off, and let Harbor's tag retention delete the tags of this cluster.

```yaml
spec:
  cleanup:
    enabled: false
```

Every tag an `ImageMirror` writes ends with `_<clusterID>`, the [cluster ID](../configuration.md#cluster-id) of the cluster that wrote it: `nginx:1.27` is copied as `nginx:1.27_focused-turing-7f3a`. That suffix tells this cluster's tags apart from the others'. Read the ID of this cluster:

```bash
kubectl get configmap kuik-cluster-id -n kuik-system -o jsonpath='{.data.clusterID}'
```

Then add [tag retention rules](https://goharbor.io/docs/main/working-with-projects/working-with-images/create-tag-retention-rules/) to the Harbor project:

- **A rule for this cluster's tags**: repositories matching the destination path inside the project (`**` when the `ImageMirror` owns the whole project), tags matching `*_<clusterID>`, and a generous condition, such as "retain the most recently pushed N artifacts" per repository or a long "pushed within the last N days" window. Size it to how long a copy must outlive its origin.
  Retention rules cannot see whether pods still need a copy. A rule on pull time misses it: under `rewritePolicy: OnFailure`, pods pull the origin while it answers, so Harbor sees a pull of the copy only during an outage, and kuik's own reads of the destination are `HEAD`s, which Harbor does not record as pulls. Such a rule can delete the copy the next outage needs.
- **A rule for every other tag**: Harbor retention rules list what to keep, and a tag no rule retains is deleted when the policy runs. Add a rule that always retains the tags excluding `*_<clusterID>`, so the policy never touches the tags of the other clusters or tools.

The rules of a project add up: a tag is kept when any rule retains it. When several clusters share the project, they all take setup B, and the project holds one policy for all of them: one rule per cluster on its own suffix, each with its own condition, and one rule that always retains the tags carrying none of these suffixes. A rule for this cluster never matches another cluster's suffix: only that cluster knows which of its tags its pods still use.

> [!TIP]
> Since the clusters share the destination, the same image often has one manifest carrying several clusters' tags. Check how your Harbor version applies retention to an artifact several tags point at, and run the policy as a dry run before scheduling it.

A tag the retention deletes while a pod of this cluster still runs its image is written again after the next destination pass (`mirror.destinationScan.interval`), only when its origin or an `ImageAlternative` covering it answers: kuik reads the image from that source, then writes the tag alone when its manifest is still at the destination, or a full copy with an `ImageRecopied` warning. A copy deleted while its origin is down is not restored until a source answers again: that outage is what the mirror was for. Until then, new pods are routed past the missing copy, and a node pulling it again for a pod already rewritten to it fails.

## Deleting the ImageMirror

With `cleanup.enabled` and neither setup, a deleted `ImageMirror` on Harbor stays `Terminating`: kuik keeps trying to delete its tags, and Harbor keeps refusing. Set `cleanup.enabled: false` on it to release it, leaving its tags to Harbor's retention, or `cleanup.deleteByDigest: true` when it is the only writer of its destination. See [An `ImageMirror` stays `Terminating`](./troubleshooting.md#an-imagemirror-stays-terminating).

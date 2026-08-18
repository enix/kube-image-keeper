# Troubleshooting

## Stale tag caches on pull-through proxies

**Symptom:** kuik reports an image as available (and keeps routing pods to it), but pods pulling it fail with `manifest unknown` or `not found` on every node that does not already have the image in its local store.

By default, the availability check does what a plain `docker pull` of a tag starts with: one `HEAD` (or `GET`) on the tag. Container runtimes do more, they resolve the tag to a manifest digest, then fetch the manifest *by that digest*. Some registries answer the two requests inconsistently:

- a pull-through proxy whose upstream image was deleted or garbage-collected keeps serving the cached tag while the manifest behind it is gone;
- a scanner-gated registry (Artifactory with Xray, for instance) can block a specific manifest by digest while the tag lookup still succeeds.

In both cases the tag request returns `200`, kuik marks the image `Available`, and the pull fails anyway. Setting `resolveDigest: true` makes kuik follow the same two-step path as the runtime. A `404` on the digest request is reported as `NotFound` with a `tag/digest inconsistency` message, which triggers the usual fallback to the next alternative and the re-mirror path.

The check is opt-in because it **doubles the number of registry requests** for every checked tag reference (references already pinned to a digest still cost a single request). Two things must be kept in mind when enabling it:

- **`routing.activeCheck.timeout`** — the timeout applies to each request individually, so a tag check can take up to `2 × timeout` of wall time before the webhook falls back to the next alternative. Lower it if admission latency matters more than probe reliability.
- **`monitoring.registries.*.maxPerInterval`** — this counts *images checked*, not requests sent. With `resolveDigest`, each tag-referenced image costs two requests, so halve the value on rate-constrained registries (Docker Hub anonymous pulls, for example) to keep the same request budget.

```yaml
routing:
  activeCheck:
    resolveDigest: true # each of the two requests gets the full activeCheck timeout

monitoring:
  registries:
    items:
      docker.io:
        resolveDigest: true
        interval: 1h
        maxPerInterval: 3 # halved from 6, each tag check now costs two requests
```

`routing.activeCheck.resolveDigest` (webhook) and `monitoring.registries.*.resolveDigest` (`ClusterImageSetAvailability` probes) are independent, enable whichever surface you need. The per-registry value is a three-state boolean: unset inherits `monitoring.registries.default`, `false` opts a single registry out of an enabled default.

> [!NOTE]
> Registries that answer `405` or `501` to a **HEAD** manifest-by-digest request are treated as available: HEAD support on the digest path is optional for proxies, and they still serve the image fine to container runtimes. The same answer to a **GET** probe is reported as unreachable, since runtimes pull with GET.

## Duplicated credential secrets (`kuik-kuik-...`)

When a `(Cluster)ImageSetMirror` or `(Cluster)ReplicatedImageSet` declares a `credentialSecret`, kuik copies it into every namespace where a matching image is rerouted, naming the copy `kuik-<secret-name>-<hash>`.

A bug ([#604](https://github.com/enix/kube-image-keeper/issues/604)) caused these copies to be duplicated over time, each duplicate prefixed by an extra `kuik-`:

```console
$ kubectl get secrets -A -l kuik.enix.io/owner-name=my-registry-creds
NAMESPACE   NAME
app-a       kuik-my-registry-creds-182d49977813a14c
app-b       kuik-kuik-my-registry-creds-182d49977813a14c-5d5356fbcd468beb
app-c       kuik-kuik-kuik-my-registry-creds-182d49977813a14c-...-f8e079be1f599921
```

**This is fixed in v2.3.0**, so no new duplicates are created once you upgrade. However, existing duplicates are not removed automatically, you should clean them up manually after upgrading.

List the duplicates first (review the output):

```bash
kubectl get secrets -A -l kuik.enix.io/owner-name \
  --no-headers -o custom-columns=NS:.metadata.namespace,NAME:.metadata.name \
  | awk '$2 ~ /^kuik-kuik-/'
```

Then delete them:

```bash
kubectl get secrets -A -l kuik.enix.io/owner-name \
  --no-headers -o custom-columns=NS:.metadata.namespace,NAME:.metadata.name \
  | awk '$2 ~ /^kuik-kuik-/ { print $1, $2 }' \
  | while read -r ns name; do
      kubectl -n "$ns" delete secret "$name"
    done
```

Pods that were mutated to reference one of the deleted secrets keep the stale `imagePullSecrets` entry until they are recreated. List the affected pods (namespace, pod, and the referenced kuik secrets):

```bash
kubectl get pods -A -o json \
    | jq -r '.items[]
        | .metadata.namespace as $ns
        | .metadata.name as $pod
        | [ (.spec.imagePullSecrets // [])[].name
            | select(startswith("kuik-kuik-")) ] as $secrets
        | select($secrets | length > 0)
        | "\($ns)\t\($pod)\t\($secrets | join(","))"' \
    | sort -u | column -t
```

Recreate the listed pods (for example by rolling out their owning Deployment/StatefulSet/DaemonSet) so the webhook re-mutates them with the correct `kuik-...` secret.

# Troubleshooting

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

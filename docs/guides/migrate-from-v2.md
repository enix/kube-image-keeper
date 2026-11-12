---
description: Move a cluster from kube-image-keeper v2 to v3, starting with the migrate command that saves the v2 state before anything changes.
sidebar:
  order: 0
---

# Migrate from v2

kuik v3 shares nothing with v2 but its API group: new resources, a new mirror layout, new pull secrets. Nothing converts from one to the other on its own. The `migrate` command of the kuik binary reads your cluster, saves what v2 knows in a local file, and prints what it found.

> [!IMPORTANT]
> Run `migrate` first, while v2 still runs, before you change anything. Once v2 is uninstalled, the cluster no longer tells which origin each mirrored image came from: the state file is the only record of it.

## What migrate does

`migrate` only reads the cluster. It never creates, changes or deletes an object, lists Secrets by their name and labels without reading their content, and never contacts a registry. It needs your kubeconfig to read pods, workloads, Deployments, webhook configurations, CRDs and the kuik resources in every namespace.

Each run prints:

- the v2 installation: its manager Deployment and version, its webhook configuration and the v2 CRDs left, the 2.0 prerelease ones included;
- every v2 resource, with its finalizers;
- the pods whose images v2 rewrote to a mirror, with the original image of each container and the workload they belong to, and the pods that use a pull secret v2 copied;
- the pull secrets v2 copied into namespaces;
- what runs of v3, if anything: its cluster ID, its webhook, reconciler and secret-syncer, and the `Ready` condition of each v3 resource.

The original image of a container comes from the `kuik.enix.io/original-images` annotation v2 sets on a pod, and from the template of the pod's workload when v2 did not record it (init containers, pods admitted by v2.0 and 2.1). `migrate` warns when the two disagree.

## Run it

Build the kuik binary from the tag of the v3 release you will install, with `go build -o kuik ./cmd`, then run it from a directory you keep:

```bash
kuik migrate --kubeconfig ~/.kube/config
```

Without `--kubeconfig`, it reads `KUBECONFIG`, then the in-cluster config when it runs in a pod, then `~/.kube/config`, like any controller-runtime client. The binary is also the entrypoint of the kuik image, which runs it without a Go toolchain:

```bash
docker run --rm --network host --user "$(id -u):$(id -g)" \
  -v "$HOME/.kube/config:/kubeconfig:ro" -v "$PWD:/work" -w /work \
  quay.io/enix/kube-image-keeper:$VERSION migrate --kubeconfig /kubeconfig
```

A kubeconfig that points at certificate files by path needs them mounted at the same path too. `--network host` reaches an API server on `127.0.0.1` (a Kind cluster) on Linux only, not with Docker Desktop. The image holds no cloud authentication plugin: with a kubeconfig that calls one (`aws`, `gcloud`, `kubelogin`), build the binary instead.

| Flag | Default | Meaning |
| ---- | ------- | ------- |
| `--state-file` | `kuik-migration-state.json`, in the current directory | Where the state is saved. |
| `--namespace` | `kuik-system` | The namespace v3 is, or will be, installed in. |
| `--kubeconfig` | see above | The kubeconfig to read the cluster with. |

## Keep the state file

The first run writes `kuik-migration-state.json`. Each later run while v2 runs completes it: it adds the pods rewritten since, never drops one, and takes the latest status of each v2 resource. Once v2 no longer runs, the file is left as it is.

Keep it, and run `migrate` from the same directory each time. If v2 is already gone and no state file exists, `migrate` stops and says so: the migration cannot go on from the cluster alone.

## Exit code

| Code | Meaning |
| ---- | ------- |
| `0` | The cluster holds no trace of v2 and there is no state file: nothing to migrate. |
| `1` | The migration is not complete, or cannot go on without the state file. |
| `2` | The cluster or the state file could not be read or written. |
| `64` | A flag is wrong, or `-h` printed the usage: nothing was read. |

Errors, and what stops the migration, go to the standard error; the rest of the report goes to the standard output.

---
description: How kuik is configured, through its Helm values and its global config file, and how it reads registry credentials.
---

# Operator configuration

kuik runs as 3 processes, each deployed by the chart as its own Deployment:

- `webhook` answers admission requests and routes the images of the pods being created
- `reconciler` runs the mirror, monitor and status loops
- `secret-syncer` materialises and renews the injected pull secrets

They read the same [global config file](#global-config-file) and share the
[cluster resource namespace](#cluster-resource-namespace). The custom resources are documented in
[`crds.md`](./crds.md).

## Global config file

The global config file configures the operator itself, where the custom resources describe what it
should do. The chart renders it into a ConfigMap from the `config` value and mounts it in the 3
processes at `/etc/kuik/config.yaml`. The `--config` flag of each process names it.

```yaml
# values.yaml
config:
  clusterID: prod-eu
  registries:
    docker.io:
      copy:
        interval: 10m
  fallbackAuth:
  - repositoryGroup: private-registry.tld/project1
    secretRef:
      name: project1-creds
```

The file is validated whole:

- a key it does not define, at any level, rejects the file
- a process does not start on a file that does not validate
- a change is reloaded in place, without a restart. A reload that does not validate is refused
  whole: the previous config stays in effect, the failure is logged and counted in
  `kuik_config_reload_errors_total`
- `clusterID` and `metrics` are read at startup only: a reload that changes them is logged and
  ignored until the process restarts

### Keys

A key left out takes the default below.

| Key | Default | Effect |
| --- | ------- | ------ |
| `clusterID` | none, required | Identity of this cluster, appended to every tag an `ImageMirror` writes, matching `^[a-zA-Z0-9][a-zA-Z0-9.-]*$`. Pick a short name that stays stable for the life of the cluster: changing it orphans every tag written under the previous one, and two clusters sharing a mirror destination need different ones. The chart fails to render without `config.clusterID` |
| `metrics.copyDuration` | `false` | Histogram of how long each copy took, per `ImageMirror` |
| `mirror.destinationScan.interval` | `1h` | How often each `ImageMirror` re-reads its own destination |
| `webhook.demoteMirrorWithPullPolicyAlways` | `true` | Demote a mirror candidate for a container with `imagePullPolicy: Always` |
| `webhook.availabilityCheck.timeout` | `2s` | Time before the webhook considers a candidate unavailable |
| `webhook.availabilityCheck.activeCheckCache.ttl` | `10s` | Lifetime of an active check result, per webhook replica |
| `webhook.availabilityCheck.demoteKnownFailures` | `true` | Try last a candidate the background loops report failing |
| `registries.default.check` | `interval: 1m`, `timeout: 10s` | Pace of the background checks against every host |
| `registries.default.copy` | `interval: 3m`, `timeout: 0` | Pace of the copies from every host. A `timeout` of `0` means no bound |
| `registries.<host>` | `registries.default` | Pace of one host, field by field: a host block overrides the fields it names and inherits the others. `index.docker.io` and `docker.io` are the same host |
| `fallbackAuth` | none | Credentials the reconciler reads an image with when no custom resource declares any, see [Registry credentials](#registry-credentials) |

Intervals are at least `5s`. Timeouts are positive, except `copy.timeout`, which may be `0`.
Durations are written with their unit (`30s`, `10m`, `1h`).

## Registry credentials

kuik tries the credentials it holds for an image in this order:

1. the `auth` the custom resource declares for that image: an `ImageAlternative` entry, or an `ImageMirror` destination
2. the pod's `imagePullSecrets`, read in the pod's namespace
3. the most specific `fallbackAuth` entry matching the image
4. anonymous

The webhook skips step 3. Its check predicts what the node can pull, and the node never holds a
`fallbackAuth` credential: kuik never injects one.

### `fallbackAuth`

Each entry names the images it covers the way an `ImageAlternative` entry does, fully qualified,
hostname included:

- `repository` covers that exact repository, whatever the tag or digest
- `repositoryGroup` covers every repository under that path, at any depth. A host alone covers the
  whole host

An entry carries a `secretRef` (a `kubernetes.io/dockerconfigjson` Secret) or a `provider`, with no
`auth` wrapper. The two forms may be mixed in the list. When several entries match, the most
specific wins: a `repository` beats a `repositoryGroup`, and a deeper group beats a shallower one.

> [!NOTE]
> A `provider` (`aws`, `gcp` or `azure`) is accepted but not used yet: kuik skips it and moves on
> to the next step.

### `secretAccess`

`secretAccess` is a chart value, not a key of the global config file. It decides whether step 2
can succeed:

- `permissive` (the default) lets the webhook and the reconciler read Secrets in every namespace,
  so a private registry works with nothing declared
- `restricted` grants that read only in the namespaces listed in `secretAccess.namespaces`.
  Elsewhere the API server refuses it, and kuik moves on to step 3 as if the pod declared no pull
  secret. Declare the credentials of your private registries in `fallbackAuth` instead

The secret syncer never reads Secrets outside the cluster resource namespace, in either mode.

## Cluster resource namespace

Every `secretRef`, on a custom resource or in `fallbackAuth`, resolves in a single namespace: the
cluster resource namespace. A `secretRef` carries no namespace, so referencing a Secret requires
being able to write it there. The `--cluster-resource-namespace` flag of each process names it,
`kuik-system` by default. The chart always sets it to the release namespace.

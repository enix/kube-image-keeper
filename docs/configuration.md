---
description: How kuik is configured, through its Helm values and its global config file, and how it reads registry credentials.
---

# Operator configuration

kuik runs as 3 processes, each deployed by the chart as its own Deployment:

- `webhook` answers admission requests and routes the images of the pods being created
- `reconciler` runs the mirror, monitor and status loops
- `secret-syncer` materialises and renews the injected pull secrets

They read the same [global config file](#global-config-file) and share the
[cluster resource namespace](#cluster-resource-namespace); the reconciler and the webhook also
share the [cluster ID](#cluster-id). The custom resources are documented in
[`crds.md`](./crds.md).

## Global config file

The global config file configures the operator itself, where the custom resources describe what it
should do. The chart renders it into a ConfigMap from the `config` value and mounts it in the 3
processes at `/etc/kuik/config.yaml`. The `--config` flag of each process names it.

```yaml
# values.yaml
config:
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
- `metrics` is read at startup only: a reload that changes it is logged and ignored until the
  process restarts
- `clusterID` is not a key: kuik generates the [cluster ID](#cluster-id), and a file that still
  sets it is refused, see
  [Upgrading from a configured `clusterID`](#upgrading-from-a-configured-clusterid)

### Keys

A key left out takes the default below.

| Key | Default | Effect |
| --- | ------- | ------ |
| `metrics.copyDuration` | `false` | Histogram of how long each copy took, per `ImageMirror` |
| `mirror.destinationScan.interval` | `1h` | How often each `ImageMirror` re-reads its own destination |
| `secretSyncer.onFailureGrace` | `15m` | How long an entry of an injected pull Secret stays once no live pod uses it, for a `rewritePolicy: OnFailure` resource: the next pod of a rollout or a scale-up during an outage still finds the credential. Held in memory: a restart of the secret syncer drops the entries in their grace. Must be positive |
| `webhook.demoteMirrorWithPullPolicyAlways` | `true` | Demote a mirror candidate for a container with `imagePullPolicy: Always` |
| `webhook.availabilityCheck.timeout` | `2s` | Time before the webhook considers a candidate unavailable |
| `webhook.availabilityCheck.activeCheckCache.ttl` | `10s` | Lifetime of an active check result, per webhook replica |
| `webhook.availabilityCheck.demoteKnownFailures` | `true` | Try last a candidate the background loops report failing, see [`rewritePolicy`](./crds.md#rewritepolicy) |
| `registries.default.check` | `interval: 1m`, `timeout: 10s` | Pace of the background checks against every host |
| `registries.default.copy` | `interval: 3m`, `timeout: 0` | Pace of the copies from every host. A `timeout` of `0` means no bound |
| `registries.<host>` | `registries.default` | Pace of one host, field by field: a host block overrides the fields it names and inherits the others. `index.docker.io` and `docker.io` are the same host |
| `fallbackAuth` | none | Credentials the reconciler reads an image with when no custom resource declares any, see [Registry credentials](#registry-credentials) |
| `tls.<host>.caSecretRef` | none | A Secret of the cluster resource namespace whose PEM bundle kuik adds to the system trust store for that host: `name`, and `key` (default `ca.crt`). See [Registry TLS](#registry-tls) |
| `tls.<host>.insecureSkipVerify` | `false` | Reach that host over HTTPS without verifying its certificate, as `curl -k`. Exclusive with `caSecretRef` |

Intervals are at least `5s`. Timeouts are positive, except `copy.timeout`, which may be `0`.
Durations are written with their unit (`30s`, `10m`, `1h`).

How `registries` paces each host, and what a reload does to it, is explained in
[Registry pacing](./concepts/pacing.md).

## Cluster ID

The cluster ID is the identity of this cluster, appended as `_<clusterID>` to every tag an
`ImageMirror` writes, so that several clusters can share one destination. kuik generates it: on
its first start, the reconciler creates the ConfigMap `kuik-cluster-id` in the
[cluster resource namespace](#cluster-resource-namespace), with the ID under the key
`clusterID`, and reads it back on every later start. It never rewrites the ConfigMap, and the
chart does not render it, so `helm upgrade` and `helm uninstall` leave it in place.

A generated ID is an adjective and a surname of the Docker container name generator joined by
`-`, then `-` and 4 random hexadecimal characters: `focused-turing-7f3a`.

The reconciler does not start when the ConfigMap holds no `clusterID`, or a value that does not
match `^[a-zA-Z0-9][a-zA-Z0-9.-]*$` or is longer than 56 characters: fix the ConfigMap, kuik
never replaces the value. The webhook only reads the ConfigMap, once at startup: until it has
read a valid ID, it reports not ready and admits pods unchanged.

> [!IMPORTANT]
> The reconciler must have run at least once. Until it has created `kuik-cluster-id`, the
> webhook stays unready and admits every pod unchanged, `ImageAlternative` routing included.
> With `reconciler.replicas: 0`, the webhook never becomes ready.

> [!WARNING]
> Deleting the ConfigMap, or the namespace that holds it, changes the cluster ID. The next start
> generates a new one, and every tag already copied under the previous one is orphaned: kuik no
> longer recognises it as its own, so it never verifies nor deletes it. Keep a copy of the value,
> and restore the ConfigMap before kuik starts again. The reconciler and the webhook read the ID
> once at startup: after restoring the value, restart both, or a process that started in between
> keeps the ID it read or generated then.

To choose the ID, create the ConfigMap before the first install.

> [!CAUTION]
> Two clusters sharing a destination need different IDs: never copy `kuik-cluster-id` to another
> cluster, whether through a backup restored elsewhere, a GitOps repository or a ConfigMap created
> before the install with a value already in use. Each cluster deletes the tags carrying its own
> suffix, so 2 clusters with the same ID delete each other's tags.

### Upgrading from a configured `clusterID`

Up to 3.0.0-alpha.5, `clusterID` was a key of the global config, set by `config.clusterID` in the
chart values. A file that still sets it is now refused, and an upgrade without the ConfigMap
would generate a new ID, orphaning every tag the mirrors copied. Before upgrading, read the old
value from the global config the release renders, `<release>-config` (or
`<release>-kube-image-keeper-config` when the release name does not contain
`kube-image-keeper`):

```sh
kubectl get configmap <release>-config -n <namespace> -o jsonpath='{.data.config\.yaml}'
```

Then create the ConfigMap with that value, in the release namespace:

```sh
kubectl create configmap kuik-cluster-id -n <namespace> --from-literal=clusterID=<old value>
```

Last, remove `config.clusterID` from your values and upgrade: the chart refuses to render while
it is set. With a values file, delete the key. A release upgraded with `--reuse-values` keeps the
old value from its stored values: add `--set config.clusterID=null`.

## Registry credentials

kuik tries the credentials it holds for an image in this order:

1. the `auth` the custom resource declares for that image: an `ImageAlternative` entry, or an `ImageMirror` destination
2. the pod's `imagePullSecrets`, read in the pod's namespace
3. the most specific `fallbackAuth` entry matching the image
4. anonymous

The webhook skips step 3. Its check predicts what the node can pull, and the node never holds a
`fallbackAuth` credential: kuik never injects one.

An `ImageMonitor` check has no step 1, an origin declaring no `auth`. Step 2 tries the pull
secrets of every pod declaring the image, each once, until one answers. A `fallbackAuth` Secret
that is missing or not a `dockerconfigjson` does not stop the check: like the kubelet with a
missing pull secret, kuik goes on without it, and the monitor's `Ready` goes `False` with reason
`SecretNotFound` or `SecretMalformed`.

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

## Registry TLS

kuik verifies the certificate of every registry against the system trust store. The `tls` key
changes that for a host whose certificate a private CA signs, or that is self-signed:

```yaml
# values.yaml
config:
  tls:
    harbor.corp.tld:             # private CA
      caSecretRef:
        name: harbor-ca          # a Secret in the cluster resource namespace
        key: ca.crt              # default: ca.crt, a PEM bundle of one or more certificates
    registry.lab.tld:5000:       # self-signed certificate
      insecureSkipVerify: true
```

- A key matches a registry host exactly, port included, as containerd's `certs.d/<host:port>`
  does: `harbor.corp.tld` and `harbor.corp.tld:443` are 2 different keys, and an image written
  `harbor.corp.tld:443/app` takes the second. A `docker.io` key applies to `index.docker.io`,
  where Docker Hub requests go. Keys are lowercased, and a host written twice rejects the file
- `caSecretRef` adds the certificates of its bundle to the system trust store, for that host
  only. Several CAs go in the same bundle. Every PEM block of the bundle must be a
  certificate; text outside the blocks, such as comments, is ignored
- `insecureSkipVerify: true` keeps HTTPS without verifying the certificate. It cannot be set
  together with `caSecretRef`
- A host with an entry is only reached over HTTPS, including on a loopback or private address.
  An entry that sets neither field is no entry: its host is verified against the system trust
  store, and reached over plain HTTP on a loopback or private address, as without one
- The entry applies to every request kuik sends to that host: the webhook's checks, the
  `ImageMonitor` checks, the `ImageMirror` copies (source and destination), destination scans,
  cleanups and deletions. A redirect or a token server follows the entry of the host it goes to,
  not the one of the registry. An HTTPS proxy on the way is verified against the system trust
  store, as without an entry, and must be named by a host name other than the registry's: a
  request through an HTTPS proxy addressed by IP, or sharing the registry's host name on
  another port, is refused
- A reload of the file or a change of the CA Secret applies without a restart. The webhook and
  the reconciler report ready once they have read the CA Secrets, and a request sent before
  waits for them
- A CA Secret that is missing, lacks its key or whose bundle is invalid makes its host
  unreachable (`Unreachable`, or `SourceUnreachable` / `DestinationUnreachable` on a copy), with
  an error naming the Secret in the logs. kuik never falls back to a weaker verification
- An `ImageAlternative` alternative or an `ImageMirror` destination with `insecure: true` still
  reaches its registry over plain HTTP, and ignores the `tls` entry of its host. Other entries on
  the same host keep the `tls` entry

> [!IMPORTANT]
> The `tls` key configures kuik only. The container runtime of each node pulls the images
> itself: configure it to trust the same CA (containerd's `certs.d`, CRI-O's
> `/etc/containers/certs.d`), or the pods fail to pull from a registry kuik reaches.

## Cluster resource namespace

Every `secretRef`, on a custom resource or in `fallbackAuth`, resolves in a single namespace: the
cluster resource namespace. A `secretRef` carries no namespace, so referencing a Secret requires
being able to write it there. The `--cluster-resource-namespace` flag of each process names it,
`kuik-system` by default. The chart always sets it to the release namespace.

# Trusted Mirrors

By default, kube-image-keeper's webhook performs an availability probe (HTTP HEAD/GET request) on each alternative image before using it. This ensures only reachable images are selected, but it has a limitation: the probe runs from the **manager pod**, not from the node runtime.

This limitation matters when using upstreams that are only reachable from nodes, such as:

- `localhost:<port>` proxies served by a DaemonSet on each node
- Mirrors with self-signed TLS certificates that kubelet trusts but the manager pod doesn't
- Mirrors that require node-local credentials or certificates

For self-signed certs or plain-HTTP registries that the manager *could* reach if its TLS config allowed it, prefer extending the manager trust store via the [`insecureRegistries` / `rootCertificateAuthorities`](configuration.md) Helm values instead of bypassing the probe.

## Scope

The `skipActiveCheck` flag is exposed on `ClusterReplicatedImageSet` (CRIS) and `ReplicatedImageSet` (RIS) — i.e. the pull-through-proxy CRs where each node pulls directly from the upstream.

It is **not** available on `(Cluster)ImageSetMirror` (ISM/CISM): those CRs have a manager-side mirroring controller that pushes images via `client.CopyImage()`. If the manager cannot reach a destination, the mirror would never be populated and pods would hit `ImagePullBackOff` — a broken configuration. Use CRIS/RIS for the node-only-reachable case.

## `skipActiveCheck` flag

When set to `true`, the pod-mutation webhook stops checking that upstream at admission time and treats it as available.

**Scope**: this flag only affects the webhook's routing decision — i.e. which alternative image gets pinned into the pod spec. The replication controllers keep doing their normal work; none of that is short-circuited.

### Placement

The flag can be set at two levels:

1. **CR-level**: `spec.skipActiveCheck` on `(Cluster)ReplicatedImageSet`
2. **Per-upstream**: `upstreams[].skipActiveCheck`

When both are set, the per-upstream value takes precedence. If neither is set, the default behavior (probe all images) applies.

### Using the flag

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterReplicatedImageSet
metadata:
  name: localhost-proxy
spec:
  # Skip probe for all upstreams in this CR
  skipActiveCheck: true
  upstreams:
  - registry: registry.example.com
    path: ""
    imageFilter:
      include:
        - .+
  - registry: localhost
    path: /kuik-cache
    imageFilter:
      include:
        - .+
    # Override: probe this specific upstream even though CR-level skips
    skipActiveCheck: false
  - registry: localhost
    path: /kuik-backup
    imageFilter:
      include:
        - .+
    # Inherits skipActiveCheck: true from CR level
```

## Example: `localhost:<port>` with DaemonSet proxy

This pattern is useful when you want each node to pull through a local registry proxy. A DaemonSet runs a proxy on every node, listening on `localhost:5000`.

> ⚠️ **Kubelet still needs to reach `localhost:5000`.** `skipActiveCheck` only stops the *manager-side* probe; the *node-side* pull is unchanged. By default kubelet/containerd talks HTTPS to any registry, so plain-HTTP `localhost:5000` will fail with `http: server gave HTTP response to HTTPS client` or `x509: certificate signed by unknown authority`. You need **one** of:
>
> - **containerd configured to treat `localhost:5000` as insecure** (e.g. a `hosts.toml` entry under `/etc/containerd/certs.d/localhost:5000/` with `skip_verify = true` or `plaintext = true`). Requires node-level access.
> - **The proxy terminates TLS** with a certificate trusted by kubelet (typically signed by a CA already in the node trust store). This is the only option on managed Kubernetes without containerd access.
>
> The DaemonSet manifest below serves plain HTTP for brevity — adapt it to your environment.

### Deploy the DaemonSet proxy

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: kuik-local-proxy
  namespace: kuik-system
spec:
  selector:
    matchLabels:
      app: kuik-local-proxy
  template:
    metadata:
      labels:
        app: kuik-local-proxy
    spec:
      containers:
      - name: proxy
        image: registry:2.8
        ports:
        - containerPort: 5000
          # hostPort binds the container port to the node's loopback so kubelet
          # can reach `localhost:5000`. Do not combine with `hostNetwork: true`.
          hostPort: 5000
```

### Configure ClusterReplicatedImageSet to use it

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterReplicatedImageSet
metadata:
  name: localhost-mirror
spec:
  # Trust localhost proxy - manager can't reach it but kubelet can
  skipActiveCheck: true
  upstreams:
  # Match the images that should be rewritten to the local proxy. Keep this
  # narrow enough to exclude the proxy's own image, otherwise the webhook
  # rewrites `docker.io/library/registry:2.8` and the DaemonSet cannot
  # bootstrap on a fresh node.
  - registry: registry.example.com
    path: ""
    imageFilter:
      include:
        - .+
  - registry: localhost
    path: /kuik-cache
    imageFilter:
      include:
        - .+
    priority: 1
```

When a pod is scheduled on a node, kubelet pulls from `localhost:5000` on that same node (where the DaemonSet proxy is running), bypassing the manager-side probe that would have failed.

If you really need a broad include (`.+`), pair it with an `exclude` rule that protects the proxy image and any other image the cluster must be able to pull without going through `localhost`:

```yaml
    imageFilter:
      include:
        - .+
      exclude:
        - docker\.io/library/registry.*
```

## Considerations

> ⚠️ **Use `skipActiveCheck: true` only for upstreams whose availability is guaranteed by construction** — typically node-local proxies (`localhost:<port>` DaemonSets) or registries reachable only from kubelet. **Avoid it on remote registries where images can disappear**: with the probe bypassed, the webhook will keep routing pods to a broken upstream and the failure only surfaces as `ImagePullBackOff` on the node, with no signal back to kuik.

### Interaction with `ClusterImageSetAvailability`

`ClusterImageSetAvailability` (CISA) iterates over images found in pod annotations and HEAD-checks each one independently of `skipActiveCheck`. The webhook records the rewritten reference in the `kuik.enix.io/original-images` annotation, so a `localhost:5000/...` ref will be probed by CISA from the manager pod and reported as `Unreachable` permanently — even when kubelet is happily pulling from it.

To avoid the false-negative noise in CISA status and dashboards, filter those references out at the CISA level via `spec.imageFilter.exclude`, e.g.:

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageSetAvailability
metadata:
  name: cisa
spec:
  imageFilter:
    exclude:
      - ^localhost(:\d+)?/
```

### Other considerations

- **Availability**: When `skipActiveCheck: true`, kube-image-keeper no longer verifies that the upstream actually has the image. You must ensure your mirror infrastructure reliably populates images before they're requested.
- **Failure detection**: Pods may fail to start if a trusted upstream is down or missing images. The webhook will still try alternative images in priority order, but it won't filter out unavailable trusted upstreams.
- **Use with priority**: Combine `skipActiveCheck` with the priority system to use trusted upstreams as primary and fall back to probed upstreams if needed:

  ```yaml
  spec:
    skipActiveCheck: true  # All upstreams in this CR are trusted
    upstreams:
    - registry: localhost
      path: /fast-cache
      imageFilter:
        include:
          - .+
      priority: 1   # Try this first (trusted, no probe)
    - registry: fallback-registry.example.com
      path: /backup
      imageFilter:
        include:
          - .+
      priority: 10  # Fall back to this (probed)
  ```

- **Credentials**: When using `skipActiveCheck` with pull secrets, ensure the secret is accessible from nodes (not just from manager). For node-local proxies, credentials may not be needed at all.
- **Observing skips**: Each bypassed probe is logged by the manager at debug verbosity with message `skipping availability check for trusted alternative`. To confirm a CR is taking the skip path, run the manager with `--zap-log-level=debug` and `kubectl logs … | grep "skipping availability check"`.

## When to use

| Situation                                                  | Recommended approach                                                                          |
| ---------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| Standard public or private registry reachable from cluster | Default behavior (no `skipActiveCheck`)                                                       |
| Self-hosted Harbor with proper certificates                | Default behavior (no `skipActiveCheck`)                                                       |
| Self-signed registry, manager can reach it                 | Default behavior + [`rootCertificateAuthorities`](configuration.md) Helm value                |
| Plain-HTTP registry, manager can reach it                  | Default behavior + [`insecureRegistries`](configuration.md) Helm value                        |
| `localhost:<port>` proxy via DaemonSet                     | `skipActiveCheck: true` on a `ClusterReplicatedImageSet` upstream                             |
| Upstream requires client certificate not available to manager | `skipActiveCheck: true` on a `ClusterReplicatedImageSet` upstream                          |

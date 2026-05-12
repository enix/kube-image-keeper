# Trusted Mirrors

By default, kube-image-keeper's webhook performs an availability probe (HTTP HEAD/GET request) on each alternative image before using it. This ensures only reachable images are selected, but it has a limitation: the probe runs from the **manager pod**, not from the node runtime.

This limitation matters when using mirrors that are only reachable from nodes, such as:

- `localhost:<port>` mirrors served by a DaemonSet on each node
- Mirrors with self-signed TLS certificates that kubelet trusts but the manager pod doesn't
- Mirrors that require node-local credentials or certificates

## `skipActiveCheck` flag

The `skipActiveCheck` flag allows you to bypass the manager-side availability probe for specific mirrors or upstreams. When set to `true`, the pod-mutation webhook stops checking that mirror at admission time and treats it as available.

**Scope**: this flag only affects the webhook's routing decision — i.e. which alternative image gets pinned into the pod spec. The mirroring/replication controllers keep doing their normal work (pulling upstream images into the cache, populating `mirroredAt`, running cleanup); none of that is short-circuited.

### Placement

The flag can be set at two levels:

1. **CR-level**: `spec.skipActiveCheck` on `(Cluster)ImageSetMirror` or `(Cluster)ReplicatedImageSet`
2. **Per-mirror/upstream**: `mirrors[].skipActiveCheck` or `upstreams[].skipActiveCheck`

When both are set, the per-mirror/upstream value takes precedence. If neither is set, the default behavior (probe all images) applies.

### Using the flag

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageSetMirror
metadata:
  name: localhost-proxy
spec:
  # Skip probe for all mirrors in this CR
  skipActiveCheck: true
  imageFilter:
    include:
      - registry.example.com/.+
  mirrors:
  - registry: localhost
    path: /kuik-cache
    # Override: probe this specific mirror even though CR-level skips
    skipActiveCheck: false
  - registry: localhost
    path: /kuik-backup
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
        # Pull the proxy image from upstream (docker.io), NOT from localhost:5000
        # — otherwise the DaemonSet cannot bootstrap on a fresh node.
        image: registry:2.8
        ports:
        - containerPort: 5000
          # hostPort binds the container port to the node's loopback so kubelet
          # can reach `localhost:5000`. Do not combine with `hostNetwork: true`.
          hostPort: 5000
```

### Configure ImageSetMirror to use it

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageSetMirror
metadata:
  name: localhost-mirror
spec:
  # Trust localhost proxy - manager can't reach it but kubelet can
  skipActiveCheck: true
  imageFilter:
    include:
      - .*
  mirrors:
  - registry: localhost
    path: /kuik-cache
```

When a pod is scheduled on a node, kubelet pulls from `localhost:5000` on that same node (where the DaemonSet proxy is running), bypassing the manager-side probe that would have failed.

### Using with ClusterReplicatedImageSet

The same `skipActiveCheck` flag works with `ClusterReplicatedImageSet` for upstreams:

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterReplicatedImageSet
metadata:
  name: localhost-upstream
spec:
  # Skip probe for all upstreams in this CR
  skipActiveCheck: true
  imageFilter:
    include:
      - registry.example.com/.+
  upstreams:
  - registry: localhost
    path: /kuik-cache
    # Override: probe this specific upstream even though CR-level skips
    skipActiveCheck: false
  - registry: localhost
    path: /kuik-backup
    # Inherits skipActiveCheck: true from CR level
```

## Considerations

> ⚠️ **Use `skipActiveCheck: true` only for mirrors whose availability is guaranteed by construction** — typically node-local proxies (`localhost:<port>` DaemonSets) or registries reachable only from kubelet. **Avoid it on remote registries where images can disappear**: with the probe bypassed, the webhook will keep routing pods to a broken mirror and the failure only surfaces as `ImagePullBackOff` on the node, with no signal back to kuik.

> ⚠️ **Stale `mirroredAt` is never cleared on a skipped mirror.** The webhook's failure-driven cleanup runs only when the probe returns `NotFound` — which never happens here. A CR can therefore report `mirroredAt: <timestamp>` indefinitely for an image that has actually vanished from the mirror, and kuik will keep routing new pods to it. Always keep at least one non-skipped alternative in the same CR so cleanup is still triggered for the underlying image, or drive re-mirroring out of band when a trusted mirror has been refreshed.

- **Availability**: When `skipActiveCheck: true`, kube-image-keeper no longer verifies that the mirror actually has the image. You must ensure your mirror infrastructure reliably populates images before they're requested.
- **Failure detection**: Pods may fail to start if a trusted mirror is down or missing images. The webhook will still try alternative images in priority order, but it won't filter out unavailable trusted mirrors.
- **Use with priority**: Combine `skipActiveCheck` with the priority system to use trusted mirrors as primary and fall back to probed mirrors if needed:

  ```yaml
  spec:
    skipActiveCheck: true  # All mirrors in this CR are trusted
    mirrors:
    - registry: localhost
      path: /fast-cache
      priority: 1  # Try this first (trusted, no probe)
    - registry: fallback-registry.example.com
      path: /backup
      priority: 10  # Fall back to this (probed)
  ```

- **Credentials**: When using `skipActiveCheck` with pull secrets, ensure the secret is accessible from nodes (not just from manager). For node-local proxies, credentials may not be needed at all.
- **Observing skips**: Each bypassed probe is logged by the manager at debug verbosity with message `skipping availability check for trusted alternative`. To confirm a CR is taking the skip path, run the manager with `--zap-log-level=debug` and `kubectl logs … | grep "skipping availability check"`.

## When to use

| Situation | Recommended approach |
| --- | --- |
| Standard public or private registry reachable from cluster | Default behavior (no `skipActiveCheck`) |
| Self-hosted Harbor with proper certificates | Default behavior (no `skipActiveCheck`) |
| Self-signed registry + node-level containerd config | Default behavior (no `skipActiveCheck`) |
| Self-signed registry, no node config access | `skipActiveCheck: true` on mirror |
| `localhost:<port>` proxy via DaemonSet | `skipActiveCheck: true` on mirror |
| Mirror requires client certificate not available to manager | `skipActiveCheck: true` on mirror |

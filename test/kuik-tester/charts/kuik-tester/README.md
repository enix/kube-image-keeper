# kuik-tester Helm chart

Deploys [kuik-tester](../../README.md), a synthetic OCI registry and proxy with fault
injection, and an optional in-chart zot used as the backend of the proxy hosts.

The chart is published as `oci://quay.io/enix/charts/kuik-tester` and deploys the image
`quay.io/enix/kuik-tester` at the chart's `appVersion` by default.

Installation, Ingress annotations, admin exposure and operational notes:
[kuik-tester README](../../README.md#install-on-a-cluster).

Objects (with `<fullname>` = `<release>-kuik-tester`, or the release name when it
already contains `kuik-tester`):

| Object | Purpose |
| --- | --- |
| Deployment, ConfigMap `<fullname>` | the tester (1 replica, `Recreate`); the pod restarts when the config changes |
| Service `<fullname>-registry` (8080), `<fullname>-admin` (8081) | `ClusterIP` Services |
| Service `<fullname>-plainhttp` | only when `plainHTTP.enabled` |
| Ingress `<fullname>-registry` | `*.<baseDomain>` |
| Ingress `<fullname>-admin` | `admin.<baseDomain>`, only when `ingress.admin.enabled` (off by default) |
| PVC `<fullname>-state` | state file, when `persistence.enabled` |
| Deployment, ConfigMap, Service, PVC `<fullname>-zot` | when `zot.enabled` |
| NetworkPolicy `<fullname>`, `<fullname>-zot` | only when `networkPolicy.enabled` (the zot one when `zot.enabled` too) |

The pod runs as UID/GID 65532 with `fsGroup: 65532`, so the state file is writable
on a fresh volume.

## Values

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| image.repository | string | `"quay.io/enix/kuik-tester"` | Image built from `test/kuik-tester/Dockerfile` (`task tester:image`). |
| image.tag | string | the chart appVersion | Image tag. Empty means the chart appVersion. |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy of the tester container. |
| image.pullSecrets | list | `[]` | Names of docker-registry Secrets, in the release namespace, used to pull the tester image (not the zot one). |
| baseDomain | string | `"kt.example.com"` | Every `<label>.<baseDomain>` host is a synthetic registry or a proxy. |
| proxies | list | `mirror`, `mirror-*`, `nodelete` and `nodelete-*`, all on the in-chart zot Service | Hosts forwarding to a real registry, as a list of `{name, backend}`. An empty `backend` means the in-chart zot Service (rendering fails when `zot.enabled` is false). A `name` may be a `path.Match` pattern without a dot (`mirror-*`): the exact name wins, then the first matching pattern. |
| trustedProxies | list | `[]` | CIDRs whose `X-Forwarded-*` headers are trusted (client IP, Bearer realm scheme). Set it to the narrowest CIDR holding the Ingress controller pods: their node subnet with hostNetwork, else the pod CIDR (e.g. `[10.244.0.0/16]`), which also trusts every other pod. Empty trusts nobody: behind an Ingress the Bearer realms are `http://` and per-IP quotas see the controller. |
| logSize | int | `10000` | Size of the request log ring buffer (at least 1). |
| expireAfter | string | `"7d"` | How long seen or resolved images (with their generations and digests) and logged requests are kept without being seen again, as a Go duration (`168h`, `90m`) or a number of days (`7d`). Older ones are dropped. `0` keeps everything. |
| tls.secretName | string | `""` | Secret with `tls.crt` and `tls.key`. When set, the binary terminates TLS on :8080. Rendering fails when `ingress.registry.enabled` is also true. |
| persistence.enabled | bool | `true` | Keep the state file on a PVC. |
| persistence.size | string | `"1Gi"` | Size of the state PVC. |
| persistence.storageClass | string | `""` | Storage class of the state PVC. Empty means the cluster default. |
| plainHTTP.enabled | bool | `false` | Serve an extra plain-HTTP registry on :5000. |
| plainHTTP.hostLabel | string | `"http"` | Host label served by the plain-HTTP listener (a synthetic host or a proxy name). |
| plainHTTP.service.type | string | `"LoadBalancer"` | Service type: `LoadBalancer`, `NodePort` or `ClusterIP`. |
| plainHTTP.service.port | int | `5000` | Service port. |
| plainHTTP.service.nodePort | int | `0` | Fixed node port (`NodePort` or `LoadBalancer`); `0` lets Kubernetes allocate one. Fix it when containerd hosts.toml or the kuik config name `<nodeIP>:<nodePort>`. |
| ingress.registry.enabled | bool | `true` | Create the Ingress for `*.<baseDomain>`. |
| ingress.registry.className | string | `""` | `ingressClassName` of the registry Ingress, omitted when empty. |
| ingress.registry.annotations | object | `{}` | Annotations of the registry Ingress. |
| ingress.registry.tlsSecretName | string | `""` | TLS Secret for `*.<baseDomain>`. |
| ingress.admin.enabled | bool | `false` | Create the Ingress for the admin API and UI, which have no authentication of their own. Enable it only on a private Ingress class or with allowlist and auth annotations. |
| ingress.admin.className | string | `""` | `ingressClassName` of the admin Ingress, omitted when empty. |
| ingress.admin.annotations | object | `{}` | Annotations of the admin Ingress. |
| ingress.admin.host | string | `admin.<baseDomain>` | Host of the admin Ingress. |
| ingress.admin.tlsSecretName | string | `""` | TLS Secret for the admin host. |
| networkPolicy.enabled | bool | `false` | Create a NetworkPolicy on the tester pod and, with the in-chart zot (`zot.enabled`), one on zot. The tester policy lets everyone reach the registry ports (8080, and 5000 with `plainHTTP.enabled`) and restricts the admin port (8081) to `adminFrom`. The zot policy lets only the tester pod reach zot on 5000. No egress rule is set. The CNI plugin must enforce NetworkPolicy, or this has no effect. |
| networkPolicy.adminFrom | list | `[]` | Peers (`podSelector`, `namespaceSelector`, `ipBlock`) allowed on the admin port. Empty means only `kubectl port-forward` (it does not go through the pod network) and the kubelet probes (they come from the node, which most CNI plugins let through; add the node CIDR as an `ipBlock` if yours does not; zot's own probes on port 5000 come from the node too and are covered by the same caveat). Must name the Ingress controller when `ingress.admin.enabled` is true. |
| zot.enabled | bool | `true` | Deploy zot as the default proxy backend. |
| zot.image | string | `"ghcr.io/project-zot/zot-minimal:v2.1.21"` | zot image. |
| zot.gcDelay | string | `"5m"` | zot `storage.gcDelay`. |
| zot.gcInterval | string | `"10m"` | zot `storage.gcInterval`. |
| zot.retentionDelay | string | `"5m"` | zot `storage.retention.delay`. |
| zot.persistence.enabled | bool | `true` | PVC for zot data, `emptyDir` otherwise. |
| zot.persistence.size | string | `"5Gi"` | Size of the zot PVC. |
| zot.persistence.storageClass | string | `""` | Storage class of the zot PVC. Empty means the cluster default. |
| zot.resources | object | `{}` | Resources of the zot container. |
| resources | object | `{}` | Resources of the tester container. |

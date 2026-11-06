# kuik-tester

kuik-tester is a synthetic OCI registry and a pass-through registry proxy with fault injection.
It plays the registries that [kube-image-keeper](https://github.com/enix/kube-image-keeper)
(kuik) talks to, and makes them fail on demand: missing manifests, 5xx, latency, connection
resets, rate limits and quotas, Basic and Bearer authentication, tags that move. Its request log
records what every client asked and what it got, which is the evidence of how kuik reacted.

It is meant for:

- **kuik users**: demos, checking that the kuik resources you wrote do what you expect, and seeing
  how kuik behaves when a registry fails, rate-limits, requires credentials or drifts, without
  breaking a real registry;
- **kuik maintainers**: manual testing, and automated tests that drive the admin API through
  `pkg/client`.

It lives in kube-image-keeper's repository as a nested Go module, with its own tasks
([notes/0017](../../notes/0017-kuik-tester.md)). Paths below are relative to
`test/kuik-tester/`, and `task tester:<task>` runs from the repository root.

## Features

- It serves tiny multi-arch (amd64 and arm64) `sleep`-forever images for any `repo:tag`
  under as many hostnames as you want, one per subdomain of a wildcard domain, without
  pushing anything.
- Proxy hosts forward to a real registry (zot) and play the mirror destination of kuik,
  with destination actions (delete a tag, delete a manifest, tag, push).
- A rule engine breaks requests per host, repo, tag, request kind and caller: status codes,
  latency, connection resets, `RateLimit-Remaining: 0`, quotas, Basic and Bearer auth.
- A web UI and a JSON API (`/api/v1`) change behaviours in a couple of clicks; everything
  the UI does is available through the API, and [`pkg/client`](./pkg/client) is a Go client of it.
- A request log with the caller class (`kuik`, `containerd`, `crane`, `other`) is the
  evidence of each scenario.
- Drift (a tag moved to new content), divergent alternatives (same tag, other digest) and
  single-platform images are one API call each.

kuik-tester is not tied to a kuik version: it only plays registries. How it works inside:
[docs/architecture.md](./docs/architecture.md).

## Quick start with the compose stack

The compose stack is for development and local trials (the Helm chart is the deployment path).
It runs kuik-tester in front of a zot registry, with every port bound to `127.0.0.1` because
the admin has no authentication of its own.

```sh
cd test/kuik-tester
docker compose up -d --build
```

The admin on `localhost:8081` has no authentication: see [Admin exposure](#admin-exposure)
before you expose its port beyond this machine.

| Port | Service | Override |
| --- | --- | --- |
| 8080 | registry listener (`<label>.kt.localhost`) | `KT_REGISTRY_PORT` |
| 8081 | admin UI and API | `KT_ADMIN_PORT` |
| 5000 | plain-HTTP registry, host `upstream` | `KT_PLAIN_PORT` |
| 5001 | zot (backend of the proxies `mirror` and `nodelete`) | `KT_ZOT_PORT` |

Open <http://localhost:8081> for the UI. Pull with `crane` (`*.localhost` resolves to
`127.0.0.1` on most systems; the stack serves plain HTTP, hence `--insecure`):

```sh
crane digest --insecure upstream.kt.localhost:8080/acme/foo:t1
crane manifest --insecure upstream.kt.localhost:8080/acme/foo:t1
crane digest --insecure localhost:5000/acme/foo:t1      # same host through the plain-HTTP port
```

Break it:

```sh
curl -X POST localhost:8081/api/v1/images/action \
  -d '{"host":"upstream","repo":"acme/foo","tag":"t1","action":"503"}'
crane digest --insecure localhost:5000/acme/foo:t1      # now fails
curl localhost:8081/api/v1/requests?host=upstream | jq
```

`docker compose down -v` stops the stack and deletes its volumes (state and zot data), which
resets everything.

## Install on a cluster

The Helm chart is published as `oci://quay.io/enix/charts/kuik-tester` and deploys the image
`quay.io/enix/kuik-tester`, with an in-chart zot as the backend of the proxy hosts. The chart, the
image and the Go module share one version, tagged `test/kuik-tester/v<version>` in this
repository:

```sh
helm install kt oci://quay.io/enix/charts/kuik-tester --version <chart version> \
  --namespace kuik-tester --create-namespace \
  --set baseDomain=kt.example.com \
  --set 'trustedProxies={<Ingress controller CIDR>}' \
  --set ingress.registry.className=nginx \
  --set ingress.registry.tlsSecretName=kt-wildcard-tls
```

`trustedProxies` names the Ingress controller pods; see [Operational notes](#operational-notes)
for the CIDR to use.

DNS: point `*.<baseDomain>` (and `admin.<baseDomain>` when the admin Ingress is enabled) at the
Ingress controller.

TLS with cert-manager: create a wildcard `*.<baseDomain>` Certificate using a DNS-01 solver.
The chart does not create the Issuer. Reference the resulting Secret from
`ingress.registry.tlsSecretName` (and `ingress.admin.tlsSecretName` when the admin Ingress is
enabled; the wildcard covers `admin.<baseDomain>`). The nodes must trust the certificate:
containerd pulls over TLS.

From a checkout, `helm install kt test/kuik-tester/charts/kuik-tester ...` installs the working
tree; set `image.repository` and `image.tag` to an image built with `task tester:image` and
pushed to a registry the cluster can pull from.

With the defaults only the registry is exposed. The admin is reachable with
`kubectl -n kuik-tester port-forward svc/kt-kuik-tester-admin 8081` (`svc/<fullname>-admin`),
or through its own Ingress, see below.

[docs/demo.md](./docs/demo.md) installs kuik-tester next to kuik, points kuik v3
CRs at it, breaks the upstream registry from the UI and API, and shows where to read kuik's
reaction.

Every value of the chart is listed in [the chart README](./charts/kuik-tester/README.md).

### Registry Ingress annotations

Image layers are streamed: no body size limit and no request buffering.

ingress-nginx:

```yaml
ingress:
  registry:
    annotations:
      nginx.ingress.kubernetes.io/proxy-body-size: "0"
      nginx.ingress.kubernetes.io/proxy-request-buffering: "off"
```

Traefik has no body limit and does not buffer unless a `Buffering` middleware is
attached to the router: attach none.

### Admin exposure

The admin UI and API (port 8081) have no authentication. Whoever reaches the port can rewrite
every rule, read the user passwords and delete content in zot.

Inside the cluster, any pod reaches the admin Service, and the zot Service, which has no
authentication either, unless `networkPolicy.enabled` is set (see [NetworkPolicy](#networkpolicy)).

The cross-origin guard refuses a non-`GET` request whose `Origin` names another host. It
compares `Origin` with the request's own `Host`, and the admin checks no `Host`. A web page
whose domain an attacker re-points at the admin address (DNS rebinding) therefore passes the
guard: a browser that can reach the admin, through a `localhost` port-forward included, can be
driven to change the state.

Keep port-forwards short-lived. For shared access, use an Ingress with authentication (see
[Admin Ingress](#admin-ingress)).

### Admin Ingress

The admin API and UI have no authentication of their own: whoever reaches them can
rewrite every rule, read the user passwords and delete content in zot. That is why
`ingress.admin.enabled` is `false` by default. Enable it only on a private Ingress
class, or with allowlist and auth annotations (the examples below), for instance:

```sh
helm upgrade --install kt oci://quay.io/enix/charts/kuik-tester --version <chart version> \
  --namespace kuik-tester --reuse-values \
  --set ingress.admin.enabled=true \
  --set ingress.admin.className=nginx-private \
  --set ingress.admin.tlsSecretName=kt-wildcard-tls
```

With `networkPolicy.enabled`, list the Ingress controller in `networkPolicy.adminFrom`.

The SSE stream `/api/v1/requests/stream` needs no response buffering and a long read timeout,
and the `Host` header must reach the admin unchanged (cross-origin guard).

ingress-nginx:

```yaml
ingress:
  admin:
    annotations:
      nginx.ingress.kubernetes.io/whitelist-source-range: "192.0.2.0/24"
      nginx.ingress.kubernetes.io/auth-type: basic
      nginx.ingress.kubernetes.io/auth-secret: kt-admin-auth
      nginx.ingress.kubernetes.io/proxy-buffering: "off"
      nginx.ingress.kubernetes.io/proxy-read-timeout: "3600"
```

Traefik, with `ipAllowList` and `basicAuth` middlewares created beforehand:

```yaml
ingress:
  admin:
    annotations:
      traefik.ingress.kubernetes.io/router.middlewares: kt-admin-allow@kubernetescrd,kt-admin-auth@kubernetescrd
```

### NetworkPolicy

`networkPolicy.enabled` (off by default) creates a NetworkPolicy on the tester pod and, with the
in-chart zot (`zot.enabled`), one on zot, with ingress rules only:

- on the tester pod, the registry ports (8080, and 5000 with `plainHTTP.enabled`) stay open to
  everyone, and the admin port (8081) only accepts the peers listed in `networkPolicy.adminFrom`;
- on the in-chart zot (when `zot.enabled`), only the tester pod reaches port 5000.

`networkPolicy.adminFrom` takes NetworkPolicy peers (`podSelector`, `namespaceSelector`,
`ipBlock`). For an ingress-nginx controller in the `ingress-nginx` namespace:

```yaml
networkPolicy:
  enabled: true
  adminFrom:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: ingress-nginx
```

With `networkPolicy.enabled`, rendering fails when `ingress.admin.enabled` is true and
`networkPolicy.adminFrom` is empty.

With an empty `adminFrom`, the admin port still answers `kubectl port-forward`, which does not go
through the pod network, and the kubelet probes, which come from the node. Most CNI plugins let
node traffic through; if yours does not, add the node CIDR to `adminFrom` as an `ipBlock`. The
same goes for zot's own probes on port 5000 when its policy is on: they also come from the node,
and the policy only admits the tester pod.

The CNI plugin must enforce NetworkPolicy, or these objects have no effect.

### Operational notes

- `trustedProxies` is empty by default: the tester trusts no `X-Forwarded-*` header. Behind an
  Ingress, set it to the narrowest CIDR holding the Ingress controller pods: their node subnet
  when they use `hostNetwork`, else the pod CIDR (for example `10.244.0.0/16`). A pod CIDR also
  trusts every other pod: any pod can then forge the client IP that per-IP quotas count, and the
  scheme of its Bearer realms. Left empty behind an Ingress, the Bearer realms are `http://...`,
  which go-containerregistry refuses, and per-IP quotas see the controller's IP for every client.
- The pod fails to start when `statePath` is set and its directory is missing or not writable
  (a read-only state volume, say), and when the configuration is invalid: the error lists every
  problem with its key path. An explicit empty `registryAddr`, `adminAddr` or `plainHTTPHost`,
  or `logSize: 0`, is refused (they used to fall back to the default).
- Seen images (with their generations and digests) and logged requests not seen for `expireAfter`
  (chart value, default `7d`, `0` disables) are dropped, so the state does not grow for ever.
- With `tls.secretName`, a renewed certificate is picked up without a restart (certificate and
  key reloaded when their files change). A pair that fails to load is logged and the previous
  one stays in use.
- A backend that sends no response headers within 30 s answers 502.
- Per-IP quotas need the client source IP preserved up to the Ingress controller
  (`externalTrafficPolicy: Local` on its Service, or an equivalent).
- Running in the same cluster as kuik is fine, as long as no kuik CR targets the
  registry the tester image is pulled from.
- With the admin Ingress disabled, `admin.<baseDomain>` reaching the registry
  Ingress is served as the synthetic host `admin`, never as the admin: the registry
  Ingress has a single backend, the registry Service, and the admin is only
  served on :8081. When the admin Ingress is enabled on the same class as the
  registry Ingress, the exact host `admin.<baseDomain>` is routed to the admin
  before the wildcard, so it must be protected as described above.
- Untagged manifests are removed by zot once `zot.retentionDelay` has elapsed and
  the next GC run (every `zot.gcInterval`) has passed. Pushing a second image to a
  tag therefore makes the first manifest disappear after at most about
  `retentionDelay + gcInterval`.
- A change to the values that alter the tester configuration (new proxy, new
  `trustedProxies`...) restarts the pod through the `checksum/config` annotation.

## Without Docker

```sh
task tester:build                  # test/kuik-tester/bin/kuik-tester
cd test/kuik-tester
cp config.example.yaml config.yaml # edit baseDomain, proxies, statePath...
bin/kuik-tester -config config.yaml
```

`config.example.yaml` documents the keys (`baseDomain` is required, unknown keys are
rejected).

## Host naming

Requests are routed by the `Host` header: `<label>.<baseDomain>` is the host `<label>`.

- **Proxy host**: a label declared in `proxies` (`name`, `backend`; the name may be a pattern
  without a dot, `mirror-*`) is forwarded to its backend, with rules, auth and the request log
  applied as for any host. Several proxies can share one backend (`nodelete` is `mirror` plus a
  rule refusing `DELETE`).
- **Synthetic host**: every other label is a synthetic registry created on first request
  (`upstream`, `alt1`, `secure`...). Synthetic hosts are read-only (writes answer 405).
- The bare `baseDomain`, a nested label (`a.b.<baseDomain>`) and a foreign domain answer
  404.
- **`plainHTTPHost`**: the optional plain-HTTP listener (`plainHTTPAddr`) has no usable
  `Host`, so every request on it maps to one fixed label, `plainHTTPHost` (default `http`).
  It can name a synthetic host or a proxy.

## UI pages and API routes

All routes are under `/api/v1` on the admin listener. A non-`GET` request with a foreign
`Origin` is refused (403); `curl` sends none. The wire types are in [`pkg/api`](./pkg/api), and
[`pkg/client`](./pkg/client) is a Go client of these routes (with `pkg/client/clienttest`, an
in-process server for the tests of code built on it). The client sets no whole-request timeout
(dial and TLS handshake 10 s each): bound every call with its `ctx`. `CreateRule` always creates
an enabled rule (disable it with `UpdateRule`), `PutQuota` returns the stored quota, and an error
body decodes as `api.ErrorResponse`.

| UI page | Content | API routes |
| --- | --- | --- |
| Images (`/`) | seen images (filter on host and repo, at most 500 rows shown), effective behaviour, one-click faults, drift, resolve | `GET /images`, `POST /images/action`, `POST /drift`, `POST /resolve`, `DELETE /images` (purge) |
| Rules (`/rules`) | ordered fault rules, first match wins | `GET/POST /rules`, `DELETE /rules` (purge), `PUT/DELETE /rules/{id}`, `POST /rules/{id}/move`, `POST /rules/{id}/enabled` |
| Settings (`/settings`) | users, grants, auth scheme per host, aliases, divergences, quotas, platform rules | `/users/{name}`, `/grants`, `/hostauth/{host}`, `/aliases`, `/divergents`, `/quotas/{name}`, `GET /quotas/usage`, `POST /quotas/{name}/reset`, `/platforms`; `DELETE` on a collection purges it |
| Destinations (`/destinations`) | inventory and actions on proxy hosts, purge of every tag under a repository prefix, or of every repository | `GET /destinations`, `GET /destinations/{host}`, `POST /destinations/{host}/delete-tag`, `.../delete-manifest`, `.../tag`, `.../push`, `.../purge`, `.../purge-all` |
| Requests (`/requests`) | live log and counters | `GET /requests`, `GET /requests/export`, `GET /requests/stream`, `GET /stats`, `POST /stats/reset`, `DELETE /log` (purge) |
| State (`/state`) | export and import of the whole state, sets (list, export, merge a YAML into a set, delete), reset of everything (type `purge`) | `GET /state`, `GET /export`, `POST /import`, `GET /sets`, `GET /export?set=<name>`, `POST /import?set=<name>`, `DELETE /sets/{name}`, `POST /purge` (purge everything) |

The one-click actions of `POST /images/action` are `ok` (delete the rules of exactly that image),
`exempt`, `404`, `401`, `403`, `429`, `503`, `ratelimit0` (`RateLimit-Remaining: 0;w=21600`),
`latency` (3 s), `reset` and `drift`. `GET /requests` filters on `host`, `repo` (exact),
`kind`, `caller` (the caller class), `status` (`0`: no response was sent) and `since`.
Every `DELETE` on `/rules`, `/aliases`, `/divergents`, `/grants`, `/platforms`, `/users`,
`/hostauth`, `/quotas`, `/images` and `/log` purges that kind at once and answers
`{"deleted": n}`; each has a `Purge all` button, with a confirmation that names the count, on
its UI page. A purge removes the objects of every set, and is refused (400, nothing changes) when
what remains would be dangling (users a grant names, a quota a rule names). Purging the images
forgets the seen images and every generation; an image that a rule names exactly stays listed.
`POST /destinations/{host}/purge-all` deletes every tag of every repository of a proxy backend. A
destination purge (`purge`, `purge-all`) also deletes the manifests those tags pointed to (and the
platform manifests of an index) and their blobs in each purged repository, so that a later copy into
the same repository reads and pushes everything again; the repository itself stays listed, empty, as
long as the backend keeps it.
`POST /purge` with `{"backends": bool}` brings the tester back to a blank state: every list and set, every
seen image, every quota counter, the request log and the stats. With `backends` true it also purges each
distinct proxy backend once (proxies that share a backend count once). It answers 200 with the count per
kind and, per backend, `repositories`, `deletedTags` and an `error` when that backend failed; the state
is purged anyway. The State page's `Reset` section does the same behind a typed `purge`.
`GET /healthz` on the admin listener is the health probe.

### Sets

Every rule, user, grant, alias, divergence, quota, host auth and platform rule may carry a
`set` name that groups what one test or fixture created: `POST /import?set=<name>` merges a YAML
document under a set, `GET /export?set=<name>` exports it, `DELETE /sets/{name}` removes it
in one call and `GET /sets` lists the sets. A `PUT` replaces the whole object, its set included:
a body without `set` takes the object out of its set (the UI's edit form carries the set). The
exact semantics are in [docs/architecture.md](./docs/architecture.md#admin-api).

### Synthetic images

A synthetic image is a tiny multi-arch OCI index (`linux/amd64`, `linux/arm64`) served for any
`repo:tag` without a push; a platform rule (`POST /platforms`) narrows a repository to one
platform. Its `/sleep` entrypoint blocks until `SIGTERM`, or exits 0 after the duration given as
argument, and the image declares no user. Details:
[docs/architecture.md](./docs/architecture.md#synthetic-images).

## Development

The tester is a nested Go module: the root `task lint`, `task vet` and `task test` do not cover
it. Its tasks run from the repository root; `go` commands run from `test/kuik-tester`.

| Command | What it does |
| --- | --- |
| `task tester:test` | unit and handler tests of the tool, with the race detector |
| `task tester:vet` | `go vet`, the `e2e`-tagged specs included |
| `task tester:lint` | golangci-lint with the repository's configuration (`tester:lint-fix` applies the fixes) |
| `task tester:tidy` | fails when `go.mod` or `go.sum` is not tidy |
| `task tester:build` | `bin/kuik-tester`, with the `sleep` binaries embedded |
| `task tester:test-integration ZOT_URL=http://127.0.0.1:5001` | specs tagged `e2e` against a real zot (the task refuses to run without `ZOT_URL`; `go test -tags e2e` skips these specs without it; the compose stack publishes a zot on that port) |
| `task tester:chart-test` | `helm lint` and the chart rendering assertions |
| `task tester:chart-docs` | regenerates the chart README from the chart values (`tester:chart-test` checks it is current) |
| `task tester:image` | multi-arch image with `docker buildx` (`TESTER_PLATFORMS=linux/amd64` for a local `--load`, `TESTER_IMAGE` names it) |
| `task tester:bins` | builds the `sleep` binaries of the synthetic images |
| `task tester:test-bins` | `tester:bins`, then the tests that need those binaries |

### Running the tests

The Go tests are [Ginkgo](https://onsi.github.io/ginkgo/) and [Gomega](https://onsi.github.io/gomega/)
specs, one suite per package, started by `go test`:

```sh
go test ./...                                  # every suite
go test ./internal/admin/                      # one package
go test ./internal/admin/ -ginkgo.focus 'moves, toggles and deletes a rule'   # one spec
go test ./internal/admin/ -ginkgo.randomize-all -ginkgo.seed=1                # shuffled specs
```

Select a spec with `-ginkgo.focus '<text>'` (a regular expression on the full text of the spec),
never with `-run`, which only selects the package's `Test<Pkg>` bootstrap function. Specs must
not depend on each other: run a package with `-ginkgo.randomize-all` before pushing a change.

### The sleep binaries

Every synthetic image holds a small static `sleep` binary (`cmd/sleep`), one per architecture.
They are not committed: `task tester:bins` builds them under `internal/synth/bin/` (ignored by
git), and a build with the `sleepbins` tag embeds them with `go:embed`. `task tester:build` and the
image do both.
Without the tag, `go build`, `go test` and the linters use placeholder bytes instead, so they
need no generation step: the synthetic images are served and pulled the same way, but a
container cannot start on them, and the tool logs a warning at startup.

`task tester:bins` builds reproducibly (`-trimpath`, empty build ID, `-buildvcs=false`), so the result
only depends on `cmd/sleep`, its module path and the Go release. The binary is a layer of every
synthetic image: a new Go release in the `Dockerfile` or a new module path changes every digest
the tool serves, so a state saved by an older image may record digests a newer one no longer
serves.

## License

MIT, like kube-image-keeper. `internal/admin/ui/static/htmx.min.js` is
[htmx](https://github.com/bigskysoftware/htmx) 2.0.11, under the Zero-Clause BSD license.

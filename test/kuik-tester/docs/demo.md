# Walkthrough: kuik against a failing registry

This walkthrough installs kuik-tester on a cluster that runs kuik, points kuik custom
resources at kuik-tester hosts, then breaks the upstream registry from the kuik-tester UI or API
and shows where kuik's reaction can be read: the pod, its events, the CR status, and the
kuik-tester request log.

It covers three kuik resources:

1. an `ImageAlternative` that sends a pod to an equivalent registry when the origin fails
   (503, latency, rate limit, quota);
2. an `ImageMonitor` that reports why an image became unavailable (404, authentication, 429,
   connection reset);
3. an `ImageMirror` that copies images to a destination registry, runs a pod from the copy when
   the origin fails, and restores a copy deleted behind its back.

The kuik v3 documentation is the reference for the field meanings.

## Before you start

- **kuik v3** installed in `kuik-system`, with its webhook active: the custom resources below
  (`ImageAlternative`, `ImageMonitor`, `ImageMirror`) are those of kuik v3.
- **An Ingress controller**, a DNS record `*.kt.example.com` pointing at it, and a wildcard
  certificate for `*.kt.example.com` that both containerd (on every node) and kuik trust. A
  certificate from a public CA (cert-manager with a DNS-01 solver) needs nothing more. With a
  private CA, containerd needs it in its per-host configuration on every node, and kuik through
  `config.tls.<host>.caSecretRef` (one entry per host kuik contacts in this walkthrough:
  `upstream.$D`, `alt1.$D`, `notfound.$D`, `denied.$D`, `ratelimited.$D`, `down.$D` and `mirror.$D`,
  each `caSecretRef: {name: <CA Secret>}`).
- **No other kuik CR selecting the demo namespace.** A cluster-wide `ImageMirror` or
  `ImageMonitor` that matches every namespace would also copy or check the demo images. Exclude
  the demo namespaces in the `namespaceSelector` of such CRs.
- **Clocks.** The `ImageAlternative` part runs on kuik's defaults: it acts at admission, and kuik
  caches a registry check for about 10 seconds (`activeCheckCache`). The `ImageMonitor` and
  `ImageMirror` parts depend on background clocks that are slow on a default install (a copy
  waits for the copy window of its source registry, minutes by default; the destination pass
  runs about hourly). For a demo, shorten them in kuik's Helm values:

  ```yaml
  config:
    mirror:
      destinationScan:
        interval: 30s
    registries:
      default:
        check:
          interval: 5s
        copy:
          interval: 5s
  ```

- `kubectl`, `helm`, `curl` and `jq` on your workstation; `crane` is optional.

The shell variables used below:

```sh
export D=kt.example.com              # the wildcard domain of kuik-tester
export ADMIN=http://localhost:8081   # the admin, through the port-forward below
export NS=kt-demo                    # the namespace of the demo workloads
```

## 1. Install kuik-tester

```sh
helm install kt oci://quay.io/enix/charts/kuik-tester --version <chart version> \
  --namespace kuik-tester --create-namespace \
  --set baseDomain=$D \
  --set 'trustedProxies={<Ingress controller CIDR>}' \
  --set ingress.registry.className=nginx \
  --set ingress.registry.tlsSecretName=kt-wildcard-tls
```

- `ingress.registry.className` and `ingress.registry.tlsSecretName` name your Ingress class
  and the Secret of the wildcard certificate. The
  [README](../README.md#registry-ingress-annotations) gives the annotations
  that turn off body size limits and request buffering (image layers are streamed).
- `trustedProxies` must cover the pod IPs of the Ingress controller, see the
  [operational notes](../README.md#operational-notes).
- The chart also deploys a zot registry, the backend of the proxy hosts `mirror`, `mirror-*`,
  `nodelete` and `nodelete-*`. Every other label under `$D` is a synthetic registry.

The admin has **no authentication**, so the chart keeps it off the Ingress; reach it with a
port-forward (see [Admin Ingress](../README.md#admin-ingress)):

```sh
kubectl -n kuik-tester port-forward svc/kt-kuik-tester-admin 8081
```

Open <http://localhost:8081>: the Images, Rules, Settings, Destination, Requests and State pages
are the UI of everything below. Check the install:

```sh
curl -fsS "$ADMIN/healthz"
curl -fsS -X POST "$ADMIN/api/v1/resolve" -d '{"ref": "upstream/acme/demo/app:t1"}' \
  | jq '{indexDigest, platforms}'
crane digest upstream.$D/acme/demo/app:t1    # optional: the same index digest, over the Ingress
```

`resolve` takes the host label (`upstream`), not the full name. Nothing was pushed: a synthetic
host serves any `repo:tag`, a tiny multi-arch image whose entrypoint sleeps until `SIGTERM`.
`upstream`, `alt1` and `alt2` serve the same content for the same path by default, which makes
them equivalent registries for kuik.

Every admin object created below carries `"set": "demo"`, so `DELETE /api/v1/sets/demo` removes
them all at the end. The one-click buttons of the Images page create rules without a set: the
"OK" button of the same row removes them.

Create the namespaces of the workloads (the `ImageMonitor` of step 3 gets its own, so that it
does not also report the images the other steps break):

```sh
kubectl create namespace "$NS"
kubectl create namespace "$NS-mon"
```

Each step uses its own image tag, so that no node starts a pod from its image cache and hides
what kuik did. The pods use this Deployment (it meets the Pod Security `restricted` profile; the
synthetic images declare no user):

```sh
deploy() { # deploy <name> <image> [namespace]
kubectl -n "${3:-$NS}" apply -f - <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: $1
spec:
  replicas: 1
  selector:
    matchLabels: {app: $1}
  template:
    metadata:
      labels: {app: $1}
    spec:
      securityContext:
        runAsNonRoot: true
        runAsUser: 65534
        runAsGroup: 65534
        seccompProfile: {type: RuntimeDefault}
      containers:
        - name: sleep
          image: $2
          securityContext:
            readOnlyRootFilesystem: true
            allowPrivilegeEscalation: false
            capabilities: {drop: [ALL]}
YAML
}
```

## 2. ImageAlternative: fall back when the origin fails

An `ImageAlternative` (cluster-scoped) declares repositories that hold the same images. With
`rewritePolicy: OnFailure` kuik keeps the origin while it answers and rewrites the pod to the
first alternative that answers when it does not. The origin is listed first: an entry list that
does not name the origin does not apply to its pods.

```sh
kubectl apply -f - <<YAML
apiVersion: kuik.enix.io/v1alpha1
kind: ImageAlternative
metadata:
  name: demo-alt
spec:
  namespaceSelector:
    matchLabels:
      kubernetes.io/metadata.name: $NS
  rewritePolicy: OnFailure
  alternatives:
    - repository: upstream.$D/acme/demo/app
    - repository: alt1.$D/acme/demo/app
YAML
kubectl get imagealternative demo-alt -o jsonpath='{.status.conditions}' | jq
```

Wait for `Ready=True`, then start a pod while the origin is healthy:

```sh
deploy healthy upstream.$D/acme/demo/app:t-healthy
kubectl -n "$NS" get pods -l app=healthy -o jsonpath='{.items[0].spec.containers[0].image}'
```

The image is unchanged and the pod carries no kuik annotation.

### Break the origin: 503

UI: on the Images page, the 503 button of the row `upstream / acme/demo/app:t-503` (use the
resolve form first if the row is not listed). API:

```sh
RULE=$(curl -fsS -X POST "$ADMIN/api/v1/rules" -d '{
  "set": "demo",
  "target": {"host": "upstream", "repo": "acme/demo/app", "tag": "t-503"},
  "effects": {"status": 503},
  "note": "demo: origin down"}' | jq -r .rule.id)
sleep 10    # let kuik's check cache expire
deploy down503 upstream.$D/acme/demo/app:t-503
```

What kuik does:

- the pod image is `alt1.kt.example.com/acme/demo/app:t-503`, and the annotation
  `kuik.enix.io/rewrites` records `by`, `origin`, `rewrittenTo` and `policy: OnFailure`:

  ```sh
  kubectl -n "$NS" get pods -l app=down503 -o json \
    | jq '.items[0] | {image: .spec.containers[0].image, rewrites: .metadata.annotations["kuik.enix.io/rewrites"]}'
  ```

- an `ImageFallback` event (Normal) on the pod:
  `kubectl -n "$NS" get events --field-selector reason=ImageFallback`;
- on the CR, condition `FallbackActive=True` with reason `OriginUnavailable` and an entry in
  `status.activeFallbacks`: `kubectl get imagealternative demo-alt -o yaml`.

What kuik-tester saw (the Requests page with the filters host, kind, caller and status, or the
API):

```sh
curl -fsS "$ADMIN/api/v1/requests?host=upstream&repo=acme/demo/app" \
  | jq -r '.[] | select(.reference=="t-503") | [.time, .method, .kind, .status, .caller, .ruleId] | @tsv'
curl -fsS "$ADMIN/api/v1/requests?host=alt1&repo=acme/demo/app" \
  | jq -r '.[] | select(.reference=="t-503") | [.time, .method, .kind, .status, .caller] | @tsv'
```

On `upstream`: kuik's `HEAD` of the manifest answered 503 by the rule (its id in `ruleId`), and no
`containerd` request. On `alt1`: kuik's `HEAD`, then containerd's `GET`s of the pull, all 200. The
`caller` column is derived from the User-Agent: `kuik` for kuik (go-containerregistry),
`containerd` for the kubelet's pulls.

### Slow origin: latency

A candidate that answers slower than the webhook's probe timeout (`availabilityCheck.timeout`, 2 s
by default) counts as unavailable. UI: the `Latency 3 s` button of the row. API:

```sh
curl -fsS -X POST "$ADMIN/api/v1/rules" -d '{
  "set": "demo",
  "target": {"host": "upstream", "repo": "acme/demo/app", "tag": "t-slow"},
  "effects": {"latencyMs": 3000}}' | jq .rule.id
sleep 10
deploy slow upstream.$D/acme/demo/app:t-slow
```

The pod is rewritten to `alt1` as above. In the request log, the kuik `HEAD` on `upstream` has
status `0`: kuik went away during the delay, so no response was ever sent
(`requests?host=upstream&status=0` lists such requests).

### Rate limit: 429, `RateLimit-Remaining: 0`, a real quota

kuik treats a registry that says its quota is exhausted as unavailable, whether it answers 429 or
200 with `RateLimit-Remaining: 0`. The Images page has a button for each (`429` and
`RateLimit 0`). With the API, the second one is a rule with headers only, so the
manifest is still served:

```sh
curl -fsS -X POST "$ADMIN/api/v1/rules" -d '{
  "set": "demo",
  "target": {"host": "upstream", "repo": "acme/demo/app", "tag": "t-rl0"},
  "effects": {"headers": {"RateLimit-Remaining": "0;w=21600"}}}' | jq .rule.id
sleep 10
deploy ratelimit0 upstream.$D/acme/demo/app:t-rl0
```

The pod goes to `alt1`; the log shows a kuik `HEAD` on `upstream` answered 200 by the rule, and no
containerd request there.

A quota is a real counter: N manifest requests per window, keyed `global`, per client `ip` or per
`user`, with `RateLimit-Limit` and `RateLimit-Remaining` on every counted answer and 429 once it
is exhausted. By default it counts manifest `GET`s only, like Docker Hub; kuik's checks are
`HEAD`s, so count both. A rule attaches it to the traffic:

```sh
curl -fsS -X PUT "$ADMIN/api/v1/quotas/demo" \
  -d '{"set": "demo", "limit": 5, "windowSec": 600, "key": "global", "methods": ["GET", "HEAD"]}'
curl -fsS -X POST "$ADMIN/api/v1/rules" -d '{
  "set": "demo",
  "target": {"host": "upstream", "repo": "acme/demo/app", "tag": "t-quota-*"},
  "effects": {"quota": "demo"}}' | jq .rule.id
deploy quota1 upstream.$D/acme/demo/app:t-quota-1
curl -fsS "$ADMIN/api/v1/quotas/usage" | jq
deploy quota2 upstream.$D/acme/demo/app:t-quota-2
```

The first pod's admission and pull use up most of the counter (kuik's `HEAD`, then containerd's
manifest requests); once the next kuik probe sees `RateLimit-Remaining: 0` or a 429, the pod goes
to `alt1`. If the second pod still starts from `upstream`, the counter had some left: start a third
one on `t-quota-3`. The Settings page shows the
counter live and resets it (`POST /api/v1/quotas/demo/reset`).

## 3. ImageMonitor: why an image is unavailable

An `ImageMonitor` checks the images of the pods it selects in the background, one image per check
window of each registry, and lists the unavailable ones in its status with the reason the registry
gave. Use one kuik-tester host per failure, so that each check runs on its own series of windows:

```sh
kubectl apply -f - <<YAML
apiVersion: kuik.enix.io/v1alpha1
kind: ImageMonitor
metadata:
  name: demo-mon
spec:
  namespaceSelector:
    matchLabels:
      kubernetes.io/metadata.name: $NS-mon
YAML
for h in notfound denied ratelimited down; do deploy mon-$h $h.$D/acme/demo/app:t-mon "$NS-mon"; done
```

Wait until the four images are checked available
(`kubectl get imagemonitor demo-mon -o jsonpath='{.status.images.origin}'`), then break each host
its own way:

```sh
# 404
curl -fsS -X POST "$ADMIN/api/v1/rules" -d '{"set": "demo",
  "target": {"host": "notfound", "repo": "acme/demo/app", "tag": "t-mon"}, "effects": {"status": 404}}'
# authentication: a grant restricts the repository to one user; kuik checks anonymously
curl -fsS -X PUT "$ADMIN/api/v1/users/demo" -d '{"set": "demo", "password": "demo-password"}'
curl -fsS -X POST "$ADMIN/api/v1/grants" -d '{"set": "demo",
  "host": "denied", "repo": "acme/demo/**", "actions": ["pull"], "users": ["demo"]}'
# 429
curl -fsS -X POST "$ADMIN/api/v1/rules" -d '{"set": "demo",
  "target": {"host": "ratelimited", "repo": "acme/demo/app", "tag": "t-mon"}, "effects": {"status": 429}}'
# connection reset (TCP RST, no HTTP response)
curl -fsS -X POST "$ADMIN/api/v1/rules" -d '{"set": "demo",
  "target": {"host": "down", "repo": "acme/demo/app", "tag": "t-mon"}, "effects": {"reset": true}}'
```

The same faults are on the Images page (404, 401, 429, reset buttons) and on the Settings page
(users and grants).

What kuik does, within one lap of checks:

```sh
kubectl get imagemonitor demo-mon -o json | jq '.status.unavailableImages'
```

- four entries `{ref, reason, since, pods}`, with the reasons `ManifestNotFound` (`notfound`),
  `Unauthorized` (`denied`), `QuotaExceeded` (`ratelimited`) and `Unreachable` (`down`); `since`
  stays put while the failure lasts;
- condition `ImagesUnavailable=True`, while `Ready` stays `True`;
- no event, except `AnonymousAccessDenied` (Warning) on the monitor for the `Unauthorized` image,
  since the check is anonymous and no `fallbackAuth` entry of kuik's configuration matches
  `denied.$D`.

In the request log, `curl -fsS "$ADMIN/api/v1/requests?host=down&caller=kuik"` shows status `0`
for the resets, and `requests?host=denied` shows the 401 challenges. A pod that pulls with the
right credentials would show the user in the `user` column.

Lift the faults: `DELETE /api/v1/sets/demo` removes every demo object created so far (the rules
of step 2 included), or delete them one by one (`DELETE /api/v1/rules/{id}`,
`DELETE /api/v1/grants/{id}`, `DELETE /api/v1/users/demo`). Each entry leaves
`unavailableImages` at its next check.

**Drift.** A monitor also reports a tag that moved upstream to new content. kuik-tester moves a
tag without a push:

```sh
curl -fsS -X POST "$ADMIN/api/v1/drift" -d '{"host": "notfound", "repo": "acme/demo/app", "tag": "t-mon"}'
```

The pod still runs the old digest, so the monitor lists the image in `status.driftedImages`, sets
`ImagesDrifted=True` and emits `ImageTagDrifted` (Warning). A new pull of the tag (a pod with
`imagePullPolicy: Always`, restarted) runs the new digest and clears it. Drift detection compares
the digest the container runtime reports for the running image. It holds when the pod pulled the
tag itself before the drift (not from an image preloaded on the node), so the runtime reports the
index digest kuik compares.

## 4. ImageMirror: copy, fall back to the copy, restore it

An `ImageMirror` copies the images of the pods it selects to a destination registry, under the
destination path plus the full origin reference, with the kuik cluster ID appended to the tag. The
destination here is the proxy host `mirror`, which forwards to the in-chart zot. It has no
authentication as long as no grant names it, so the mirror needs no credentials.

```sh
kubectl apply -f - <<YAML
apiVersion: kuik.enix.io/v1alpha1
kind: ImageMirror
metadata:
  name: demo-mirror
spec:
  namespaceSelector:
    matchLabels:
      kubernetes.io/metadata.name: $NS
  podSelector:
    matchLabels:
      app: mirrored
  rewritePolicy: OnFailure
  destination:
    path: mirror.$D/demo/
  cleanup:
    enabled: true
    retention: 1h
  driftPolicy: Ignore
YAML
CID=$(kubectl -n kuik-system get configmap kuik-cluster-id -o jsonpath='{.data.clusterID}')
curl -fsS "$ADMIN/api/v1/destinations/mirror" | jq    # nothing under demo/ yet
```

kuik copies nothing in advance. Start a pod:

```sh
deploy mirrored upstream.$D/acme/demo/mirrored:t-mir
```

At the next copy window of `upstream`:

- the destination holds the repository `demo/upstream.kt.example.com/acme/demo/mirrored` with the
  tag `t-mir_$CID`, whose digest is the index digest that `resolve` reports for the origin:
  `curl -fsS "$ADMIN/api/v1/destinations/mirror" | jq` (the Destinations page shows the same);
- an `ImageCopied` event (Normal) on the mirror:
  `kubectl get events -A --field-selector involvedObject.kind=ImageMirror,involvedObject.name=demo-mirror`;
- `status.repositories` lists the destination repository and `status.images.copy` counts the
  image: `kubectl get imagemirror demo-mirror -o yaml`;
- the request log shows the copy: kuik's `GET`s on `upstream`, then on `mirror` the blob uploads
  and the `manifest-put`s, all `forwarded: true`:

  ```sh
  curl -fsS "$ADMIN/api/v1/requests?host=mirror&kind=manifest-put" \
    | jq -r '.[] | [.method, .repo, .reference, .status, .forwarded] | @tsv'
  ```

### The origin fails: the pod runs from the copy

```sh
curl -fsS -X POST "$ADMIN/api/v1/rules" -d '{"set": "demo",
  "target": {"host": "upstream", "repo": "acme/demo/mirrored", "tag": "t-mir"},
  "effects": {"status": 503}}' | jq .rule.id
sleep 10
kubectl -n "$NS" rollout restart deployment/mirrored
```

The new pod runs
`mirror.kt.example.com/demo/upstream.kt.example.com/acme/demo/mirrored:t-mir_<cluster ID>`,
with an `ImageFallback` event naming `ImageMirror/demo-mirror`, and the mirror reports
`FallbackActive=True/OriginUnavailable` with an entry in `status.activeFallbacks`. On a node that
already holds the image, containerd only sends `HEAD`s to `mirror`; on a fresh node the log shows
its `GET`s there.

### A copy deleted behind kuik's back

A destination action changes zot directly (it does not appear in the request log). Delete the tag
from the Destinations page, or:

```sh
REPO=demo/upstream.$D/acme/demo/mirrored
curl -fsS -X POST "$ADMIN/api/v1/destinations/mirror/delete-tag" \
  -d "{\"repo\": \"$REPO\", \"tag\": \"t-mir_$CID\"}"
```

At its next destination pass the mirror notices the missing tag and writes it again (a tag `PUT`
when the manifest is still there); `delete-manifest` with the digest removes the index itself, and
the mirror writes it again with an `ImageRecopied` event (Warning). A destination that refuses
`DELETE` is one rule away: the proxy host `nodelete` forwards to the same zot, and a rule
`{"target": {"host": "nodelete", "kinds": ["delete"]}, "effects": {"status": 405}}` makes it refuse
cleanup deletions.

## Clean up

```sh
kubectl delete namespace "$NS" "$NS-mon"   # the pods first: a mirror in use stays Terminating
kubectl delete imagealternative demo-alt
kubectl delete imagemonitor demo-mon
kubectl delete imagemirror demo-mirror
curl -fsS -X DELETE "$ADMIN/api/v1/sets/demo"
curl -fsS -X POST "$ADMIN/api/v1/destinations/mirror/purge" -d '{"prefix": "demo/"}'
curl -fsS -X POST "$ADMIN/api/v1/stats/reset"
```

Rules created from the Images page carry no set: remove them with the "OK" button of their row or
from the Rules page. `GET /api/v1/export` saves the whole kuik-tester state as YAML, and
`POST /api/v1/import` loads it back, to replay a setup later.

## Going further

- [Architecture](./architecture.md): how kuik-tester classifies, breaks and logs requests.

# kuik-tester architecture

kuik-tester replaces a bench of real registries (several `registry:2` instances, a test CA on
every node, images pushed by hand, failures provoked with scale-to-zero, nginx or toxiproxy) with
one service. It plays every registry kuik reads from or writes to, breaks them on demand, and
records what each client asked. This page describes how it is built and why.

## Goals and constraints

- **No push and no setup per image.** Any `<host>.<baseDomain>/<repo>:<tag>` pulls.
- **Every failure a kuik scenario needs on the registry side is one action**, effective on the
  next request.
- **The request log is evidence**: count of `HEAD`/`PUT`/`DELETE` per host, which user
  authenticated, which digest was served, which rule answered.
- **Real clients must see a normal registry.** go-containerregistry (used by kuik) and containerd
  (used by the kubelet) must get correct digests, `Docker-Content-Digest` headers, identical bytes
  whatever the `Accept` header, and images that run.
- **Test pods stop at once**: the image handles `SIGTERM`.
- **No fault leaks between targets**: a rule on `acme/foo` never touches `acme/foo-bar`.
- **The admin surface is never reachable through the registry endpoint.**

Out of scope: large images and throughput limits, multi-user isolation (one shared state),
multi-step scenarios (rules are unitary; the state is exported and imported as YAML), Docker Hub
short-name normalization, cloud provider token services, more than one replica, a Prometheus
endpoint (the request counters of the API are the evidence).

## Components

One Go binary, one container image, one replica. Each package documents its own role in its
`// Package` comment.

```text
                *.kt.example.com (registry Ingress, wildcard TLS)
kuik / kubelet ──▶ Ingress ──▶ :8080 registry listener ─┬─ synthetic hosts (generated images)
                                                        └─ proxy hosts ──▶ zot (or any registry)
browser / e2e  ──▶ port-forward or admin Ingress ──▶ :8081 admin listener (web UI + /api/v1)
                   optional :5000 plain-HTTP registry listener (one fixed host label)
```

### Listeners

- **Registry listener** (`:8080`): the host is the `Host` header, port and trailing dot ignored,
  case-insensitive. `<label>.<baseDomain>` is the host `<label>`. A label declared in `proxies`
  is forwarded; any other label is a synthetic registry, created on first request. The bare base
  domain, a nested label and a foreign domain answer 404 `NAME_UNKNOWN`. Optional TLS from a
  certificate file, reloaded when the certificate or key file changes (checked at each handshake,
  so a renewed Secret is picked up without a restart; a pair that fails to load is logged and the
  previous one stays in use); it serves HTTP/1.1 only (see the `reset` effect below).
- **Admin listener** (`:8081`): a separate port and Service, never routed by the registry Ingress,
  so a forged `Host` on the public endpoint cannot reach it. It has no authentication of its
  own: access control belongs to whatever exposes it (a port-forward, or an Ingress with an
  allowlist or authentication).
- **Plain-HTTP listener** (optional): the same handler for `insecure: true` registries. Its
  `Host` (`IP:5000`) does not end in the base domain, so every request on it maps to one fixed
  label, `plainHTTPHost`.

- **Timeouts of the listeners**: `ReadHeaderTimeout` 10 s, `IdleTimeout` 120 s, no write timeout
  (the SSE stream, latency faults and uploads are unbounded). A header block over 64 KiB answers
  431. At shutdown, the in-flight requests of every listener are cancelled and the listeners get
  10 s in all to stop (one deadline shared by the sequential shutdowns); one that does not stop in
  time is logged.

## Request flow

1. **Classify.** The path gives the endpoint kind: `ping` (`/v2/`), `manifest-by-tag`,
   `manifest-by-digest`, `blob`, `upload` (blob `POST`/`PATCH`/`PUT`), `manifest-put`, `delete`,
   `tags-list`, `catalog`, `token`. References are validated (the distribution spec's tag
   grammar, `sha256:<64 hex>` digests, no `.` or `..` path segment). An endpoint that is not
   classified answers 404 and is never forwarded, on proxy hosts too: every request that reaches
   a backend has a kind for rules, auth and the log. A host label outside
   `[a-z0-9][a-z0-9_-]{0,62}`, or a repository over 255 characters or 32 segments, answers 404
   `NAME_UNKNOWN`, is logged, creates no host and records no image.
2. **Identify the caller.** Basic credentials or a Bearer token. Invalid ones answer 401 with the
   challenge, and the log records `<name> (invalid)` or `(invalid token)`.
3. **Fault rules** (below).
4. **Authorization**: grants, 401 without credentials, 403 for a user without the right.
5. **Quota**: a refused request (401/403) consumes nothing.
6. **Serve**: generate the synthetic content, or forward to the backend.
7. **Log** the request, whatever happened: unknown hosts and unclassified endpoints included.

## Rule engine

A **target** selects requests by `host` (glob on the label, stored lower-case), `repo` (glob per
path segment: `*` is one segment, `**` any number, so `acme/foo` never matches `acme/foo-bar`),
`tag` (glob), `kinds`, `methods` and `caller` (`anonymous` or a user name). A request by digest has
no tag: a rule with a tag other than `*` matches it only when its kinds name `manifest-by-digest`.

**Fault rules** are an ordered list; the first enabled rule that matches wins. First-match is
easier to reason about and to display than most-specific-wins: the UI reorders rules, edits them
in place, and a one-click action on an image row inserts an exact-target rule at the top. Effects,
combinable in one rule:

| Effect | Behaviour |
| --- | --- |
| `status` | an HTTP status with the matching OCI error body: 401 `UNAUTHORIZED` with the host's challenge, 403 `DENIED`, 404 `MANIFEST_UNKNOWN`/`BLOB_UNKNOWN`/`NAME_UNKNOWN` by kind, 405 `UNSUPPORTED`, 429 `TOOMANYREQUESTS`, others `UNKNOWN` |
| `latencyMs` | a delay before the first byte; a client that leaves during it gets nothing (status 0 in the log) |
| `reset` | the connection is closed with a TCP RST, no TLS `close_notify`, so Go clients see `ECONNRESET` and treat it as a real reset |
| `headers` | extra response headers, such as `RateLimit-Remaining: 0;w=21600` |
| `quota` | the name of a quota counter that applies to the matching requests |
| `pass` | no fault: exempts a narrower target from a broader rule below it |

They apply in this order: headers on whatever follows, latency, reset, status, else normal
serving. A `HEAD` under a fault gets the status and headers of the `GET`, without a body. On a
proxy host a `status` or `reset` answers instead of forwarding; `latency` and `headers` wrap the
forwarded request. The TLS listener serves HTTP/1.1 only because under HTTP/2 a reset can only
cancel a stream, which go-containerregistry does not retry like a connection reset.

### Settings

Settings are properties of hosts and repositories, not faults. First match wins per type, except
grants.

- **Users** (name and password) are global and valid on every host, proxies included.
- **Grants** give users `pull`, `push` or `delete` on a host and repo glob. When several grants
  match, their users are united: a repository is restricted as soon as one grant matches it.
  Union rather than first-match lets a `manage` grant (pull, push, delete) and a `pull` grant
  coexist on one destination.
- **Auth scheme per host**: `basic` (default) or `bearer`. A Bearer host serves its own token
  endpoint (`/token` on the same host, kind `token`, so token failures can be injected): `GET`
  with optional Basic credentials as go-containerregistry sends, `POST` with the OAuth password
  grant that containerd sends first when it has credentials. Tokens are opaque, live 300 s in
  memory (at most 10 000 live tokens: past that an arbitrary one is dropped and its client
  re-authenticates on the 401) and are bound to the user only; rights are checked on every
  request. The realm is
  `<scheme>://<Host>/token` with the request's `Host` verbatim, since go-containerregistry
  accepts a realm on the registry's own host only; the scheme comes from a trusted
  `X-Forwarded-Proto`, else from the listener.
- **Without credentials**: a Basic host without grants serves everything; with a grant, `/v2/`
  and `_catalog` challenge anonymous callers and the repositories no grant covers stay open. A
  Bearer host challenges every request without credentials except `/token`, like Docker Hub:
  containerd never pings `/v2/`, so only a challenge on the repository endpoint makes it fetch a
  token.
- **Quotas**: a name, a limit N, a window W, a key (`global`, client `ip`, `user`) and the kinds and
  methods counted (default: manifest `GET`s, like Docker Hub, which does not count `HEAD`). Every
  counted answer carries `RateLimit-Limit: N;w=W` and `RateLimit-Remaining: R;w=W`; an exhausted
  quota answers 429 with the same headers. A quota applies through a rule with the `quota` effect,
  so it can be scoped (to anonymous callers, for one). Counters live in memory. The client IP is
  the socket address, or behind a trusted proxy the first `X-Forwarded-For` entry that is not a
  trusted proxy, read right to left, so entries the client prepends cannot spoof it.
- **Aliases** rewrite a repository prefix when computing the content key (`alt1/mirror/acme` serves
  `acme`), for alternatives that live on another path.
- **Divergences** salt the content key, so a host serves other digests for the same `repo:tag`.
- **Platform rules** narrow a repository to some platforms.

## Synthetic images

- Every synthetic image is an OCI index of two platform images, `linux/amd64` and `linux/arm64`,
  served for any `repo:tag` without a push. A platform rule (`POST /platforms`, `{host, repo,
  platforms}` with globs like a fault rule, first match wins) narrows a repository to one
  platform: the index then holds one manifest, with another digest. Each has one layer holding a
  static `sleep` binary as entrypoint, which exits at once on `SIGTERM`, and a config whose labels
  carry the content key and the generation. The config, manifest and index digests are therefore
  unique per content and generation, while the binary layer is shared by every image, so
  cross-repository mounts and blob deduplication behave as with real images.
- The entrypoint `/sleep` (`cmd/sleep`) blocks until `SIGTERM`. With an argument (`args: ["10s"]`
  in a pod: a Go duration, a number of seconds such as `0.5`, or `infinity`) it exits 0 after that
  time, so an initContainer can complete. The image declares no user: a pod under the Pod Security
  `restricted` profile sets `runAsUser` itself.
- The layer is an uncompressed tar, so its digest equals its diffID, and generation is
  deterministic (fixed tar headers, zero timestamps).
- **Content key**: the repository path without the host, after alias resolution, plus a divergence
  salt. By default `upstream/acme/foo:t1` and `alt1/acme/foo:t1` serve the same digest:
  alternatives are equivalent out of the box.
- **Generation**: per (host, repo, tag), default 1. **Drift** increments it for every seen
  (host, repo, tag) matching a target (host, repo and tag globs), which moves the tag to new
  content; a literal target that was never seen starts at generation 2, so a tag can be drifted
  before its first pull. Drift and divergences match the request path, not the content key: a
  drift on `upstream` leaves an alternative on the previous generation, like a lagging mirror.
- **Expiry**: a seen image, its generation and its digests carry a last-seen time, and are
  dropped once not seen for `expireAfter` (default `7d`; `0` disables). A pull or a by-digest
  fetch refreshes it, written at most once per `min(1h, expireAfter/10)`. A state file from
  before expiry, an import, and a drift on a tag never seen all start their clock when the next
  sweep finds them, so nothing disappears before a full `expireAfter`. A sweep runs every
  `min(10m, expireAfter/10)`.
- **Digest index**: every manifest, index and blob served is recorded and persisted, so a pinned
  `repo@sha256:...` keeps working after a drift and after a restart. The seen images are capped
  at 10 000 (a new one replaces the one first seen longest ago) and the digest index at 50 000
  (entries other than the new image's are evicted, so a client that pulls only by digest may get
  `MANIFEST_UNKNOWN` until the tag is pulled again). A persisted manifest digest that the
  current build no longer produces (another Go release or module path) answers
  `MANIFEST_UNKNOWN`. A manifest by digest is
  served on a repository only when its content key matches the repository's (any generation);
  that is what makes a divergent alternative answer `MANIFEST_UNKNOWN` to a pinned digest.
- Any tag exists unless a rule says otherwise. `tags/list` returns the tags seen or named by a
  rule. Write endpoints answer 405: synthetic hosts are read-only.
- The `sleep` binaries are built reproducibly with the image and embedded with the `sleepbins`
  build tag; without it they are placeholder bytes (see
  [the sleep binaries](../README.md#the-sleep-binaries)).
  Building them with another Go release changes every synthetic digest.

## Proxy hosts and destination actions

- A proxy host forwards to its backend (`http(s)://host[:port]`), streaming bodies both ways. It
  rewrites absolute `Location` headers that name the backend to the public scheme and host, sends
  `X-Forwarded-Proto` and `X-Forwarded-For`, and never forwards the client's `Authorization`:
  kuik-tester enforces auth itself and the backend runs without. Headers it sets (rule headers,
  `RateLimit-*`) replace the backend's. A backend that sends no response headers within 30 s fails
  the request, and so does an unreachable one: destination actions and proxied requests both fail,
  proxied requests with 502. Backend traffic never goes through an environment proxy.
- Several proxy hosts may share a backend: `nodelete` is `mirror` plus a rule that answers 405 to
  `DELETE`. A proxy name may be a pattern without a dot (`mirror-*`): the exact name wins, then
  the first matching pattern.
- **Destination actions** act on the backend directly, so they never appear in the request log:
  inventory (repositories, tags and digests; entries that vanish during the listing are skipped),
  delete a tag, delete a manifest by digest (the tags pointing to it, then the manifest; platform
  manifests and blobs are left to the registry's garbage collection), create a tag on an existing
  manifest, push a synthetic image, purge every tag under a repository prefix (or of every repository:
  `purge-all`) along with the manifests and the blobs they point to in each purged repository, a
  backend that refuses a manifest or blob deletion (404, 405) not being an error. Invalid input
  answers 400 before any request; backend errors answer 502, a deadline included. Actions on one
  repository (delete, tag, push) stop after 1 min; inventory and purge, which walk every
  repository, after 5 min.
- The chart's zot runs with `DELETE` enabled, deduplication, and a short explicit retention of
  untagged manifests, so a deleted index and its orphans go within the retention delay
  plus one garbage-collection interval (about 15 minutes with the chart's defaults).

## Request log

- An in-memory ring buffer (10 000 entries by default, `logSize`), not persisted. Entries older than `expireAfter` are dropped (the cumulative counters are not
  touched).
- Each entry: time, host, client IP, user, User-Agent and caller class, method, kind, repository,
  reference, status, served digest, duration, matched rule (`pass` included), and whether it was
  forwarded. A request without an HTTP response (reset, client gone during latency) has status 0.
  Every logged string is cut to 256 bytes with an ellipsis.
- **Caller class**, from the User-Agent: `crane/` is `crane`; else `kuik` or `go-containerregistry`
  is `kuik`; else `containerd` is `containerd`; anything else is `other`.
- Queries filter on host, repository, kind, caller, status and `since`; NDJSON export; a live SSE
  stream; counters by (host, kind, method, status), with a reset. Requests for an unknown host are
  counted under `(unknown)`, so arbitrary `Host` headers cannot grow the counters. The counters keep
  at most 10 000 (host, kind, method, status) keys; later new keys count under the host
  `(overflow)`. A method outside the registry's verbs counts as `OTHER`.

## Admin API

Everything the UI does goes through `/api/v1`: it is the contract for scripts and e2e tests.

- **Validation against the whole resulting state**: every write (import included) is refused with
  400 when the resulting state is invalid (globs, kinds, statuses 400 to 599, `pass` combined with
  `status` or `reset`, unknown users or quotas). A rule's headers cannot set
  `Content-Length`, `Transfer-Encoding`, `Connection`, `Host`, `Upgrade` or `TE`. JSON and import
  bodies over 8 MiB answer 413; a UI form over 8 MiB is refused with an error message. Deleting a
  user or quota still referenced is refused, never cascaded. One validation path, and the serving
  side stays permissive.
- **Cross-origin guard**: the admin has no authentication, so a non-`GET` request whose `Origin`
  names another host is refused, and no `GET` changes state (resolving an image, which records it
  as seen, is a `POST`).
- **Conventions**: a rule created without `enabled` is enabled; every `POST` and `PUT` answers the
  stored, normalized object as a later `GET` returns it; host globs are stored lower-case; import
  assigns missing IDs, so hand-written YAML needs none; a `PUT` replaces the whole object, its
  set included.
- **Sets**: every state object (rule, user, grant, alias, divergence, quota, host auth, platform
  rule) may carry a set name (`[A-Za-z0-9_.-]`, 64 characters at most). Objects created by one
  test or fixture share a set, so cleanup is robust to a crashed test run.
  - `POST /import?set=<name>` merges a YAML document into the state and tags its objects instead
    of replacing the state (a plain `POST /import` does replace it). ID-keyed objects go on top in
    file order; name-keyed ones (a user, a quota) replace the entry of the same key, unless
    another set owns it, which refuses the import.
  - `GET /export?set=<name>` exports a set, `GET /sets` lists the names with their object counts.
  - `DELETE /sets/{name}` removes the whole set in one validated update: a grant outside the set
    that names a user inside it refuses the whole deletion.
- **Purge by type**: `DELETE /{rules|aliases|divergents|grants|platforms|users|hostauth|quotas}`
  empties one list in one validated update, set-tagged objects included, and answers
  `{"deleted": n}`; purging quotas resets their counters. `DELETE /images` empties `seen`,
  `digests` and `generations` (a rule that names an image literally keeps it listed) and
  `DELETE /log` empties the request log ring, keeping the stats. These are plain `DELETE`s on
  a collection path, with no conflict with `DELETE /rules/{id}`; the same-origin check covers them.
- **Purge everything**: `POST /api/v1/purge` (`{"backends": bool}`) resets the whole state to empty in
  one `store.Update`, skipping the validation of `Service.update` (an empty state is valid and nothing
  dangles), resets every quota counter, then clears the request log and its stats. The log is not
  in `Service`, so a handler-level `purgeAll(svc, log)` shared by the API and the UI does that last
  step. With `backends`, each distinct backend (`dest.Proxies.Backends`, deduplicated by scheme://host)
  is purged once; a failing backend is reported in the result (HTTP 200) and does not stop the
  others. The State page requires the typed word `purge`, checked server-side.

## State and configuration

- **Static configuration** (YAML, read at start, unknown keys rejected): `baseDomain`, listener
  addresses, `plainHTTPHost`, TLS files, `proxies`, `trustedProxies`, `statePath`, `logSize`, `expireAfter`.
  Every problem is reported at once with its key path (`proxies[1].backend`), after
  `reading`, `parsing` or `validating config <path>:`. `logSize` must be at least 1, `expireAfter` (a Go duration, `<n>d` or `0`) must not be negative, and an
  explicit empty `registryAddr`, `adminAddr` or `plainHTTPHost` is refused instead of falling back
  to its default, so a chart or compose value set to `""` fails at start.
- **Dynamic state** (rules, settings, users, quotas, generations, seen images, digest index): in
  memory, persisted to a JSON file on each change when a state path is set. Quota counters and
  the request log are not persisted.

State file:

- When `statePath` is set, startup refuses a state directory that is missing or not writable.
- It is written atomically (temporary file, fsync, rename) before the change is published, so a
  failed write changes nothing.
- Exception: an image recorded by a pull or a resolve is published even when the write fails, and
  the failure is logged as an error. The pull succeeds; the image is kept in memory until the next
  successful write, and lost if the tester restarts first.
- A missing or empty file is an empty state.
- A file that cannot be parsed is moved aside to `<statePath>.corrupt` (replacing an older one),
  logged as an error, and the tester starts empty.
- A state saved by an older version that holds a rule setting one of the framing headers above is
  loaded, but every change to the rules, users and other state objects is
  refused until that rule is deleted (`DELETE /api/v1/rules/{id}`, or the Rules page) or its
  header removed. A whole-state import (`POST /api/v1/import` without `set`, or the State page's
  replace) also clears it: the imported state replaces every rule, and only that state is validated.

## Logs

The tester logs with `slog`: capitalised past-tense messages (`Started listening`, `Stopped on an
error`) and the key `error` for an error value.

## Deployment

- One replica with `strategy: Recreate` (the state volume is `ReadWriteOnce`).
- A distroless image running as UID/GID 65532 with a read-only root filesystem.
- The admin is never on the registry Ingress.

Installation and operation are described in
[the README](../README.md#install-on-a-cluster). kuik-tester may run in the cluster it tests, as
long as no kuik CR routes the registry its own image comes from. On a cluster without an Ingress
(a kind cluster, say), the binary terminates TLS itself from a certificate Secret.

## How kuik talks to registries

These client behaviours (those of kuik v3, the release kuik-tester was first built for) shaped
the design; the kuik documentation is the reference for its contract.

- All of kuik's registry I/O goes through go-containerregistry: a `GET /v2/`, then the
  `WWW-Authenticate` challenge flow, Basic and Bearer.
- Availability probes and monitor checks are manifest `HEAD`s by tag or digest.
- A copy reads the source manifest, child manifests and blobs, then on the destination checks the
  manifest by digest, the blobs, attempts a cross-repository mount, uploads each missing blob in
  one `PATCH` and writes the manifests. Cleanup deletes tags.
- kuik reads `RateLimit-Remaining` on manifest answers: `0` means the quota is exhausted.
- A registry host is recognized by a dot, a port or `localhost`, so each `<label>.<baseDomain>`
  is a distinct registry for kuik, paced on its own.

The User-Agent decides the caller class, so whether the log tells kuik's webhook from its
controller depends on kuik: today both send the same one.

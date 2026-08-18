# kube-image-keeper — Design v3

A redesign of kuik's CRDs around three **cluster-scoped-only** resources (namespaced variants dropped), with clearly separated responsibilities:

| CRD | Mission |
|---|---|
| `ImageAlternative` | Declare equivalent refs on other registries, so the webhook rewrites a pod's image at admission toward an available alternative |
| `ImageMirror` | Replicate the images of selected pods (`podSelector`/`namespaceSelector`) to a registry **and act as a routing destination** (copy + serve: lifecycle — retention, cleanup, verification, re-copy — plus a `rewritePolicy`) |
| `ImageMonitor` | Continuously monitor the availability (and drift) of the images of pods selected by its selectors — the default usage being without a selector, hence de facto all images used in the cluster |

Division of labor, to be documented as-is: **the monitor observes (continuously), the webhook decides (at admission, based on the ImageAlternatives and ImageMirrors), the mirror acts (copies, verifies, deletes)**. The data flow between components is strictly **unidirectional and passes exclusively through the API server** (specs, status, pod annotations) — no private communication channel between controllers.

On naming: no more `Cluster` prefix — this follows the k8s convention, where the prefix only serves to distinguish from a namespaced sibling (`Role`/`ClusterRole`); without a sibling, cluster-scoped kinds are bare (`Node`, `StorageClass`, `PriorityClass`). One-way door, assumed: if namespaced variants ever appear, they are the ones that will have to stand out. `ImageMirror` (and not `ImageCopy`) because "mirror" implies **copy + serve** in registry vocabulary — the CR routes, a passive-artifact name would have masked it.

---

## 1. Cross-cutting principles

### 1.1 Image matching by prefix (no more regex, no more tag handling)

A reference ending in `/` → **subtree** of repositories; otherwise → **exact repository**, all tags/digests included. This is the containerd/CRI mirror semantics, familiar to operators.

```yaml
quay.io/enix/            # everything under enix/
quay.io/thanos/thanos    # a single repository (all tags)
docker.io/               # a whole registry
```

Refs are normalized before matching (`busybox:stable` → `docker.io/library/busybox:stable`), as today.

### 1.2 No more numeric priority: declaration order is authoritative

`spec.priority` and per-entry `priority` disappear. The order of `alternatives[]` in the YAML is the preference order within a CR; between CRs and between kinds, a **documented total order** applies (`Always` before the original, `OnFailure` after, ImageMirror at the extremes — §2.4), the lexicographic name order only serving as a tie-breaker between CRs of the same kind and same policy. **In the common case, you don't have to worry about it**: most ImageAlternatives match disjoint sets of images, and the order between them has no observable effect. It only becomes relevant, in case of deliberate overlap, to name CRs to control their ordering. Priority leaves the API; it lives in an optional naming convention, trivial to audit.

### 1.3 Pod selection: `podSelector` / `namespaceSelector`

No custom filtering structure: two **standard** `metav1.LabelSelector`, the exact pattern of NetworkPolicy and admission webhooks — `matchLabels`/`matchExpressions`, validated by the OpenAPI schema, known to the whole ecosystem.

```yaml
podSelector:                 # labels of the POD
  matchExpressions:
  - {key: cnpg.io/podRole, operator: NotIn, values: [instance]}
namespaceSelector:           # labels of the NAMESPACE
  matchLabels:
    env: prod
```

- Standard LabelSelector semantics: **AND between the two selectors**; an absent or empty selector (`{}`) = matches everything — a spec with no selectors thus covers all pods in the cluster, with no sentinel to invent.
- Namespace by **exact name** via the automatic label `kubernetes.io/metadata.name` (k8s ≥ 1.22); exclusions are written as `matchExpressions` (`NotIn`, `DoesNotExist`) — no custom `exclude` block.
- Gone versus the historical filter: the **annotation** dimension (the core API never selects by annotation — opt-in/opt-out is done by label) and the **image** dimension — image selection belongs to the `alternatives` entries of ImageAlternatives (prefixes, §1.1); mirror and monitor process *all* images of selected pods, and the pathological case (CI namespaces with unique tags) is excluded via `namespaceSelector`.

**Why not CEL**: the simple case becomes verbose, errors shift from admission to runtime, and above all **several mechanisms of the design require statically analyzable selections** (`AmbiguousRewrite` detection, candidate computation for the mirror, anti-loop rules) — LabelSelectors and prefixes can be intersected and compared; a CEL expression is an evaluable black box that can neither be intersected nor compared. If the need for expressiveness emerges, an **additive** `matchConditions` field (CEL as a final AND after the typed selectors), mirroring what Kubernetes admission did: the selectors stay, CEL refines. It can only restrict, so static analysis by over-approximation remains correct.

### 1.4 Authentication: discriminated union `auth`, secrets resolved in a single namespace

Everywhere `credentialsSecretRef` used to be:

```yaml
auth:
  secretRef:                    # classic docker-registry secret — NO namespace
    name: quay-pull             # field: resolved in the "cluster resource
                                # namespace" (kuik's install namespace,
                                # configurable via operator flag)
  injectPullSecret: true        # default true for secretRef
# --- OR ---
auth:
  provider:                     # ambient cloud identity (inspiration: Flux's
    name: aws                   # `provider` field; go-containerregistry keychains)
    serviceAccountRef:          # optional: TokenRequest on a dedicated SA →
      name: mirror-pusher       # one IAM role per CR instead of the
                                # controller's global identity
  injectPullSecret: false       # default false for provider; true = kuik
                                # materializes + RENEWS + injects a docker-
                                # registry secret (cross-cloud case, 12h ECR tokens)
```

**Why `secretRef` without a namespace** — two arguments, one of them a decisive security concern:

1. **Anti-*confused-deputy***: with a free namespace, anyone able to create a kuik CR (cluster-scoped) could reference another team's secret (`namespace: team-b`) with `injectPullSecret: true` and have it copied into a namespace they control — kuik would become a secret-exfiltration channel. Resolving in a single namespace = referencing a secret requires being able to write it in `kuik-system`, i.e. being kuik's operator: the trust loop is closed. Exact precedent: cert-manager's `ClusterIssuer` and its `--cluster-resource-namespace`.
2. **Uniform read RBAC**: the three components only need a Role namespaced to `kuik-system` — no cluster-wide `get secrets` anywhere (§6.2).

Rules:

- Exactly one key among `{secretRef, provider}` (admission CEL). **Closed** provider enum (`aws | gcp | azure`) — no `exec:` credential helper (security surface, arbitrary binaries in a minimal image). Region/project derived from the registry hostname.
- **Absence of `auth` = kuik does nothing**: no injection (relying on existing `imagePullSecrets` or the kubelet's credential provider) **and anonymous checks**. Documented pitfall: a private alternative without `auth` will appear perpetually `Unavailable` — a persistent anonymous 401/403 triggers a `Warning` event suggesting the likely oversight.
- A single `auth` block per ImageAlternative entry (no separate `pullAuth`): the controller's checks and the kubelet's pull are two **read** operations on the same registry, the same credential fits — only whether to inject differs, hence the boolean. Splitting into two credentials only exists on the mirror, where push/delete and pull are **privileges different by nature**.
- Asymmetric `injectPullSecret` defaults, and justified: with a static secret, if the controller needs it, so does the kubelet; with a provider, the majority case is same-cloud where the kubelet is natively authorized.

### 1.5 Status philosophy: bounded, reconstructible, written on transitions

Rule applied to the three CRDs:

1. **The status contains only aggregates + anomalies.** Humans and alerting want to know *what's wrong* — not 3,000 `Available` lines. In steady state, the anomaly lists are empty.
2. **We only persist what is costly to lose.** The exact criterion is not mere reconstructibility, it's the **cost of loss**, which defines three tiers:
   - **Irreversible loss → etcd.** "This image is no longer referenced since this date" (`retainedImages` of the monitor, `pendingDeletion` of the mirror — minimal ref + timestamp entries): losing `unusedSince` shortens or cancels a retention, a permanent damage. And the **list of repositories** populated by the ImageMirror (`status.repositories`, §3.4): the OCI Distribution spec only guarantees `GET /v2/<name>/tags/list` per repository — **no** global catalog (`_catalog` is non-standard and often requires system rights, unavailable with project-scoped credentials such as Harbor's). The set of pushed repos is therefore not rediscoverable from the registry; without it, a repo whose last reference disappears while the controller is down leaks forever. Same logic as a Flux `Kustomization`'s `status.inventory`. Crash-recovery conservative by construction: a `unusedSince` re-stamped at the first reconcile *lengthens* retention, never shortens it.
   - **Bounded, cheap loss → minimal, lazy persistence.** Two cases. The **cursor** of the check scheduler (§6.3): one ref per (CR, registry), written once per tick — its loss costs at worst `maxPerInterval` duplicate HEADs. And the **copy budget** per registry (§3.4): unlike the cursor ("where to resume", benign), it protects a quota actually consumed on the source registry — a restart that forgot it could *exceed* the budget by starting from a fresh state (pathological case of a controller crashing every 10 min). Hence a nuance: we **persist before copying**, not after — reserving the slot *precedes* the action, so that a crash can only waste budget (reserved slot unused), never create it.
   - **Free, self-healing loss → pure memory.** Check verdicts (they re-verify themselves, that's their nature), the set of in-use images (recomputed from pod informers), the per-tag state of each inventoried repo (re-listable via `tags/list`), the webhook caches.
3. **Writes on transitions only** (referencing, aggregate change, anomaly entry/exit) — never at the rate of checks, never in the webhook. etcd hygiene: no write amplification, no watch-storm, no contention. Assumed and quantified exceptions: the check cursor (§6.3), O(registries) writes per interval; the copy budget (§3.4), one write per copy — negligible at the real volume of copies (a copy is a pull+push of several MB/GB, an annotation PATCH is noise next to it).

**Why not a CR per image** (cert-manager `Order`/`Challenge` pattern, Cilium `CiliumEndpoint`): a legitimate pattern — atomic updates, GC via ownerReferences, kubectl UX — but the projects that adopted it had to re-architect at scale: Kyverno moved its `PolicyReport` out of etcd toward a dedicated extension apiserver (etcd pressure and controller caches saturated by the object volume), and Cilium created `CiliumEndpointSlice` to aggregate its per-pod CRs (watch-storm and apiserver load on large clusters). On top of that, a domain-specific problem: an image ref does not fit in a `metadata.name` (hash + label = degraded kubectl UX). O(1) lookup is not an argument: the webhook consumes an informer cache and indexes in memory regardless of storage. If one day the full index must be persisted, the pattern to copy would be **EndpointSlice** (aggregation objects of ~100 entries, bounded size) — kept in reserve.

### 1.6 Canonical identity and equivalence classes

**The problem**: as soon as the webhook rewrites pods (or a user natively writes an alternative ref in their manifest), the same logical image appears in the cluster under several different refs. If the ImageMirror counted references on *observed* refs, the original would appear unused as soon as it is rewritten (→ wrongful cleanup), and the rewritten ref would appear as a new image to copy (→ `mirror/mirror/…` loop).

**Definitions**:

- The **equivalence class** of an image is the set of refs that designate it: the prefixes declared in the ImageAlternatives that match it (each prefix covers a set of images and derives one ref per image), plus the mirror refs derived by the ImageMirrors whose scope covers it.
- The **canonical ref** (the class representative) = the ref derived by the **first declared prefix of the first `ImageAlternative`** (lexicographic order) that matches it; failing that, the ref itself. Mirror refs are **never** representatives (they canonicalize by stripping the destination prefix, step 2 — otherwise the destination layout, computed from the canonical, would self-reference).

**Canonicalization algorithm** for a ref observed in a pod, in order:

1. **Annotation** `kuik.enix.io/original-images` present on the pod (a per-container JSON map set by the webhook on each rewrite, §5.3) → the entry for the container replaces the observed ref. Self-contained: the info travels with the pod, survives controller restarts, does not depend on the CR that rewrote it still existing.
2. Otherwise, a ref prefixed by the **destination of a known ImageMirror** (`destination.path`) → strip the prefix **and the tag suffix `_<clusterID>`** (§3.8) → original ref (the §3.3 layout remains invertible).
3. The resulting ref is **normalized to the representative** of its equivalence class.

**Example** — `ImageAlternative` `thanos` declaring, in order: `quay.io/thanos/thanos`, `ghcr.io/thanos-io/thanos`; an `ImageMirror` `prod-mirror` to `registry.tld/mirror/`. Three pods in the cluster:

| Ref observed in the pod | Step applied | Canonical ref |
|---|---|---|
| `quay.io/thanos/thanos:v0.34.1` | 3 (already the representative) | `quay.io/thanos/thanos:v0.34.1` |
| `ghcr.io/thanos-io/thanos:v0.34.1` (written natively) | 3 (normalization to the 1st prefix) | `quay.io/thanos/thanos:v0.34.1` |
| `registry.tld/mirror/quay.io/thanos/thanos:v0.34.1` (rewritten, annotated) | 1 then 3 | `quay.io/thanos/thanos:v0.34.1` |

→ the ImageMirror counts **one** canonical image referenced by **3 pods**; its computed destination is `registry.tld/mirror/quay.io/thanos/thanos:v0.34.1` — identical regardless of the observed variant.

Annotations set by the webhook on rewrite (per-container JSON maps; they also carry attribution for the statuses, §2.6 — see §5.3 for the full form):

```yaml
metadata:
  annotations:
    kuik.enix.io/original-images: '{"thanos-sidecar":"quay.io/thanos/thanos:v0.42.2"}'
    kuik.enix.io/rewritten-by:    '{"thanos-sidecar":"ImageAlternative/thanos"}'
    kuik.enix.io/reason:          '{"thanos-sidecar":"OnFailure"}'   # OnFailure | Always | NoAlternatives
```

**Consequences**: a copied image stays `inUse` as long as *any member whatsoever* of its class is referenced; the copy is **idempotent** (`strip(destination.path) ∘ copy = identity`); re-copy can be done from any available member of the class. **Non-disableable** safety belt: an ImageMirror excludes by default any ref prefixed by its own destination — no loop possible, even with a lost annotation.

### 1.7 Rates and per-registry budgets: global operator config, not in the CRDs

Check and copy cadences are **not** CRD fields: they are **cluster-wide per-registry** budgets, shared across all CRs (two monitors and a mirror talking to docker.io consume the *same* quota — a per-CR budget would make no sense with respect to the registry's rate limiting), and there is no legitimate reason to have different values per CR. They live in the operator's global config, with a systematic **check/copy distinction**: a check is a HEAD, cheap and rarely rate-limited → high frequency acceptable; a copy involves GETs (manifests + blobs), heavily limited (Docker Hub in particular) → a much stricter budget.

```yaml
# operator config.yaml — NOT in the CRDs
webhook:
  availabilityCheck:
    timeout: 2s              # max time before considering a registry unavailable
    activeCheckCache:        # LOCAL per-replica cache + singleflight: a single
      ttl: 10s               # image used by 50 pods scheduled in a short window
                             # → 1 check, not 50
    skipHints:               # use the NEGATIVE check results from ImageMirror /
      enabled: true          # ImageMonitor to test other alternatives first
      maxAge: 30m
registries:
  default:
    check:
      method: HEAD           # HEAD (default) | GET
      interval: 10m
      maxPerInterval: 20
      timeout: 10s
    copy:
      interval: 1h
      maxPerInterval: 20
      timeout: 10s

  private-registry.tld:
    copy:
      interval: 10m
    # Auth used by the ImageMonitor (and by ImageAlternative if not provided)
    # to check availability when kuik has no access to the image pull secret
    # (i.e. kuik configured without cluster-wide secret access, for security)
    perPrefixFallbackAuth:
    - prefix: /project1                 # one secret per project, several entries
      secretRef:                        # per registry without conflict
        name: project1-creds
    - prefix: /project2
      secretRef:
        name: project2-creds

  123456.dkr.ecr.eu-west-3.amazonaws.com:
    perPrefixFallbackAuth:
    - prefix: /acme
      provider:
        name: aws
        serviceAccountRef:
          name: kuik-ecr-access

  docker.io:
    copy:
      interval: 1h
      maxPerInterval: 6
    perPrefixFallbackAuth:
    - prefix: /
      secretRef:
        name: dockerhub-creds

  public.ecr.aws:
    check:
      interval: 30m
```

Budgets are keyed by **registry** (hostname) — rate limiting is per registry — with `check`/`copy` overriding `default`; `perPrefixFallbackAuth` resolves at the longest matching prefix *within* that registry.

**Checker auth resolution chain**, for a given ref: (1) explicit `auth` of the concerned entry (an alternative's config, a mirror's destination); (2) the pod's `imagePullSecrets` **if kuik is allowed to read them** (see below); (3) the registry's `perPrefixFallbackAuth` (longest prefix); (4) anonymous — a persistent 401/403 produces the `Unauthorized` anomaly with the concerned prefix and the event suggesting the missing `perPrefixFallbackAuth` entry: credential discovery is no longer automatic, but it is *guided* and converges in one cycle. This path also covers what `imagePullSecrets` never covered: declared alternatives and mirror destination self-checks, which have no source pod.

**Reading `imagePullSecrets`: per-namespace RBAC opt-in.** The historical behavior ("kuik reads the imagePullSecrets of observed pods") was a cluster-wide `get secrets` — the exact privilege the design removes (§6.2), and RBAC cannot grant it in a restricted form (neither by type, nor "only secrets referenced by a pod"). Step (2) is therefore only active **where it is explicitly granted**: a per-namespace RoleBinding to the reconciler's ServiceAccount (Helm value `secretRead.namespaces: [...]`, or set by the namespace's admin; ClusterRoleBinding for whoever accepts returning to the historical behavior). The default remains the strong posture — "cannot read anything anywhere" — and the exemption is local, visible, and auditable (`kubectl get rolebindings -A | grep kuik`). Same philosophy as `injectPullSecret`: the choice is made explicit and bounded by default, not made on behalf of the operator. Note: the secret duplication toward `kuik-system` that `perPrefixFallbackAuth` implies is already accepted elsewhere (the same credentials live in N application namespaces) and is trivial to automate (external-secrets and the like push the same secret to both places).

This is also what gives meaning to the reconciler's shared rate limiter (§6.2): the budget is defined once, applied by a single process. The CRDs only keep what is a *per-resource policy*: the ImageAlternative its `rewritePolicy`, the ImageMirror its *policies* (`rewritePolicy`, `driftPolicy`, `platforms`), the monitor its *toggles* (`driftDetection`, `monitorAlternatives`). The webhook's check behavior (`availabilityCheck`: timeout, local cache, hints) lives here too — no identified interest in varying it per CR; if a legitimate case emerges, a per-CR override can be added additively.

---

## 2. ImageAlternative

### 2.1 Purpose and semantics

Declare sets of equivalent refs (the kind's name describes the *declaration*, not an active component) so the webhook rewrites, at pod admission, the image toward an **available** alternative. Invariants:

- **Availability is always checked before rewriting**, whatever the policy. We never route blindly.
- **If no alternative is available (original included), we touch nothing.**
- The pod's original image is simply *one of the alternatives* (the one whose prefix matches it): its place in the consideration order depends on `rewritePolicy`.

### 2.2 Spec

```yaml
apiVersion: kuik.enix.io/v1beta1
kind: ImageAlternative
metadata:
  name: thanos
spec:
  podSelector: {}            # standard LabelSelectors (§1.3) — absent
  namespaceSelector: {}      # or empty: all pods

  # OnFailure (default): original image untouched if available; otherwise,
  #   first available alternative in declaration order.
  # Always: always the first available in the list, even if the original is
  #   (quota, latency, network costs) — the original being considered at the
  #   position of the prefix that matches it.
  rewritePolicy: OnFailure   # OnFailure | Always

  # ORDERED list of prefixes (§1.1) — replaces the upstreams[] of
  # (Cluster)ReplicatedImageSet. The nominal case (public registries) fits
  # in a few strings, no structure.
  alternatives:
  - quay.io/acme/foo
  - docker.io/acme-org/foo
  - 123456.dkr.ecr.eu-west-3.amazonaws.com/repo/acme/foo
  - registry.local:5000/mirror/acme/foo

  config:                    # optional: per-prefix options. Key = an entry of
                             # alternatives, IDENTICALLY (CEL: keys ⊆ alternatives,
                             # strict equality — no prefix matching between key
                             # and entry, otherwise silent typos)
    123456.dkr.ecr.eu-west-3.amazonaws.com/repo/acme/foo:
      auth:
        provider:                  # provider-specific auth (AWS IRSA, …), same
          name: aws                # logic as Flux OCIRepository .provider
          serviceAccountRef:
            name: kuik-ecr-access
        injectPullSecret: false    # default false for provider: the kubelet can
                                   # already pull from the provider registry
    "registry.local:5000/mirror/acme/foo":     # keys with :, / or trailing /: quote them
      insecure: true               # non-HTTPS registry
      unavailable: true            # no longer available in this repo, but if a
                                   # pod uses it we still try to substitute an
                                   # alternative — participates in matching, never
                                   # proposed nor checked (no timeout wait)
      auth:
        secretRef:
          name: local-registry
        injectPullSecret: true     # default true for secretRef: inject the secret
                                   # in the pod namespace if this image is used as
                                   # an alternative, so the kubelet can pull it

  # NB: the webhook's check behavior (live check timeout, local/singleflight
  # cache, negative hints) belongs to the global config (§1.7). No events knob
  # either: the least-noisy level is chosen automatically based on rewritePolicy
  # (§5.1)
```

### 2.3 Design of `alternatives` + `config` — and why no more mirror reference

- **List of strings + override map** rather than a list of dicts: the vast majority case (public alternatives, zero options) stays a flat list readable at a glance, and the rich cases (auth, `unavailable`, `insecure`) keep all their expressiveness in `config`. The CEL validation `keys(config) ⊆ alternatives` (strict equality) makes any orphan key visible at admission. Map keys containing `:` or `/` are legal in YAML/JSON and in a CRD (`additionalProperties`), to be documented with quoting; SSA merges these keys individually — rather an advantage.
- **No more `copierRef`**: the reference to kuik-managed mirrors was removed. The real case was almost always a "router" with a single `copierRef` entry — a singleton that was a list only in form, where a normal ImageAlternative expresses several refs substitutable for one another. The mirror's **routing behavior now lives in `ImageMirror.rewritePolicy`** (§3): a single CR for the dominant "mirror everything + global fallback" case, no more cross-resolution machinery (reference, inheritance, inter-CR `Ready` condition). If a mirror must be positioned *between* upstream alternatives — an extremely rare case — it is expressed with two `ImageMirror`s or by declaring the mirror prefix as an ordinary alternative.
- **`prefix: ""` (match-all) remains rejected** for an ImageAlternative: an invisible sentinel, loss of the "non-empty prefix" validation, self-match of an already-rewritten ref. "All images" is the territory of `ImageMirror` (absent selectors), not of a universal alternative.

### 2.4 Candidate ordering: inter-CR and inter-kind merge

An image may be covered by several `ImageAlternative`s **and** by one or more `ImageMirror`s (whose routing scope is defined by their selectors). The documented total order:

> **`Always` ImageMirror** (lexicographic among them) → **`Always` ImageAlternative** (lex) → **original image** → **`OnFailure` ImageAlternative** (lex) → **`OnFailure` ImageMirror** (lex). Within an ImageAlternative, the declared order of `alternatives`; deduplication keeping the first occurrence (dedup by resulting ref, config included).

- Rationale of the order: an `Always` mirror exists precisely for latency/quota — it must beat a distant upstream alternative; in `OnFailure`, the upstream alternatives (fresh, canonical) come before the local copy, the mirror remaining the **ultimate safety net**.
- **In the common case, you don't have to worry about it**: ImageAlternatives disjoint among themselves, a single ImageMirror — the order is then the one naturally expected (original → declared alternatives → mirror as last resort, or mirror first in `Always`). Naming to sort only matters if several CRs *of the same kind and same policy* overlap.
- Lexicographic rather than `creationTimestamp` (Gateway API's choice for its conflicts): the timestamp is not stable under GitOps (delete/recreate silently changes precedence), the name is. `kubectl get imagealternatives` displays the order within the kind for free.
- **Only true conflict signaled**: two `Always` CRs (whatever the kind) wanting to place a *different* candidate ahead of the original for the same image → `Warning` event `AmbiguousRewrite` on the concerned CRs (not on the pods), the total order breaking the tie. A specific `OnFailure` + an `OnFailure` mirror is **not** a conflict, it's the intended composition.
- Same candidate ref produced by two CRs with different `auth` → dedicated `Warning` (almost always a config error).
- Attribution: a pod is counted only by the **winning** CR (the one that provided the retained ref) → disjoint gauges between CRs and between kinds, consistent sum.

### 2.5 Admission algorithm

For each candidate, in total order (§2.4):

1. Present in the **unavailable** index (informer on the ImageMonitor/ImageMirror statuses) with `lastCheck` < `skipHints.maxAge` (global config, §1.7) → **skipped** (de-prioritized, not eliminated).
2. Otherwise: local cache/singleflight, then live check bounded by `timeout` (idem).
3. First available → rewrite + attribution annotations + injection of the `imagePullSecrets` reference if the candidate's config carries an injectable `auth`; the secret-syncer materializes the secret **on demand** in reaction to the annotation (§6.2).
4. No non-skipped candidate available → **we come back and test the skipped ones** (a stale hint can never do worse than the absence of a monitor).
5. Still nothing → image untouched, `NoAlternativeAvailable` event.

Pinned refs (`tag@sha256:`): same steps, but candidates are derived **digest-preserved** and checks are done **by digest** — never by tag (§3.7). A digest absent from an alternative (multi-push with divergent digests) is simply skipped.

The monitor is an **optimization, never a dependency**: without a monitor, the hints index is empty and the current behavior (live check) applies — continuous degradation. Hints carry only the *negative*: with a short incident `maxAge` (<1 min), a stale "available" verdict is worthless, but "down for 8 min" remains an excellent prioritization hint. The webhook→monitor write-back is **excluded** (§6.1).

### 2.6 Status

This block exists identically on **ImageAlternative and ImageMirror** — both kinds route, both carry the observability of their routing:

The status is computed **only via informers** — nothing is written directly by the mutating webhook.

```yaml
status:
  pods:                       # GAUGES on LIVE pods, computed from informers
    tracked: 123              # pods this CR could apply to
    rewritten: 12             # pods effectively rewritten (OnFailure or Always)
    noAlternatives: 2         # pods left untouched: no alternative was available
  activeFallbacks:            # anomalies only (OnFailure rewrites) — empty in steady state
  - image: quay.io/thanos/thanos:v0.42.2
    routedTo: registry.tld/mirror/quay.io/thanos/thanos:v0.42.2
    pods: 12
    since: "2026-07-10T06:40:00Z"
  noAlternatives:             # anomalies: matched, original unavailable, NO
  - image: quay.io/thanos/thanos:v0.42.2-debug   # alternative available either
    pods: 2
    since: "2026-07-11T07:27:36Z"
  conditions:
  - type: Ready               # valid config, secrets readable (if provided)
    status: "True"            # → static, never stale
  - type: NoActiveFallback    # False = kuik avoided a pull error by rewriting to
    status: "False"           # an alternative (compensating an outage)
    message: "1 image routed to fallback (12 pods)"
  - type: NoAlternatives      # True = kuik could NOT find any available alternative;
    status: "True"            # the pod may start if the image is cached on the node,
    message: "1 image unavailable (2 pods)"   # otherwise it's a pull error
```

The status is written by the **reconciler** (aggregation of live-pod annotations), never by the webhook. **No "alternatives health" block**: removed because (a) confusion with the monitor's mission, (b) fed only at admission → stale information for rarely-redeployed images — a status that *looks* like monitoring without being it is worse than nothing. That need is covered by the monitor's `monitorAlternatives` (§4.4). Cumulative counters ("how many rewrites this week") go into `kuik_rewrites_total{kind, name, reason, entry}`.

---

## 3. ImageMirror

### 3.1 Purpose

Replicate the images of selected pods (via `podSelector`/`namespaceSelector`) toward **a single destination**, and **act as a routing destination** for that same scope according to `rewritePolicy` — copy + serve, hence the name. For several registries: several CRs (lifecycle and status isolated per destination). **Hard invariant: one destination subtree = one CR** — two ImageMirrors on `destination.path` values in a prefix relationship would have divergent desired states (one's cleanup would delete images the other considers used, their verifications would contradict each other); admission refuses it (or at minimum `Warning` + `Ready: False`). Assumed consequence: differentiated routing policies on the *same* mirror ("Always in prod, OnFailure in staging") are not expressible — a rare case, to be settled with two destinations.

### 3.2 Spec

```yaml
apiVersion: kuik.enix.io/v1beta1
kind: ImageMirror
metadata:
  name: prod-mirror
spec:
  # podSelector/namespaceSelector absent: all pods, all their images — SAFE by
  # default thanks to the structural self-exclusion of the destination (§1.6).
  # Same scope for COPY and ROUTING.
  podSelector: {}
  namespaceSelector: {}
  imageExclude: []             # image prefixes to explicitly exclude from
                               # mirroring (e.g. huge images)

  # Mirror ROUTING behavior (§2.4 for the total order):
  # OnFailure (default): last resort — used only if neither the original nor the
  #   ImageAlternative candidates are available.
  # Always: rewrites toward the mirror even if the original is available
  #   (quota, latency, network costs).
  # None: pure copy, never routed — archival/compliance, security scan,
  #   bootstrap, replication to a remote site; progressive migration path
  #   (copy first, enable routing later).
  rewritePolicy: OnFailure     # OnFailure (default) | Always | None

  destination:
    path: registry.tld/mirror/ # identical on all clusters — in multi-cluster,
                               # it's the TAG that carries the clusterID (§3.8)
    insecure: false            # default false — allow HTTP registry
    push:                      # CONTROLLER credential: push + delete unused tags
      secretRef:               # (+ read for verification) — confined to the
        name: mirror-write-credentials   # reconciler, never propagated outside kuik-system
    pull:                      # NODES credential: materialized on demand in the
      auth:                    # namespaces where the webhook routes to the mirror
        secretRef:             # (§6.2); absent = public registry or natively-
          name: mirror-read-credentials  # authorized kubelet
    # NB: the destination verification rate (and the copy budget) belong to the
    # global per-registry config (§1.7), not the CRD

  cleanup:
    enabled: true              # default true — delete tags no longer referenced by any pod
    retention: 24h             # hold duration before deleting a tag whose last
                               # reference disappeared (unusedSince), not from copy
                               # time → CronJob images survive between runs

  # Detect and reconcile tag drift (digest change), e.g. tag `latest`:
  #   Ignore (default): copy once, never update even if the upstream digest changes
  #   Warn: periodically re-check the digest and warn if it differs
  #   Sync: periodically re-check and resync the destination if it differs
  driftPolicy: Ignore          # Ignore (default) | Warn | Sync

  # Multi-architecture (§3.6):
  #   Auto: copy the arches derived from node labels
  #   All: copy all arches referenced by an image
  #   List: explicit list of arches to copy
  platforms:
    mode: Auto                 # Auto (default) | All | List
    #list: []                  # only with mode: List

  # NB: no "source" auth block in the CRD — pull credentials from the origins
  # follow the checker resolution chain (§1.7): pod imagePullSecrets (if RBAC
  # opt-in), then perPrefixFallbackAuth
```

### 3.3 Destination layout: `destination.path + full canonical ref (hostname included)`

`registry.tld/mirror/quay.io/thanos/thanos:v0.34.1`. The origin hostname is **kept**, for two non-negotiable reasons:

1. **Invertibility**: `strip(destination.path)` must give back exactly the canonical ref — this is what grounds idempotence, anti-loop, and re-copy without persisted state (the ImageMirror knows where to re-fetch a disappeared manifest, even after a restart, even with a lost annotation). Without the hostname, the function is no longer injective.
2. **Real collisions**: `docker.io/acme/app` and `ghcr.io/acme/app` can be two unrelated projects (namespaces assigned independently) — and by definition, no ImageAlternative declares equivalent images that are not. Same destination repo = fighting tags = silent corruption.

Near-zero storage cost: blobs deduplicated by digest within the repo, and the multi-cluster shared-repo scheme (§3.8) makes deduplication effective regardless of the backend (including those with per-repository-scoped storage, ECR notably). Details: port normalization `:` → `_` (invertible, `_` impossible in a hostname), full normalized form for docker.io (`mirror/docker.io/library/nginx`), documented prerequisite of a registry accepting deep paths (Harbor, distribution, Zot, GAR, ECR, GitLab — Docker Hub excluded anyway); on the discovery side, only `tags/list` per repository is required (never `_catalog` nor system rights — §3.4). No `layout: PathOnly` option in v1: an option that can never be removed, invisible failure mode.

### 3.4 Verification / re-copy (the destination is the source of truth)

- **Desired state** = canonical refs `inUse ∪ retained` (the same set that drives copy and cleanup) × desired platforms (§3.6). **Observed state** = **per-precise-ref** interrogation (`HEAD /v2/<repo>/manifests/<ref>`) — never a global listing: it only requires read rights on *that repo* (Harbor per-project scope is enough). Divergence → re-copy from the origin **or any available member of the equivalence class** (re-copy works even if the strict origin has disappeared), within the *copy* budget of the source registry.
- Re-copy is **prioritized** over initial copies in the work queue: a manifest disappeared from the destination is an active availability hole (pods are probably routed to it), not a background task.
- An image in `pendingDeletion` stays verified and re-copyable until the `retention` deadline. **Only effective deletion by the ImageMirror itself** removes it from the desired state — never an observed absence (otherwise a destination-registry outage would purge the state).
- Deletion **by tag** (OCI 1.1 API) when the registry supports it, never blindly by digest (deleting a manifest by digest takes down all tags pointing to it — `v1`/`latest` pitfall).

**Cleanup without global enumeration: the repo inventory.** The OCI spec only guarantees `GET /v2/<name>/tags/list` per repository — so to know "what have I copied that is no longer desired?", the ImageMirror keeps its own **list of the repositories it has populated** (`status.repositories`: a `Set<string>` of repo paths, persisted in etcd — not rediscoverable, §1.5; same role as a Flux `Kustomization`'s `status.inventory`). The **repository** grain (not the ref) is what makes cleanup *exact*: a `tags/list` re-enumerates *all* tags of the repo, including those whose last reference disappeared during a down — the diff catches them, where an inventory of individual refs would miss them. Cleanup loop, for each repo of the inventory:

1. `tags/list` on that repo (read rights on the repo alone, never `_catalog`);
2. **filter on the `_<clusterID>` suffix** — we manage only *our* tags, those of other clusters sharing the repo (§3.8) are ignored;
3. diff against the desired state (canonical refs derived from the informers) → any of-our tags absent from the desired and past the `retention` window moves to `pendingDeletion`, then is deleted by tag;
4. a repo is removed from the inventory **only when it contains no more tags for this cluster** — without ever touching other clusters' tags.

Inventory writing follows the "before the action" doctrine: an entry is added **before** the first push into a new repo (a crash after push but before recording would create a repo outside the inventory, invisible to GC = leak). The inventory only moves on the appearance/disappearance of a *repo* (not a tag), so writes are rare. The leftover of *untagged* manifests after deleting the last tag belongs to the registry's GC (§3.8, or `deleteUntaggedManifests` by digest) — the inventory manages the tags this cluster owns, not the manifests. In multi-cluster, each cluster keeps the inventory in the status of *its* local ImageMirror: a local view, a repo possibly staying inventoried at A while B has emptied it.

**"To copy" is not a list, it's a residual.** The ImageMirror's scheduler is the **same lexicographic ring + cursor** as the monitor's (§6.3), placed on the **desired state** (the canonical refs of the scope, ∪ retention — a slowly-varying set, reconstructed from the informers) and not on "the images to copy" (a shrinking set). At each pass: HEAD at the destination → present (nothing to do, or re-PUT of the missing per-cluster tag, §3.8); absent → copy, within the copy budget. The to-copy list is never materialized: it is the absent-at-destination residual of the traversal, drained as it goes. A fresh mirror thus simply has a large residual on the first pass (visible as `desired − copied` in the status, §3.9), spread by the budget below; no dedicated queue, nothing more to persist.

**Persistent copy budget, restart-resistant.** The per-registry copy budget (§1.7) actually consumes quota on the source registry — a controller restart must not replay it from zero (pathological case: crash every 10 min → overrun). Since copies are **automatically spread** over the interval (one copy every `interval / maxPerInterval`), the budget reduces to a **rate**, persisted by a single timestamp per registry — a token-bucket whose last slot survives the restart. A single object, non-CRD (a hot-mutable counter does not have the properties of a CRD), a ConfigMap in kuik-system, **one `data` key per registry** (JSON) so that PATCHing one registry does not touch the others (a belt against leader overlap at failover):

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: kuik-registry-budget
  namespace: kuik-system
data:
  # key = registry (host), port ':' encoded as '_' (consistent with §3.3)
  docker.io: '{"lastCopy":"2026-07-10T08:14:03Z"}'
  quay.io:   '{"lastCopy":"2026-07-10T08:12:50Z"}'
```

Rule per registry: a copy is allowed if `now − lastCopy ≥ interval / maxPerInterval` → **persist `data.<registry>` first, copy after** (persist-before-acting: a crash between the two wastes a slot, never doubles it). On restart, the reconciler re-reads `lastCopy` and respects the delay before the next copy — the rate is thus preserved regardless of the number of crashes. Written by the single leader-elected reconciler (§6.2), no contention between replicas. **Checks (HEAD)** persist no budget: a HEAD pulls nothing, and the cursor is enough to resume the traversal without a burst — a slight HEAD burst on restart is inconsequential.

### 3.5 `driftPolicy`: Ignore vs Warn vs Sync

Tag drift = the source tag's digest changes after copy (typical of mutable tags like `latest`). The three levels:

- **`Ignore` (default)**: copies once, never touches again — the mirror is a *snapshot of what runs*, insensitive to upstream mutations (accidental or malicious). Documented consequence: on failover, rescheduled pods run the old digest — the one that was running. It's a feature, chosen consciously.
- **`Warn`**: the verification loop periodically re-checks the source tag's digest and surfaces a divergence (`ImageTagDrifted` event + `DestinationInSync` reflecting it), without touching the copy. Detection without mutation — the operator keeps the human in the loop.
- **`Sync`**: same detection, plus a symmetric copy leg on the source side (HEAD of the origin tag, comparison to the copy's digest, re-push on divergence). `ImageResynced` event as `Normal` (steady-state regime of Sync), not to be confused with `ImageRecopied` (`Warning`: something deleted the copy). Assumed semantics "the tag is alive"; strict reproducibility = stay in `Ignore`.

Note that drift is *also seen* by the ImageMonitor (§4.3) independently of any mirror; `driftPolicy` is what the ImageMirror *does* about it. Extension noted but out of scope: rewriting to a digest-pinned ref (`pinDigest`) — tag→digest resolution in the critical path, pods that no longer follow their tag; monitor-that-alerts + `Ignore` cover the bulk of the risk while keeping the human in the loop.

### 3.6 Multi-architecture: `platforms`

- **`Auto` (default)**: the desired platforms are a **state derived from the cluster** — union of the Nodes' `kubernetes.io/os` + `kubernetes.io/arch` labels (near-free informer). Adding an arm64 node pool naturally triggers the **backfill** of missing arm64 manifests via the existing verification loop, exactly like a re-copy. Documented caveats: the backfill is not instantaneous (planned architecture addition → switch to `List` in advance); node labels do not carry the 32-bit ARM variant (`arch=arm` → default mapping `linux/arm/v7`, overridable via `List`).
- **`All`**: verbatim copy of the index and all its children — the **only digest-preserving mode**. To recommend explicitly when the cluster consumes refs by digest or the mirror must be an exact mirror (e.g. cosign signature verification, attached to the source digest — hence the combined interest with `includeAttestations` for the `unknown/unknown` entries of indexes: BuildKit attestations/provenance, referrers).
- **`List`**: explicit list `os/arch[/variant]`.

**The point not to discover at implementation time: filtering platforms changes the index digest** (impossible to push a "hollow" index — registries validate the existence of children; a filtered index must be rewritten, with a digest different from upstream). Three cascading consequences:

1. Verification and `driftPolicy: Sync` no longer naively compare "copy digest vs upstream digest": the *source* index digest is recorded in an **OCI annotation on the pushed index** (`kuik.enix.io/source-digest`) — faithful to the "destination is the source of truth" principle, zero extra etcd state.
2. Pinned-ref side: an ImageMirror **never proposes itself as a candidate for a digest-pinned ref** (`@sha256:`) if `platforms.mode ≠ All` — the requested digest does not exist in a filtered mirror. Consistent with the exclusion of `@sha256:` from drift.
3. The status gains a `platformsMissing` aggregate (images copied but incomplete vs desired platforms) — it's what says "the arm node pool arrived, the mirror will be ready in N images".

The monitor is not impacted: its drift detection checks the membership of the executed `imageID` in the *upstream* index, correct regardless of the mirror's platform policy — each compares what it is responsible for.

### 3.7 Digest-pinned refs (`@sha256:`)

Classic case: charts that pin (`…/controller:v1.15.1@sha256:594cee…`). A digest being content-addressed and verified by the runtime at pull time, pinned refs are the *safest* case in the system — provided three rules:

1. **Digest-preserving copy per image, whatever `platforms.mode`**: an image one of whose in-use references is pinned is copied **verbatim** (full index, digest preserved) — platform filtering applies only to tag-referenced images. Overhead bounded to the pinned scope (typically a few charts). This is what lets the mirror be a **routing candidate** for these refs: the derivation preserves the `@sha256:` suffix (and the cosmetic tag — ignored by the runtime, useful to the human), and the availability check is done **by digest** (`HEAD /v2/<repo>/manifests/sha256:<D>`) — an *exact* check, equivalence being verified by the protocol itself. Same rule on the `ImageAlternative` side: candidates derived digest-included, check by digest, never by tag (the alternative's tag may point elsewhere). Documented caveat: multi-push upstreams often produce **different** digests per registry (rebuild, re-signature) — the check then fails and the candidate is skipped: correct behavior, the user pinned bits, we honor bits; no erroneous substitution possible, at worst no alternative.
2. **Anchor tag against the registry's GC**: a manifest without a tag is the prime target of GCs (Harbor, retentions, `registry garbage-collect`). Each pinned copy is anchored by a derived tag — cosign convention, `sha256-<digest>` — in addition to the possible cosmetic tag. Without an anchor, it wouldn't be kuik deleting the safety net, it's the external GC, silently.
3. **Lifecycle keyed by digest, decoupled from the tag.** The dangerous scenario: `driftPolicy: Sync` resynchronizes `v1.15.1` to D′ → the old D becomes an orphan → cleanup or GC deletes it → the pod pinning D loses its fallback. Rule: a pinned reference is a **full-fledged desired-state entry, keyed by digest**, independent of what the tag points to — `Sync` advances the *tag*, it never orphans a digest still in use or in retention; the anchor and the manifest are only deleted at the retention deadline **of the digest itself**. Verification HEADs the digest, without tag ambiguity.

The rest follows without adaptation: canonicalization carries the digest unchanged (`destination.path` strip still invertible), drift detection keeps excluding `@sha256:` (immutable by definition), the monitor checks pinned ones by digest like the rest.

### 3.8 Multi-cluster: shared path, one tag per cluster

Several clusters, one shared registry, **without an explicit sync mechanism**: all clusters write to the **same repo** (`mirror/quay.io/thanos/thanos`), but each cluster owns only *its* tags, suffixed by the `clusterID` from its operator config: `v0.34.1_cluster-a`, `v0.34.1_cluster-b` — generally pointing to the same manifest. The per-cluster tag is not a sidecar of a canonical reference: **it is the routing reference itself** (cluster B's webhook rewrites to `…:v0.34.1_cluster-b`) — there is no shared canonical tag, so no one has to maintain it.

- **Copy**: HEAD the manifest **by digest** in the shared repo; if it exists (pushed by another cluster) → simple PUT of the tag, a few KB, **zero blob transferred**; otherwise normal copy. Network sharing is native: the first cluster pays the transfer, the others a manifest — no more need for cross-repo blob mount (blobs are linked per repository: shared repo = standard existence HEAD sufficient), hence no `blobMountFrom` field nor topology knowledge.
- **Lifecycle**: each cluster creates, verifies (its loop HEADs *its* tags, re-PUTs if missing) and deletes **its tags only** — zero shared mutable state, zero race, no blocking dead cluster (its tags immortalize its manifests without preventing others from managing theirs: a retention choice, not a deadlock).
- **Space reclamation delegated to the registry's GC**: as long as a cluster tag exists, the manifest is uncollectable; when the last one disappears, it becomes untagged and the native GC (distribution, Harbor "delete untagged") reclaims the space. For registries without untagged GC: `deleteUntaggedManifests` option — the cluster that deletes its last tag lists the remaining tags and, if there are none, deletes the manifest by digest after a grace delay; the only place a read-check-act reappears, residual race window closed by the verification/re-copy loop. Default: delegation to the GC.
- **Multi-arch and `driftPolicy: Sync` naturally correct**: with `platforms: Auto`, two clusters with different node pools produce **different** filtered indexes — a shared canonical tag would be structurally broken (A's index lacks B's arm64, unless imposing `All` everywhere); with a per-cluster tag, each filtered index is its cluster's artifact, common blobs deduplicating anyway. Same for `Sync`: each cluster resynchronizes its tag toward the digest it observes, without a shared tag flapping. Pinned refs: same mold, `sha256-<D>_cluster-b` anchor (routing is done by digest, only the anti-GC anchor is per-cluster, §3.7).
- **Zero templating**: the ImageMirror CR is identical on all clusters — only the `clusterID` lives in the operator config. Trivially, mono-cluster is the same code with a single suffix.

Naming details to carve in: the **128-character limit** of OCI tags (short clusterID imposed; deterministic truncation + hash for endless CI tags) and a separator chosen to make collision improbable with an upstream tag literally containing the pattern (copy-time detection as a belt). Assumed limitation: rewritten refs differ between clusters *by the tag* — inconsequential, their only consumer being the local kubelet.

Alternatives evaluated and discarded: **one `destination.path` per cluster** (equivalent autonomy but zero network sharing without an opportunistic mechanism like blob mount — which requires naming a source repo and topology knowledge —, storage deduplicated only on content-addressed backends, and per-cluster prefix templating); **shared canonical tag + claims** (cosign pattern: materialized refcount, heartbeat for dead clusters, canonical-tag restoration brick — the project's first coordination machinery, and structurally incompatible with heterogeneous `platforms` between clusters); **registry GC on "last pull"** (fragile usage proxy — kubelet cache — and tied to a specific registry). In all cases, the verification/re-copy loop remains the net that turns residual races into transient incidents.

### 3.9 Status

With `rewritePolicy ≠ None`, ImageMirror also carries the **same routing block as ImageAlternative** (`pods` + `activeFallbacks` + `noAlternatives` + the routing conditions, §2.6), in addition to the following:

```yaml
status:
  images:
    desired: 312               # images in running pods + retained
    copied: 309                # images effectively copied to the destination
    retained: 1                # images pending deletion (if cleanup.retention > 0)
    drifted: 0                 # driftPolicy Warn/Sync: tags with a new upstream digest
    platformsMissing: 8        # copied but missing a platform (multi-arch)
    missingSource: 1           # no source available to copy an uncopied image
  failedImagesCopy:            # anomalies only
  - ref: quay.io/acme/tool:1.4
    reason: SourceUnavailable  # SourceUnavailable | QuotaExceeded | AuthFailed | PushFailed
    lastAttempt: "2026-07-10T06:12:00Z"
  pendingDeletion:             # NON-reconstructible: images no longer referenced,
  - ref: ghcr.io/acme/report-job:v42          # held for cleanup.retention (if enabled)
    unusedSince: "2026-07-10T02:00:00Z"
  selfCheck:                   # destination verification health (§6.3)
    cursor: registry.tld/mirror/quay.io/thanos/thanos   # last checked repo → resume here on restart
    cycleStarted: "2026-07-10T06:00:00Z"      # datetime of the last ring iteration start
    cycleDuration: 55m         # time to check all images of this registry with the
                               # current config = each image's check frequency;
                               # a too-high value signals the config needs tuning
  repositories:                # NON-reconstructible (§1.5): repos populated by this
  - registry.tld/mirror/ghcr.io/acme/report-job     # CR — drives GC (like a Flux
  - registry.tld/mirror/quay.io/acme/tool           # Kustomization status.inventory).
  - registry.tld/mirror/quay.io/prometheus/prometheus  # Written before the first push,
  - registry.tld/mirror/quay.io/thanos/thanos       # removed when no tag of this CR
                               # remains. Each tag is retrievable via
                               # GET /v2/<repo>/tags/list (OCI Distribution), so no
                               # need to store them individually.
  conditions:
  - type: DestinationInSync    # destination registry in the desired state
    status: "False"
    reason: MissingImages
  - type: Ready                # valid config, working credentials
    status: "True"
```

---

## 4. ImageMonitor

### 4.1 Purpose

Continuously monitor the availability of the images of pods selected by `podSelector`/`namespaceSelector`, among those **currently used** in the cluster (operational risk at node drain/incident), their **tag drift**, and — as an opt-in — the **alternatives** the webhook could serve. Automatic discovery from pods; the selectors restrict (the default usage, without a selector, targets everything).

### 4.2 Spec

```yaml
apiVersion: kuik.enix.io/v1beta1
kind: ImageMonitor
metadata:
  name: cluster-images
spec:
  # podSelector/namespaceSelector absent: the whole cluster; exclusion via
  # namespaceSelector for pathological cases (e.g. CI namespaces with unique tags)

  unusedImageRetention: 24h    # keep monitoring for a while after the image is
                               # no longer used in the cluster — useful for CronJobs

  driftDetection:
    enabled: true              # DEFAULT: if the pod restarts, will it run the
                               # same thing? (executed imageID vs upstream tag)

  monitorAlternatives: false   # opt-in: also monitor the alternative images
                               # (derived from the ImageAlternatives, §4.4)
                               # instead of only the original ones — ImageMirror
                               # destinations remain excluded (self-verified by
                               # their loop, §3.4)

  # NB: check method, cadences, per-registry budgets and the checker's
  # perPrefixFallbackAuth belong to the operator's global config (§1.7) — shared
  # across all CRs, one budget per registry
```

### 4.3 Drift detection (by default)

- Baseline = `pod.status.containerStatuses[].imageID` (the **executed** digest), reconstructible from the informers — nothing new to persist for in-use images. Comparison to the `Docker-Content-Digest` returned by the availability HEAD → **free check in number of requests**.
- Multi-arch: the HEAD returns the *index* digest, `imageID` the platform-manifest one → GET of the index to verify membership, redone only when the index digest changes (index immutable by digest).
- Exclusions: `@sha256:` refs (immutable by definition). Images in **retention** capture their `digest` on the way (the only extension of the persisted state).
- Bonus: detection of **intra-cluster skew** (same tag, different `imageID` between pods — stale kubelet cache), distinct reason `ClusterSkew` (different remediation: rollout restart vs upstream decision).

### 4.4 `monitorAlternatives`: plugging the hole in the safety net

Without it, **no one monitors the candidate refs**: an alternative appears in no pod until a rewrite has occurred — we would discover *during* the Quay outage that the mirror has been returning 401 for six weeks. The monitor therefore extends its source of refs: for each in-use image matched by an ImageAlternative, the candidates that one would derive. The whole mechanism (rate-limited checker, transitions, bounded status, per-registry auth) is reused as-is.

Scope bounds:

- Limited to images **in use or in retention** (not "everything the prefixes could match", a potentially infinite set) — volume ≤ in-use images × alternatives per image, smoothed by rate limiting.
- Includes images *matched but never rewritten* — precisely the case where admission will never provide fresh info.
- **Excludes ImageMirror destinations**: the mirror already verifies its destination continuously (§3.4) — re-checking it would be a duplicate at a different rate with potentially contradictory verdicts, and that monitoring must work *without any monitor*. An ImageAlternative prefix pointing "by chance" at an ImageMirror destination stays monitored (unguessable coincidence). No shared mirror/monitor index in v1.
- Excludes prefixes marked `unavailable: true` in `config` (declared dead: checking them would contradict the user).
- The "persistent 401 = auth probably missing" event lives here: emitted weeks before the incident, not during it.

### 4.5 Status

We store only aggregates, anomalies, and information that cannot be recomputed from the informers. This keeps the status human-readable and shows only usable information (e.g. which image is unavailable) — no need to persist a large volume that can be rebuilt on pod restart.

```yaml
status:
  images:                       # images seen on pods — what RUNS, immediate risk
    tracked: 3241               # images tracked by this CR
    inUse: 3180                 # images associated with a running pod
    retained: 61                # no longer running but still monitored (unusedImageRetention)
    available: 3226
    unavailable: 4
    unknown: 11
    drifted: 2                  # digest differs from upstream (driftDetection.enabled=true only)
  alternatives:                 # alternatives matching a running pod — what COULD
    tracked: 214                # serve (monitorAlternatives=true only)
    unavailable: 2
  retainedImages:               # NON-reconstructible from informers (ref+date+digest)
  - ref: ghcr.io/acme/report-job:v42
    unusedSince: "2026-07-10T02:00:00Z"
    digest: sha256:aaaa…
  unavailableImages:            # negative check results with reason
  - ref: docker.io/foo/bar:1.2
    reason: ManifestNotFound
    since: "2026-07-08T14:00:00Z"
    referencedBy: 3
  unavailableAlternatives:
  - ref: ghcr.io/thanos-io/thanos:v0.42.2
    derivedFrom: quay.io/thanos/thanos:v0.42.2
    via: "ImageAlternative/thanos[2]"
    reason: Unauthorized
  driftedImages:                # running digest differs from upstream (e.g. `latest`)
  - ref: docker.io/acme/app:prod
    runningDigest: sha256:aaaa…
    upstreamDigest: sha256:bbbb…
    referencedBy: 7
  checks:                       # health tied to the global check config (§6.3);
    registries:                 # per-image verdicts are NOT persisted: the
    - registry: docker.io       # guarantee is "checked at most every cycleDuration"
      cursor: docker.io/library/nginx      # last checked image → resume here on restart
      cycleStarted: "2026-07-10T04:00:00Z" # last ring iteration start
      cycleDuration: 4h10m      # time to check every image of this registry =
                                # each image's check frequency; too high → tune config
    - registry: quay.io
      cursor: quay.io/thanos/thanos
      cycleStarted: "2026-07-10T03:20:00Z"
      cycleDuration: 1h45m
  conditions:
  - {type: AllImagesAvailable, status: "False"}        # a tracked image is unavailable
  - {type: AllAlternativesAvailable, status: "False"}  # an alternative of a tracked image is unavailable
  - {type: NoImageDrift, status: "False"}              # digest drift detected
```

The per-*available*-image detail lives in Prometheus metrics (`kuik_image_available{image=…}`: full granularity, free history, zero etcd pressure) and in Events on transitions. `retainedImages` sizing: churn × retention (exclusion via `namespaceSelector` of CI namespaces with unique tags).

---

## 5. Observability: events and metrics

### 5.1 Events

Three design rules:

1. **Emit on the object the user will inspect**: what affects a pod → event on the Pod (`kubectl describe pod` tells the story; events on cluster-scoped CRs land in `default`, invisible to teams' RBAC); a resource's lifecycle → event on the CR.
2. **Transitions, never states** — and emit the reverse transition (a recovery `ImageAvailable` closes the loop).
3. **Stable, enumerable `Reason`s** (the variable data goes in the message), strict discipline `Warning` = someone should look / `Normal` = traceability. Vocabulary unified with the annotations: `Force`/`Fallback`.

| Reason | Object | Type | Trigger |
|---|---|---|---|
| `ImageFallback` | Pod | Normal | Rewrite because the origin is unavailable — the operationally interesting signal (message: origin ref → target, `via <kind>/<name>[i]` — indispensable to debug inter-CR precedence) |
| `NoAlternativeAvailable` | Pod | Warning | Origin unavailable and no alternative available — image untouched |
| `PullSecretInjectionFailed` | Pod | Warning | Secret materialization failure |
| `AmbiguousRewrite` | concerned CRs | Warning | Two `Force` CRs (ImageAlternative or ImageMirror) in conflict ahead of the original (once, not per pod) |
| `ImageCopied` | ImageMirror CR | Normal | Initial copy |
| `ImageRecopied` | ImageMirror CR | **Warning** | Manifest disappeared from the destination → re-copy. The most valuable event: reveals an external GC/purge while pods are routed to it |
| `ImageResynced` | ImageMirror CR | Normal | Resync in `driftPolicy: Sync` mode (steady-state regime — not to be confused with the previous one) |
| `ImageCopyFailed` / `ImageDeleted` / `ImageDeletionFailed` | ImageMirror CR | Warning / Normal / Warning | |
| `ImageUnavailable` / `ImageAvailable` | ImageMonitor CR | Warning / Normal | Transitions (recovery only Unavailable→Available) |
| `ImageTagDrifted` | ImageMonitor CR | Warning | Upstream digest ≠ executed digest (message old→new; reason `ClusterSkew` for intra-cluster skew) |
| `InvalidCredentials` / `TokenRefreshFailed` | concerned CR | Warning | Secret not found/rejected; renewal failure **before** the old token expires (the window where one can still act) |

No events knob in the CRDs: the least-noisy level is chosen **automatically based on `rewritePolicy`**. Fallback rewrites (origin unavailable, i.e. `reason: OnFailure`) always emit `ImageFallback`; `Always` rewrites — the steady-state regime, one event per pod on each rollout — emit **no** event, their traceability passing through the pod annotations (§5.3, `reason: Always`) and the `kuik_rewrites_total{reason=always}` counter. (Under `rewritePolicy: OnFailure`, every rewrite is by definition a fallback: the question doesn't arise.)

Reminders: event TTL ~1 h in etcd — this is **not** an audit log; any destructive action is also logged + counted as a metric (event-exporter for history). A mirror's initial sync = a burst of `ImageCopied` `Normal`, acceptable (alternative: `SyncStarted`/`SyncCompleted` with counters).

### 5.2 Metrics: the "aggregates + anomalies" rule applies to the TSDB too

One series per tracked image (`kuik_image_available{image=…}`) is the same problem as the exhaustive list in the status, moved into Prometheus: cardinality = images × states, and above all **churn** — each label value ever seen creates a series kept in head memory (~1-8 KiB) and in the blocks for the whole retention. The CI cluster with unique tags (already the pathological case of `retainedImages`) produces tens of thousands of dead series/month there. This is the documented anti-pattern of unbounded labels — kube-state-metrics is the cautionary example: its metrics carrying image refs in labels (`kube_pod_container_info`) are a known cardinality cost that many operators filter at ingestion. Better not reproduce the same problem in kuik. Retained structure:

```
# Aggregates — constant cardinality, mirror of the status
kuik_monitor_images{monitor, state}              # tracked|available|unavailable|unknown|drifted|retained
kuik_monitor_alternatives{monitor, state}
kuik_mirror_images{mirror, state}                # desired|copied|retained|drifted|platformsMissing|missingSource
kuik_rewrite_pods{kind, name, reason}            # rewritten|always|fallback|noAlternatives

# Intermediate PER-REGISTRY granularity — often the one sought during an
# incident ("docker.io has a problem"), at a tiny cardinality
kuik_monitor_images_by_registry{monitor, registry, state}
kuik_registry_checks_total{registry, result}     # counter: ok|unauthorized|timeout|…
kuik_check_cycle_duration_seconds{monitor, registry}  # gauge: ring traversal duration (§6.3)
                                                 # the "at what rate we spin" history
                                                 # lives in the TSDB, not etcd

# Anomalies only — bounded like the status lists, info-metric style
kuik_image_unavailable{monitor, image, registry, reason} = 1
kuik_image_drifted{monitor, image} = 1
kuik_mirror_image_failed{mirror, image, reason} = 1
```

- Anomaly series carry *presence* (gauge at 1): alerting on existence (`kuik_image_unavailable == 1`), resolution by disappearance of the series — which the exporter **explicitly removes from the registry on transition** (a clean staleness marker) rather than ceasing to feed it.
- Assumed limits: no per-*image* availability history ("nginx was unavailable 0.3% of the month") — only aggregates and per-registry have a continuous history; anomaly dashboards as tables (info-metrics), not lines.
- Per-image emission is **off by default** (anomalies only). Should full per-image series ever be wanted for a few critical images, the safe pattern is a **secondary monitor with narrow selectors** whose bounded scope caps cardinality — not a cluster-wide toggle. The statuses being bounded, kube-state-metrics can also expose the aggregates via `CustomResourceState`. (No per-CR metrics knob in the CRD spec: the emission policy is an operator/exporter concern, kept out of the resource.)
- Cumulative counters (`kuik_rewrites_total{kind, name, reason, entry}`) escape the question: **structural** labels (CR names, enumerated reasons), cardinality bounded by the config. Dividing line to document: **config labels = unbounded in time; content labels = anomalies only, unless explicit and scoped opt-in**.


### 5.3 Annotations

Set on the pod by the mutating webhook — the same `original-images` mechanism as kuik v2, extended with attribution and reason. Each is a JSON object keyed by **container name**, so a multi-container pod rewritten by several CRs is fully described:

```yaml
metadata:
  annotations:
    # Original image refs, to restore them / for canonicalization (§1.6)
    kuik.enix.io/original-images: |
      {"config-reloader":"quay.io/prometheus-operator/prometheus-config-reloader:v0.91.0","prometheus":"quay.io/prometheus/prometheus:v3.13.1-distroless","thanos-sidecar":"quay.io/thanos/thanos:v0.42.2"}
    # CR that rewrote each container's image (kind/name — both kinds route)
    kuik.enix.io/rewritten-by: |
      {"config-reloader":"ImageMirror/prod-mirror","prometheus":"ImageAlternative/prometheus","thanos-sidecar":"ImageAlternative/thanos"}
    # Reason for kuik's action (rewrite) or inaction (original kept):
    #   OnFailure:      original was unavailable → rewritten to the first available alternative
    #   Always:         rewritten to the first available alternative as requested by rewritePolicy
    #   NoAlternatives: no available alternative found → image left untouched
    kuik.enix.io/reason: |
      {"config-reloader":"NoAlternatives","prometheus":"Always","thanos-sidecar":"OnFailure"}
```

These annotations are the sole channel by which the reconciler reconstructs the routing status (§2.6) and the secret-syncer knows which entries a namespace actually uses (§6.2) — no webhook write to any status, consistent with the unidirectional flow (§6.1).

---

## 6. Controller architecture

### 6.1 Unidirectional data flow

Monitors and mirrors **publish** (status via the API); the webhook **consumes** (informers → in-memory index of negative verdicts). The webhook→monitor write-back is excluded: it would invert the direction of dependency (the webhook would become the writer of the state that drives… the webhooks), impose the ref→monitor(s) mapping (multiple) and write conflicts with the checker — to refresh refs that were *precisely* just verified. If needed in the future: a fire-and-forget priority re-check *hint* into the monitor's queue, without state writing. Assumed and documented asymmetry: a live verdict may temporarily contradict the monitor's status — two clocks for two uses (routing: `skipHints.maxAge`; alerting: the global per-registry check interval, §1.7).

### 6.2 Split: by **privilege × availability profile**, not by CRD

A single binary, three Deployments (`--mode=…` — the "one binary, several deployment modes" pattern à la Loki/Thanos: one release, zero version skew, reversible topology with a combined mode for small clusters).

| Component | Role | RBAC | Availability |
|---|---|---|---|
| **kuik-webhook** | Pod admission, active checks | **Full read-only**: CRDs + status, secrets of `kuik-system` only (namespaced Role). *References* imagePullSecrets in the mutated pod, never creates them. No pod informer (the AdmissionReview provides them) — light informers on the CR statuses. Writes **no** status. | Multiple active replicas without leader election, PDB, high priorityClass, flat resources. `failurePolicy: Ignore`: kuik down = no more rewriting, never = no more scheduling |
| **kuik-secret-syncer** | **Sole holder** of cluster-wide secret writing: materializes pull secrets **on demand** — triggered by pods' rewrite annotations (informer), it creates a secret in a namespace only when a rewrite has made it necessary there, never by pre-computing CR × matched namespaces (do not scatter private-registry credentials into namespaces that have no use for them) — and renews cloud tokens (`injectPullSecret` + provider, refresh before expiry — ECR 12 h → ~10 h) | `create/patch/delete secrets` cluster-wide, **zero read verb outside kuik-system** (see below), constrained by **ValidatingAdmissionPolicy**: `dockerconfigjson` type only, `managed-by: kuik` label, reserved name prefix **both ways** (the syncer only writes under this prefix; no one else can write under it) | Leader-elected |
| **kuik-reconciler** | Mirror **and** monitor (identical profiles: rate-limited loops, pod/node read, kuik-system secrets, writing their statuses) + the ImageAlternative/ImageMirror `status.pods` gauges (annotation aggregation). Colocation = **shared per-registry rate limiter** (check/copy budgets defined in the global config, §1.7 — a single budget toward docker.io) | Read pods/nodes/CRDs, write kuik statuses, kuik-system secrets, `configmaps: [get, create, update]` on the single `kuik-registry-budget` resource (§3.4, restricted `resourceNames`) — extensible by **opt-in**: per-namespace RoleBinding to read observed pods' `imagePullSecrets` (§1.7), never granted by default | Leader-elected, one active replica |

**The syncer without cluster-wide reads** — several combined techniques to reconcile the copies it wrote itself without ever reading them:

1. **Blind server-side apply**: the `patch` verb does not require `get` — we apply the recomputed desired state (from the CRs + kuik-system secrets), without reading the existing one. A pre-existing secret at the reserved name is taken into management by the field manager (force ownership): the wanted behavior for a name that contractually belongs to the syncer.
2. **Deterministic names derived from *identity*, never from config**: `kuik-<CR-name>` — a single **merged** secret per (routing CR, namespace) — ImageAlternative or ImageMirror — its `.dockerconfigjson` containing **one `auths` entry per registry of the injectable alternatives** (`auth` present + `injectPullSecret` effective) **actually used in the namespace** (point 3) — never the union of all config: a CR with five authenticated alternatives of which only one served in the namespace materializes a single entry there. The kubelet matches each pull to the right entry by the ref's registry. Why this granularity and not per-registry nor global: per-registry would multiply objects and references to inject (a pod may have containers rewritten toward different entries); global would break the ownerReference (a single owner per object) and name = identity — deleting a CR must GC *its* credentials and only its own; (CR, namespace) is the only split where lifecycle, ownership and content coincide (a pod matched by two routing CRs receives two references — no problem, `imagePullSecrets` is a list and the kubelet aggregates). Bonus of the merged secret: a single reference to inject per CR, atomic update. Implementation note: `provider` + `injectPullSecret: true` entries (renewed tokens) coexist with static `secretRef`s in the same object — the write rate of a namespace's secret is thus dictated by its most volatile credential (~10 h for ECR), negligible in volume but visible in the audit log. Crucial naming point: if the name derived from config (source secret name, `alternatives` entry…), modifying the CR would **orphan** the old copy, unfindable without `list`. Name = identity → a config change changes the *content* (in-place SSA patch), never the name: the mutation orphan is structurally impossible.
3. **On-demand lifecycle, content scoped to real need**: a namespace's secret is born at the **first rewrite** that requires it (the webhook injects the reference at admission; the syncer, notified by the pod's annotation via its informer, materializes it right after), and its content = the credentials of the sole **entries actually used by the namespace's live rewritten pods** — recomputed from the informers ("free loss" tier of §1.5), applied via SSA. No pre-scattering: a namespace no pod of which has ever been rewritten never sees a secret. The object itself is **never deleted by a return to the original image** — it lives until its owner's GC: this is what makes the startup race a *once-per-namespace* event (see below), and its content can empty without the object disappearing (an empty `dockerconfigjson` is valid).
4. **ownerReference to the cluster-scoped CR** (legal for a namespaced object) → deleting an ImageAlternative/ImageMirror = free GC of all its copies.
5. **Two-tier write deduplication**: in memory, a **hash of the desired state** per (CR, namespace) — the desired state being a pure function of the informers (live rewritten pods → used entries, CR specs, kuik-system source secrets) — makes a redeployment of 100 pods on the same rewritten image cost **zero** API calls after the first; and below, an **identical-content SSA is a server no-op** (no resourceVersion, no watch event): correction never depends on the cache, whose loss costs at worst one no-op apply per (CR, namespace) — "free loss" tier of §1.5. A short debounce per (CR, namespace) coalesces bursts of pod events (a rollout generally does not change the set of used entries → no write).
6. **Resync on update, restart included**: the syncer *watches* the source secrets in kuik-system — update → invalidation of the concerned hashes → apply toward the namespaces of the map (same path for cloud-token renewal). On restart, no progressive refilling: the **initial List of the pod informer** rebuilds the full map (CR → namespaces → entries, via the rewrite annotations — a more precise signal than `imagePullSecrets` names, since they say which entry served) *before* the first event, and a **startup reconciliation pass** re-applies the desired state everywhere (almost entirely as no-ops): a source secret modified while the controller was unavailable is resynchronized at boot, without waiting for a new rewrite.

**The first-rewrite race, bounded and assumed**: at the very first rewrite in a namespace, the reference is injected at admission but the secret arrives a few seconds later (informer latency + SSA). The kubelet tolerates a missing pull secret (warning, attempt without credentials) and **retries in backoff, re-reading the secrets on each attempt**: the first pod of the namespace may spend a few seconds in retry, the following ones find the secret in place since it is never cleaned up. This is the price, bounded to one event per (namespace × CR) *for life*, of non-scattering — and it stays out of the admission path (no synchronous etcd write in the webhook). Declarative escape hatch for known-critical namespaces: pre-create the empty secret at the right name (Helm/GitOps) — the syncer's SSA takes it into management and fills it on the first rewrite, the injected reference then never pointing into the void.

**The honest limit of no-read**: "only inject if the pod does not *already* have valid auth for this registry" would require reading the *content* of the pod's `imagePullSecrets` (the names, visible in the AdmissionReview, do not say which registries they cover) — that's the only genuinely out-of-reach refinement. Three escape hatches make it non-blocking: (a) the default — injecting as soon as the winning entry carries an injectable `auth` — is **harmless when redundant**, the kubelet trying all credentials matching the registry until success; (b) `injectPullSecret: false` on the entry declaratively covers the "pods/nodes already have their auth" case; (c) where the §1.7 RBAC opt-in is granted, the syncer can refine the *content* of the secret by excluding registries already covered by the pod's own secrets. Nothing there warrants revisiting the posture: the injection decision remains correct by default, only sometimes redundant.

Key motivation for the split: without it, the most **exposed** component (the webhook, in the path of every admission) would also be the most **privileged** (writing secrets everywhere) — the opposite of least privilege. Final bill defensible eyes closed: by default, no one reads secrets outside kuik-system (only possible exception: the per-namespace RoleBinding opt-in of §1.7, explicit and auditable), a single identity writes secrets and it cannot read them, the critical-path component is read-only. Sobriety guardrail: each process watching the pods opens a cluster-wide watch → do not fragment beyond three.

### 6.3 Check scheduling: deterministic ring + persisted cursor

Since per-image `lastCheck`s are no longer persisted (neither in the status nor anywhere), scheduling fairness — "don't always test the same images" — rests on a reconciler structure, **shared by the monitor (availability/drift/alternatives) and the destination self-check of ImageMirrors**:

- **One ring per (CR, registry)**, sorted by **lexicographic ref order** (deterministic, readable, debuggable). At each tick of the registry's budget (§1.7), we pop `maxPerInterval` refs from the head, check them, and push them back to the tail. The position in the ring *is* the relative age of the last check: fairness by construction, zero per-image timestamp, O(1) per operation.
- **One persisted cursor per (CR, registry)** in the status: the *ref* of the last checked image (not an index — resumption is defined as "the strict successor of the cursor in the sorted order", well-defined even if the ref disappeared, if the set was reshuffled, or if the cursor does not yet exist; same robustness as the Kubernetes API's continue-token pagination). **Lazy** write, once per tick per registry — never per check. Restart cost: at worst `maxPerInterval` duplicate HEADs (checks done since the last write), instead of a whole cycle resumed in a potentially biased order.
- **New images enter the ring at their sorted position and wait for a full turn** before their first check — and this is the right behavior, not a compromise: an image that appears comes from a pod that was just scheduled, hence one the kubelet just **pulled successfully** — the freshest availability verdict there is, better than a HEAD (and if the pull fails, `ImagePullBackOff` says so without kuik). A caveat to document: the *derived* refs via `monitorAlternatives` for that new image have been pulled by no one and also wait a turn before their first verdict — acceptable for a proactive net with a days-scale horizon, but do not expect the monitor to give a verdict on the alternatives of an image deployed ten minutes ago.
- **Global budget, multiple rings**: rings of the same registry (several CRs) share the budget via the reconciler's rate limiter; an in-memory verdict cache avoids double-checking the same ref by two overlapping monitors (the second consumes the fresh verdict without spending budget — free-loss state, nothing to persist).

What the status exposes beyond anomalies (which keep their `since` — that's what the webhook's `skipHints` and alerting consume): the **scheduler's health**, the only per-image-aggregated info with operational value. The status surfaces, per registry, the `cursor` (resume point), the `cycleStarted` (last iteration start) and the `cycleDuration` — the effective time to check every image of the registry, i.e. each image's check frequency. That duration is `tracked / maxPerInterval × interval`; a value that drifts too high (growing cluster, tight docker.io budget) is the signal to tune the config, *before* the freshness of verdicts becomes fictitious — the cursor makes the cycle boundary exactly observable (return of the starting point). Assumed consequence: we can no longer answer "when was `nginx:1.27` last checked?" for a *healthy* image — only "all images of this registry are, at most every `cycleDuration`".

### 6.4 Recapped optimizations (and their why)

| Mechanism | Why |
|---|---|
| In-memory index via informer (never an API read at admission) | O(1) lookup regardless of storage; multi-replica convergence via the API server; works across separate binaries |
| Bounded status = aggregates + anomalies | Constant size under the etcd limit (~1.5 MiB); no write amplification nor watch-storm; **the published info (the unavailable ones) is exactly what the webhook consumes (the negative hints)** |
| Persist only the non-reconstructible (`unusedSince` + digest, repo inventory) | Everything else recomputes (informers), re-verifies (checks) or lists (registry, per repo); conservative recovery (lengthens retention, never shortens it) |
| Status writes on referencing transitions | Nearly nil in steady state, decoupled from the check rate |
| Singleflight + short-TTL cache, local to the webhook replica | Absorbs redeploy bursts (50 replicas = 1 check); no inter-process sharing needed (2 replicas = at worst 2 checks) |
| Negative hints bounded by `maxAge`, skipped candidates re-tested as a last resort | "down for 8 min" is an excellent prioritization hint even with a <1 min freshness need; a stale hint can never do worse than the absence of a monitor |
| Drift check attached to the availability HEAD; index GET only on digest change | Zero extra request in steady state |
| Re-copy prioritized over initial copy | A disappeared manifest is an active availability hole, not a background task |
| `platforms: Auto` = desired state derived from Nodes | Backfilling a new architecture is an ordinary reconciliation of the verification loop, not a dedicated mechanism |
| Source index digest in an OCI annotation on the pushed index | Correct `driftPolicy: Sync`/verification with filtered indexes, without extra etcd state — the destination stays the source of truth |
| Pinned refs: verbatim copy per image + `sha256-<D>` anchor tag + digest-keyed lifecycle | Digest preserved → routable and verified by exact check; protected from external GC and from a `driftPolicy: Sync` tag resync — the net stays as long as the digest is in use/retention |
| Copy ring placed on the **desired state** ("to copy" = absent-at-destination residual, never materialized) | No queue to persist nor to order despite a shrinking set; the fresh mirror is a large residual on the first turn, spread by the budget |
| Cleanup on `status.repositories` + `tags/list` filtered `_<clusterID>` | Exact GC without `_catalog` nor system rights (only `tags/list` per repo, OCI-guaranteed); repo grain → catches refs disappeared during a down; inventory written before push (no repo outside GC); multi-cluster: local view per CR |
| On-demand secret materialization (triggered by rewrite annotations), content scoped to the entries used by live pods, object kept until owner GC; blind SSA, name = identity (`kuik-<CR>`), two-way reserved prefix (VAP) | No credential scattering in namespaces without use; startup race bounded to one event per namespace (kubelet retry); no etcd write in the admission path; kubelet re-reads the secret at pull → a refresh benefits existing pods; reconciliation without **any** cluster-wide read verb; mutation orphans and collisions structurally impossible; free GC |
| Materialization proactive vs on-demand — see the syncer block (§6.2) | — |
| Deterministic check ring + lazily-persisted cursor-ref (1×/tick/registry) | Fairness without any per-image timestamp; restart = at worst `maxPerInterval` duplicate HEADs, resume at the sorted successor (robust to set mutations); new images = freshly pulled by the kubelet, waiting a turn is correct |
| Copy budget = 1 `lastCopy` timestamp per registry in a ConfigMap, persisted **before** the copy | Survives restart (the `interval/maxPerInterval` rate survives the crash — no quota replay on the source registry); persist-before-acting → a crash wastes a slot, never doubles it; 1 write/copy, negligible vs the pull+push |
| Metrics: aggregates + per-registry + anomalies-only (info-metrics removed on transition) | Bounded cardinality and churn in the TSDB — same logic as the status; continuous history where cardinality is structural |
| Cumulatives as metrics, journal as events, state as status | Each datum in the system that gives it the right properties (multi-replica monotonicity, history, TTL) |

---

## 7. Inspirations

| Design point | Inspiration |
|---|---|
| Prefix matching, trailing `/` | containerd / CRI mirrors |
| Pod selection `podSelector`/`namespaceSelector`, exact names via `kubernetes.io/metadata.name` | NetworkPolicy, admission webhooks — standard LabelSelectors, no custom structure |
| Discriminated union (`auth`) | `Pod.spec.volumes[]`: a single list whose each entry carries exactly one type among ~30 (`configMap`, `secret`, `hostPath`, `csi`, `emptyDir`, …) — radically heterogeneous implementations coexist under a single key because the common contract ("provide a mountable volume") is clear and admission validates the discriminant. Same scheme here: `secretRef` and `provider` under `auth` |
| CEL as a future additive extension (`matchConditions`) | webhooks' `matchConditions` / ValidatingAdmissionPolicy — added *alongside* the selectors, never in their place |
| `auth.provider` (aws/gcp/azure), per-object workload identity | Flux's `provider` field (`ImageRepository`/`OCIRepository`); go-containerregistry keychains (ECR/GAR/ACR) |
| `secretRef` without a namespace, resolved in a cluster-resource namespace | cert-manager's `ClusterIssuer` and its `--cluster-resource-namespace` (same anti-*confused-deputy* motivation) |
| Short-token pull-secret renewal | ecr-login-renew and the like, internalized |
| Bounded status, anomaly lists | Flux `Kustomization` (as opposed to kuik v1's CR-per-image / cert-manager Order/Challenge / CiliumEndpoint, whose scale pains — Kyverno reports, CiliumEndpointSlice — motivate the choice) |
| `status.inventory` of populated repos to drive GC without global enumeration | A Flux `Kustomization`'s `status.inventory` (the inventory of what the controller created, for lack of being able to reliably enumerate the universe) |
| Slice-based persistence pattern (future reserve) | EndpointSlice |
| Lexicographic inter-CR precedence (vs `creationTimestamp`) | Counter-example Gateway API: the timestamp is not GitOps-stable |
| Split by privilege profile (webhook read-only / syncer sole secret writer / reconciler) | cert-manager (controller / webhook / cainjector), Kyverno (admission / background / cleanup) |
| One binary, several deployment modes (`--mode`) | Loki (`-target=`), Thanos (subcommands) |
| Anomalies-only metrics, refusal of unbounded content labels | Prometheus cardinality best practices; counter-example kube-state-metrics (`kube_pod_container_info` and its image labels, often filtered at ingestion); `CustomResourceState` as a status-exposure route |
| `failurePolicy: Ignore`, read-only webhook | kuik v2 philosophy: "bulletproof reliability, minimal manipulation of primitives" |

---

## 8. Examples

### 8.1 Minimal ImageAlternative: public multi-upstream

```yaml
apiVersion: kuik.enix.io/v1beta1
kind: ImageAlternative
metadata:
  name: thanos
spec:
  alternatives:                # public registries: strings, nothing else
  - quay.io/thanos/thanos
  - ghcr.io/thanos-io/thanos
  - docker.io/thanosio/thanos
  # rewritePolicy: OnFailure by default — the original stays untouched as long
  # as it is available
```

### 8.2 Complete ImageAlternative: Always, auth, dead repo

```yaml
apiVersion: kuik.enix.io/v1beta1
kind: ImageAlternative
metadata:
  name: x509-exporter
spec:
  namespaceSelector:
    matchExpressions:
    - {key: kubernetes.io/metadata.name, operator: NotIn, values: [kube-system]}
  podSelector:
    matchExpressions:
    - {key: cnpg.io/podRole, operator: NotIn, values: [instance]}
  rewritePolicy: Always              # always the first available in the list,
                                     # even if the original is (quota/latency)
  alternatives:
  - quay.io/enix/x509-certificate-exporter
  - ghcr.io/enix/x509-certificate-exporter
  - docker.io/enix/x509-certificate-exporter
  config:
    ghcr.io/enix/x509-certificate-exporter:
      auth:
        secretRef: {name: ghcr-pull}
        injectPullSecret: true       # default for secretRef
    docker.io/enix/x509-certificate-exporter:
      unavailable: true              # deleted upstream repo: covers the manifests
                                     # that reference it, never routed
```

### 8.3 Minimal ImageMirror: mirror everything + global fallback (a single CR)

```yaml
apiVersion: kuik.enix.io/v1beta1
kind: ImageMirror
metadata:
  name: prod-mirror
spec:
  # no selector: all pods — safe: destination self-exclusion
  # rewritePolicy: OnFailure by default — the mirror is the ultimate net,
  # used only if neither the original nor the alternatives are available
  destination:
    path: registry.interne.example.com/mirror/   # identical on all clusters:
                                     # the clusterID lives in the tag (§3.8)
    push:
      secretRef: {name: mirror-rw}   # controller: push+delete+read
    pull:
      auth:
        secretRef: {name: mirror-pull}   # nodes: materialized on demand
  cleanup:
    enabled: true
    retention: 24h
```

### 8.4 Complete ImageMirror: ECR, IAM, Sync, multi-arch, Always

```yaml
apiVersion: kuik.enix.io/v1beta1
kind: ImageMirror
metadata:
  name: prod-mirror
spec:
  namespaceSelector:
    matchExpressions:
    - {key: env, operator: In, values: [prod]}
    - {key: ci, operator: DoesNotExist}        # CI namespaces (unique tags)
                                               # stay out of the mirror
  rewritePolicy: Always              # rewrites even if the original is available
                                     # (intra-AWS network costs, upstream quota)
  destination:
    path: 123456.dkr.ecr.eu-west-3.amazonaws.com/mirror/
    push:
      auth:
        provider:
          name: aws                  # controller IAM role pull+push+delete,
          serviceAccountRef: {name: mirror-pusher}   # never exposed outside kuik-system
    pull:
      auth: {}                       # EKS cluster: the kubelet pulls this ECR
                                     # natively, nothing to inject
      # cross-cloud variant (GKE cluster pulling this ECR):
      # auth:
      #   provider: {name: aws}
      #   injectPullSecret: true     # kuik materializes + renews (~10h)
    # pull credentials from the origins: global chain §1.7 (perPrefixFallbackAuth)
  driftPolicy: Sync                  # the mirror follows the upstream tags, at
                                     # the rate of the global budgets (§1.7)
  platforms:
    mode: List                       # known and planned node mix
    list: [linux/amd64, linux/arm64]
    includeAttestations: true
  cleanup:
    enabled: true
    retention: 168h
```

`rewritePolicy: None` variant: pure copy never routed — archival/compliance to a registry unreachable by the nodes, or bootstrap before enabling routing.

### 8.5 Minimal ImageMonitor

```yaml
apiVersion: kuik.enix.io/v1beta1
kind: ImageMonitor
metadata:
  name: cluster-images
spec:
  unusedImageRetention: 24h
  # driftDetection.enabled: true by default
```

### 8.6 Complete ImageMonitor: alternatives + drift

```yaml
apiVersion: kuik.enix.io/v1beta1
kind: ImageMonitor
metadata:
  name: cluster-images
spec:
  namespaceSelector:
    matchExpressions:
    - {key: kubernetes.io/metadata.name, operator: NotIn, values: [sandbox]}
  unusedImageRetention: 24h
  driftDetection:
    enabled: true
  monitorAlternatives: true    # monitors the ImageAlternative candidates
                               # (ImageMirror destinations remain excluded:
                               # already verified by their loop; likewise the
                               # unavailable: true prefixes, declared dead)
  # cadences, per-registry budgets and perPrefixFallbackAuth: global config (§1.7)
```

### 8.7 Secondary ImageMonitor: narrow scope on a few critical images

```yaml
apiVersion: kuik.enix.io/v1beta1
kind: ImageMonitor
metadata:
  name: critical-images        # complements the general monitor (multi-CR composition)
spec:
  podSelector:
    matchLabels:
      kuik.enix.io/critical: "true"   # per-label opt-in of critical workloads
  # a narrow scope bounds cardinality — the safe place for per-image metric
  # emission should the exporter enable it (§5.2)
```

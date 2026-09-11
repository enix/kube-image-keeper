# architecture v3

[spec v3](./spec.md) describes what an operator declares and [status v3](./status.md) what they read
back; the [walkthroughs](./walkthroughs/) follow a resource through the loops that act on it. This
document answers a different question: **which processes run, and with which permissions.**

kuik ships as a single binary started in one of three modes. They are split by **privilege and
availability profile**, not by custom resource: no process owns a kind, and every kind is touched by
more than one of them.

| Process | Runs | Availability |
| ------- | ---- | ------------ |
| `kuik-webhook` | The mutating admission webhook: matching, [candidate ordering](./spec.md#candidate-ordering), [availability probing](./spec.md#availability-probing), rewriting and annotating the pod | Several active replicas, no leader election. It answers admission requests, so it scales with the API server's traffic and must tolerate the loss of a replica |
| `kuik-reconciler` | Every control loop that talks to a registry or writes a status: the `ImageMirror` copy, self-check and cleanup loops, the `ImageMonitor` check rings, and the status controllers of all three kinds | Leader-elected, one active instance |
| `kuik-secret-syncer` | Materialising and renewing the pull secrets that `injectPullSecret` asks for, in the namespaces that need them | Leader-elected, one active instance |

## Why the split runs this way

Without it, the component that is most **exposed** would also be the most **privileged**. The webhook
sits in the path of every pod admission in the cluster, which makes it the largest attack surface and
the least appropriate holder of a cluster-wide write on Secrets. Separating the syncer out leaves the
webhook strictly read-only, and confines the only broad write permission kuik needs to a process that
receives no external traffic.

That read-only property is meant literally, and it is a constraint the rest of the split was built
around rather than a consequence of it: **the webhook's only write is the mutation it returns.** It
issues no API call of its own — the patch travels back in the `AdmissionResponse` and the API server
applies it to the object already being written. Nothing else: no status, no event, no secret, no
object of any kind.

Everything the webhook learns while resolving an image, and that someone will want later — which
resource supplied the reference, why it was chosen, what the original was — is deposited in that same
mutation, as [annotations](./observability.md#annotations) on the pod. That is what the reconciler
exists for: it reads those annotations back through an informer and turns them into the statuses of
all three kinds, and it runs every loop that has to write anything. Logic sits there not because it
belongs there conceptually, but because that is where writing is allowed.

Two things are bought with that constraint. No etcd write ever lands in the admission path, so kuik
cannot slow down or fail a pod creation by being slow to write. And the component exposed to every
admission in the cluster needs no write permission at all, which is what makes the permission table
below as short as it is.

The two leader-elected processes are separate for a different reason: they fail differently. A
reconciler that stops leaves the mirror stale — copies not made, tags not collected — which degrades
slowly and visibly in the status. A syncer that stops leaves new namespaces without a pull secret,
which surfaces as `ImagePullBackOff` on the workloads themselves. Neither should be able to take the
other down.

Colocating the mirror and monitor loops in one process is deliberate: they share the **check and copy
windows of a registry host** ([Scheduling](./spec.md#scheduling)), and a shared budget is only
enforceable inside one process.

> [!NOTE]
> The three modes are a deployment topology, not a protocol: they exchange nothing directly. Running
> them combined in a single process is a valid configuration for a small cluster, and changes no
> behaviour described anywhere in these documents.

## Data flow

Everything the processes share travels through the API server, in one direction:

```text
  reconciler ──(status: unavailable images, drift)──▶  webhook
  webhook    ──(pod annotations: original-images, rewritten-by, reason, conceded-rewrites, no-alternatives)──▶  reconciler, syncer
```

The reconciler **publishes** what it observed; the webhook **consumes** it, through informers, to try
**last** the candidates a monitor or a mirror reports failing, never to drop them
([`demoteKnownFailures`](./spec.md#demoteknownfailures-reusing-what-the-loops-already-know)). The
reverse arrow does not exist, and by now it should be clear why: a webhook writing back into the
state that drives webhooks would both invert the dependency and break the constraint above.

The annotations the webhook leaves on the pod ([Annotations](./observability.md#annotations)) are the
only channel in the other direction. They are what lets the reconciler attribute a rewrite without
re-running the resolution, and what tells the syncer which entry actually served.

One asymmetry follows and is worth knowing: a probe made at admission can contradict a monitor's
status, for two reasons. They are refreshed on different clocks —
`availabilityCheck.activeCheckCache.ttl` on one side, the registry's `check.interval` on the other —
and they do not probe with the same credentials, the webhook deliberately skipping
[`fallbackAuth`](./spec.md#no-auth-at-all) so that its verdict predicts what the node can pull. Both
are intended: routing needs a verdict that matches the pull, alerting needs one that lasts.

> [!NOTE]
> The events of [observability v3](./observability.md) are emitted by the **reconciler** and by the
> **syncer** — never by the webhook. The reconciler already reads the annotations to build the status
> gauges, and the syncer reports what it could not materialise
> ([`PullSecretInjectionFailed`](./observability.md#catalogue)); emitting from the webhook would give
> the admission path a write it does not otherwise need.
>
> Metrics divide the other way. A counter belongs to the process that witnesses what it counts, and it
> lives in that process's memory rather than in the API, so `kuik_rewrites_total` and
> `kuik_alternatives_exhausted_total` are exported by the **webhook** — one increment per admission, by
> construction. Deriving them in the reconciler would mean counting from a state rather than from an
> occurrence: the informer replays every live pod at start-up, which would re-count them all instead
> of resetting the counter.

## Permissions

Cluster-wide unless stated otherwise. `kuik-system` stands for the install namespace.

| | `kuik-webhook` | `kuik-reconciler` | `kuik-secret-syncer` |
| --- | --- | --- | --- |
| `imagealternatives`, `imagemirrors`, `imagemonitors` | get, list, watch | get, list, watch | get, list, watch |
| …`/status` | — | update, patch | — |
| `pods` | — (the `AdmissionReview` carries the pod) | get, list, watch | get, list, watch |
| `namespaces` | get, list, watch (for `namespaceSelector`) | get, list, watch | get, list, watch |
| `secrets` in `kuik-system` | get, list, watch | get, list, watch | get, list, watch |
| `secrets`, cluster-wide (read) | `permissive` only | `permissive` only | never |
| `secrets`, cluster-wide (write) | — | — | **create, patch** |
| `serviceaccounts/token` | — | — | create (for `auth.provider`) |
| `events` | — | create, patch | create, patch |
| `leases` | — | leader election | leader election |

Three absences carry more weight than the entries:

- **the webhook writes nothing at all** — not a status, not a secret, not an object of any kind
- **the syncer has no `delete`** — it never removes a Secret it created; `ownerReferences` on the
  routing resource do that when the resource itself is deleted
  ([walkthrough 03, Part C](./walkthroughs/03-secret-syncer-reconciliation.md))
- **the syncer has no read verb on `secrets` outside `kuik-system`** — the one identity able to write
  Secrets across the cluster cannot read a single one. Everything it needs to reconcile the objects it
  wrote comes from informers and from its own namespace

`serviceaccounts/token` is only needed where an `auth.provider` with a `serviceAccountRef` is
declared; the token is requested for that ServiceAccount and exchanged for registry credentials.
**Neither the webhook or reconciler performs that exchange.** They consumes the Secret the
syncer has already materialised and renewed.

> [!NOTE]
> Per-platform selection is deferred (see the `platforms` note in [ImageMirror](./spec.md#imagemirror)),
> so no process reads `nodes` in v3.0. That permission comes back with the feature.

## Bounding what the syncer may write

`create, patch` on Secrets in every namespace is a broad permission, and RBAC cannot narrow it: it
grants verbs on a resource type, never on the shape of the objects written. A
`ValidatingAdmissionPolicy` closes that gap by rejecting, at the API server, any Secret write by the
syncer's identity that is not the kind of object it is supposed to produce.

Three conditions, all required:

| Condition | Why |
| --------- | --- |
| `type == "kubernetes.io/dockerconfigjson"` | a pull secret is the only thing the syncer produces |
| carries the `managed-by` label | makes the objects it owns identifiable without reading them |
| `metadata.name` starts with a reserved prefix — `kuik-inject-` in any namespace, `kuik-provider-` in `kuik-system` only | confines it to a namespace of names it owns |

Both prefixes are reserved **both ways**: the syncer may write only under them, and no other
identity may write under them at all. The second half is what keeps the names it owns from being
squatted or tampered with — the syncer applies blind, so it would otherwise overwrite, or be overwritten by,
whatever else happened to use that name.

What remains possible if the syncer is compromised or buggy is bounded to writing a useless pull
secret under a reserved name. It cannot touch an application's own Secrets, cannot read anything, and
cannot exfiltrate.

### The name of an injected Secret

Two processes compute that name and they exchange nothing: the syncer, to write the object, and the
webhook, to append the reference to the pod's `spec.imagePullSecrets` at admission. It is a contract
between them rather than an implementation detail of either, and it is derived from **identity
alone**, kind lowercased:

```text
kuik-inject-<kind>-<CR name>          e.g. kuik-inject-imagemirror-prod-mirror
```

**The name never encodes configuration.** If it derived from, say, the source secret's name or the
matched entry, then editing the CR would rename the object — leaving the previous one orphaned in
every namespace, with no way to find it again (the syncer cannot list Secrets). Deriving the name
from the CR's identity means a configuration change alters the *content* of a stable object, and
orphans are impossible by construction.

**The kind is part of that identity.** The three routing kinds share one namespace of names, so an
`ImageAlternative/foo` and an `ImageMirror/foo` would otherwise compute the same object and overwrite
each other's credentials in every namespace they both cover — silently, since the syncer applies
blind and never reads back what is already there. The `kuik-inject-` prefix answers a different
question: it is the set reserved just above, in both directions, so it has to name something nobody
else wants.

**The name is bounded, and stays injective when it is.** A Secret name is a DNS subdomain, so 253
characters; `kuik-inject-` plus the longest kind that ever injects one (`imagealternative`) plus a
separator spends 29 of them, leaving 224 for the CR's own name. Past the limit the CR name is
truncated and a short hash of the untruncated name is appended, exactly as an over-long tag is
handled at a mirror destination ([tag naming constraints](./spec.md#tag-naming-constraints)). Two CRs
whose names differ only past the cut still get different hashes, so the mapping stays injective, and
it stays computable from identity alone — which is what lets the syncer write without ever listing,
and the webhook inject without ever reading. What is lost is only legibility, and only in that
extreme case: the `ownerReferences` still name the CR verbatim, so a `kubectl get secret -o yaml`
answers "whose is this?" whatever the name looks like.

Only a routing CR ever gets one. A credential declared in
[`fallbackAuth`](./spec.md#fallback-credentials) belongs to no resource, so there is no identity to
derive a name from: it serves the controllers' own reads and is never injected
([`injectPullSecret`](./spec.md#injectpullsecret)).

> [!NOTE]
> A pod rewritten by two different CRs gets two references in its `imagePullSecrets`. That is fine —
> it is a list, and the kubelet aggregates all of them when pulling.

### Provider credentials the controllers read

An `auth.provider` is an ambient cloud identity, and turning it into registry credentials costs a
`TokenRequest` and a cloud exchange only the syncer performs. Three declarations need those
credentials with no Secret injected anywhere: `ImageMirror`'s `destination.manage`, which never injects
by construction; a [`fallbackAuth`](./spec.md#fallback-credentials) entry, which belongs to no
resource and so has no injected Secret to carry it; and an `ImageAlternative` entry with
`injectPullSecret: false` — the default — whose background check still has to be made with that
identity.

The syncer materialises those too, under a second reserved prefix and only in `kuik-system`:

```text
kuik-provider-<kind>-<CR name>        e.g. kuik-provider-imagemirror-prod-mirror
```

The name is derived from the identity that declared the credential and from nothing else — the CR
for an `auth.provider` carried by a CR, the matched `repository` / `repositoryGroup` for a
`fallbackAuth` entry — truncated and hashed past the limit exactly as an injected name is. The
webhook and the reconciler read it from `kuik-system`, which they already may, and compute it rather
than look it up, for the reason that makes the injected name computable.

It is never copied into a user namespace: these serve kuik's own reads, where an injected Secret
serves the kubelet's pull.

## Least privilege, and the one place it costs something

The split above is not only about who may *write*. It is equally about what kuik can **read**, and
the target it was designed against is explicit: a cluster where kuik holds no access to sensitive
data at all — no `get secrets` outside its own namespace, on any component.

That target is reachable, and the whole design is arranged to keep it reachable. The webhook writes
nothing and reads only the resources it matches on. The syncer, the one process able to write Secrets
across the cluster, cannot read a single one — everything it needs to reconcile the objects it wrote
comes from informers and from `kuik-system`. Statuses are computed from pod annotations rather than
from anything privileged. In this posture, compromising any kuik process yields no application
credential, because none ever passed through it.

There is exactly one operation that pulls against this, and it is worth stating plainly rather than
hiding: **checking or copying an image on a private registry needs a credential for that registry.**
The credential the cluster already holds for it is the pod's own `imagePullSecrets` — but reading
those means reading Secrets in application namespaces, and RBAC cannot express "only Secrets a pod
references, only their registry entries". The permission exists at one granularity: all Secrets,
everywhere. So the choice is binary, and it is made at install time.

### Two modes, one ClusterRole apart

The chart renders — or does not render — a single `ClusterRole` and its binding, granting
`get, list, watch` on Secrets to the components that talk to registries. **`permissive` is the
default**, so a fresh install does *not* reach the posture described just above: it is the mode that
makes kuik work against a private registry with nothing declared, as every version before v3 did,
and moving to `restricted` is a deliberate step that may cost configuration.

```yaml
secretAccess:
  mode: permissive        # permissive (default) | restricted
  namespaces: []          # restricted only: read access granted just in these,
                          # using RoleBinding instead of ClusterRoleBinding
```

Those two are **chart values**, not keys of the [global config](./spec.md#global-config) file. They
decide whether a `ClusterRole` is rendered at all, which is settled at install time and never read by
a running process.

**`permissive`** deploys it. The webhook's admission-time probes, the `ImageMonitor` checks and the
`ImageMirror` checks and copies can then read the `imagePullSecrets` of the pods they concern, and
resolve credentials for a private registry with nothing declared anywhere. This is how kuik behaved
before v3, and it is what makes a fresh install work against a private registry out of the box.

**`restricted`** does not deploy it. kuik keeps working — nothing about the loops changes — but a
private registry it has no credential for answers `401`, and the affected images are reported as
`Unauthorized` rather than checked. Credentials are supplied instead by the operator, declared once
per repository or repository group in
[`fallbackAuth`](./spec.md#fallback-credentials) (or via `auth` in `ImageAlternative`).

> [!IMPORTANT]
> The cost of `restricted` is not proportional to the number of images, but to the number of
> **distinct credentials** the cluster's private images need. A hundred images pulled from two
> projects on one registry is two entries. A hundred images spread over as many organisations, each
> with its own robot account, is a hundred — and that is the case where `restricted` becomes real
> configuration work rather than a formality.

`namespaces` is the middle ground for exactly that case: a per-namespace `RoleBinding` gives the
convenience back where the credential sprawl is, while the rest of the cluster keeps the strong
posture. Where it has been granted stays answerable with one `kubectl get rolebindings -A`.

### What the mode does not change

Two rules keep the modes from becoming two code paths, and keep manifests portable between clusters
that chose differently:

- everything in these documents describes the **`restricted`** behaviour. The credential resolution
  is the same in both modes ([Authentication](./spec.md#no-auth-at-all)); `permissive` only decides
  whether its `imagePullSecrets` step is allowed to succeed, and a step kuik may not read degrades to
  exactly what `restricted` would have done — there is no branch where a loop behaves differently
- the mode never changes the API. A `secretRef` resolves in `kuik-system` either way
  ([Authentication](./spec.md#authentication)), so a resource written for one cluster applies
  unchanged to the other

And it never touches the syncer, in either mode: the component holding the broadest write privilege
in the cluster holds no read privilege at all, and that is not something an install-time flag can
turn off.

## The admission path

Three lines of the `MutatingWebhookConfiguration` decide how the API server treats kuik:

```yaml
failurePolicy: Ignore         # a kuik that is down must not stop a pod
rules:
- operations: [CREATE]        # a pod is routed when it is created, never later
  resources: [pods]
reinvocationPolicy: IfNeeded  # another webhook may run after kuik, so kuik may be called again
```

Each is a decision with a cost, and they are taken below in that order. The last one shapes the
admission path itself: a webhook that can be called twice has to be able to answer twice.

### Failing open

A kuik that is down, slow or misconfigured stops rewriting images — pods are admitted with the
references their authors wrote — and never stops pods from being scheduled.

This is the single most important property of the deployment, and it is what the rest of the design
is arranged around: routing is an improvement applied when it can be, never a dependency of the
cluster's ability to start a workload. It is the same reasoning that keeps the webhook free of any
write of its own, free of an informer on pods, and that bounds every probe it makes with
[`availabilityCheck.timeout`](./spec.md#global-config) — each of those is one more way an unhealthy
kuik could have delayed an admission, removed.

### Only on CREATE

The rule matches a pod when it is created, and on no other write (like `kubectl set image`, or a
controller adding a toleration).

The main reason is that **`spec.imagePullSecrets` is not mutable on a Pod.** What an update may
change is only a subset of fields and the pull secrets are not on that list. A rewrite needing an
injected credential would therefore make the API server **reject the user's own update**, and
[`failurePolicy: Ignore`](#failing-open) does not cover it: the webhook succeeded, validation is
what refuses.

Relying only on CREATE also bounds the admission cost to pod churn, rather than to every subsequent
update.

It decides which containers are routed, too. **`initContainers` are routed like any other
container** — they pull from the same registries and fail the same way, and the mirror collects them
alongside `containers`
([walkthrough 02, A.2](./walkthroughs/02-imagemirror-reconciliation.md#a2-extract-the-image-references)).
**`ephemeralContainers` never are**: they are added through a subresource `UPDATE`, which this rule
does not match, so `kubectl debug` attaches an unrouted image to a pod kuik has already served.

### Reinvocation

A mutating webhook that declares `IfNeeded` may be called again when another admission plugin has
modified the object after its first call — which is the only way kuik ever sees a sidecar an injector
added after it ran.

kuik asks for that because it cannot ask for anything better. The API server invokes mutating
webhooks in the alphabetical order of their `MutatingWebhookConfiguration` names, an implementation
detail chosen so that a serial execution is deterministic, not an ordering anyone may rely on — and
the same documentation that defines `IfNeeded` states that webhooks using it *may be reordered*, and
that the number of additional invocations *is not guaranteed to be exactly one*. Running last is
therefore not an available position: the only name that would buy it is one no other vendor has
picked yet, and it would still not survive a reordering.

What that costs is stated in the same place: a mutating webhook **must be idempotent**, able to
process an object it has already admitted and modified.

That object is never a pod coming back for a second admission — that never happens. A rescheduled
workload is a **new** Pod built from a template that still holds the original reference, resolved
afresh on the availability of the moment. Only two situations put an already-mutated spec in front
of the webhook.

One is a **reinvocation**: another mutating webhook changes the pod after kuik ran, and the API
server calls kuik again on its own output. The other is a **replayed spec**, a live Pod object
created again as it stood, like a Velero restore or `kubectl debug --copy-to`. Both carry kuik's
output **and** the record that identifies it as such, which is what the gate reads.

The gates of [what the webhook never rewrites](./spec.md#what-the-webhook-never-rewrites) exist for
those two situations, and the rest of this section is how they hold.

#### Recognising kuik's own output

Nothing is stored for it. Picking a candidate is deterministic given the candidate list and the
availability of each entry, so kuik finds its own output by **running the resolution again** from
the origin recorded in `kuik.enix.io/original-images`. It is the resolution kuik already knows how
to do, and the two invocations of a reinvocation are milliseconds apart, so it is answered from
[`activeCheckCache`](./spec.md#global-config) on all but a cold replica — which pays real probes,
bounded by `availabilityCheck.timeout`.

Every container then falls in one of four states, and the test reads the **four annotation maps**
together — a container the pod holds a record for is named by exactly one of them
([where a container appears](./observability.md#where-a-container-appears-says-what-happened-to-it)):

| State | Test | What kuik does |
| ----- | ---- | -------------- |
| **new** | no map names it | resolves it like any other container |
| **intact** | `original-images` names it and its reference is **one of** the candidates for that origin and that resource — or `no-alternatives` names it | nothing at all, and the record stands |
| **conceded** | `original-images` names it and its reference is **none of** them — or `conceded-rewrites` already names it | withdraws, and records what it lost |
| **gone** | the container is no longer in the pod | drops the entry, silently |

**A container listed in `no-alternatives` is never a concession candidate.** It holds a record but no
origin: kuik offered candidates, none answered, and the live reference is still the original one.
Reading the table on `original-images` alone would call it *conceded* the moment another webhook
changed its image, and kuik would record a `was` it never placed — a gauge and an event for a rewrite
that never happened.

**Membership** is what decides, not equality with the candidate the resolution returns now. The two
part ways in a case that is not exotic: a spec replayed days later, whose origin has become
available again in the meantime. The resolution answers "I would take the origin" while the pod
carries the mirror — the same candidate list, a different element of it. Equality would read a
conflict where there is none, drop the attribution and report a concession that never happened;
membership recognises kuik's own output and leaves it where it is. It is the same rule as
[the rewrite is not sticky](./walkthroughs/01-routing-only.md#2-pod-admission-mutating-webhook),
read from the other end: a live pod is never un-rewritten, and an origin that comes back is followed
on the next rollout.

A rewrite to **another entry of the same resource** is *intact* for that reason too. Deliberately:
nothing lies — the origin is right and so is the attribution — and the only fact lost is one nobody
acts on.

A container the pod holds no record for is *new* even when another webhook has just written its
image. kuik rewrites the reference it finds and only refuses to play over its own; anything else and
it would stop rewriting altogether as soon as a sidecar injector runs before it. Which is also why
all of this is read per container rather than per pod: the sidecar just injected is routed like any
other image, while the containers kuik already served are left exactly as they are.

**kuik rewrites a container at most once per admission.** Once rewritten, its reference belongs to
kuik's own candidates, so every later round reads it as *intact* or *conceded* and never as *new* —
whatever the other webhook does, and however many rounds the API server runs. That sentence is the
whole termination argument, and it assumes nothing about the other webhook.

#### What conceding removes

kuik does not rewrite over a reference another webhook chose. Two webhooks disputing one image field
produce a result that depends on invocation order, and that order is not kuik's to control — so kuik
withdraws, and records what it lost:

```yaml
kuik.enix.io/conceded-rewrites: '{"nginx":{"from":"docker.io/library/nginx:1.27","was":"registry.tld/mirror/docker.io/library/nginx:1.27_cluster-a","by":"ImageMirror/prod-mirror"}}'
```

Three fields, and the image that won is not among them: it is on the container, where duplicating it
would only give it a chance to diverge. `was` is what the resolution above returns, which in the
case this is written for — a reinvocation, milliseconds after the rewrite — is the reference kuik
had placed.

The container leaves `original-images`, `rewritten-by` and `reason`, and does not enter
`no-alternatives`. Downstream it becomes indistinguishable from a container kuik never touched,
which is what it now is: the status controllers fall back to the live reference
([Attribution](./status.md#attribution)). `conceded-rewrites` has exactly one reader, the reconciler,
which turns it into an event and a metric series ([observability](./observability.md#annotations)).

The entry is also what makes the state stable: a container listed there is never taken up again, so
a further round reaches the same decision and produces no patch.

**The pull secret follows as an invariant, not as an action.** When the pass ends, the pod carries a
kuik-injected name (`kuik-inject-<kind>-<name>`) **if and only if** a container still attributed to
that resource in `rewritten-by` needs an injected credential
([`injectPullSecret`](./spec.md#injectpullsecret)). Put that way it is idempotent across
reinvocations, self-healing on a replayed spec, and it settles on its own the pod whose *other*
containers that resource still serves. The only name it ever removes is the one the syncer
materialised for that resource — the `imagePullSecrets` the pod declared itself are never touched —
and that secret has no business serving an image another webhook chose, since it covers the registry
of an alternative kuik no longer supplies. Leaving it behind would leave a dangling reference, the
syncer collecting the Secret as soon as the attribution is gone
([walkthrough 03](./walkthroughs/03-secret-syncer-reconciliation.md)).

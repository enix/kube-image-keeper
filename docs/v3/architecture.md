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
  webhook    ──(pod annotations: original-images, rewritten-by, reason, no-alternatives)──▶  reconciler, syncer
```

The reconciler **publishes** what it observed; the webhook **consumes** it, through informers, to
skip candidates a monitor or a mirror recently found unavailable
([`skipHints`](./spec.md#global-config)). The reverse arrow does not exist, and by now it should be
clear why: a webhook writing back into the state that drives webhooks would both invert the
dependency and break the constraint above.

The annotations the webhook leaves on the pod ([Annotations](./observability.md#annotations)) are the
only channel in the other direction. They are what lets the reconciler attribute a rewrite without
re-running the resolution, and what tells the syncer which entry actually served.

One asymmetry follows and is worth knowing: a probe made at admission can contradict a monitor's
status for a while, since the two are refreshed on different clocks —
`availabilityCheck.activeCheckCache.ttl` on one side, the registry's `check.interval` on the other.
That is intended: routing needs a verdict now, alerting needs one that lasts.

> [!NOTE]
> The events of [observability v3](./observability.md) are emitted by the **reconciler**, not by the
> webhook: it already reads the annotations to build the status gauges, and emitting from the webhook
> would give the admission path a write it does not otherwise need.
>
> Metrics divide the other way. A counter belongs to the process that witnesses what it counts, and it
> lives in that process's memory rather than in the API, so `kuik_rewrites_total` and
> `kuik_no_alternatives_total` are exported by the **webhook** — one increment per admission, by
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
| `serviceaccounts/token` | create (for `auth.provider`) | create | create |
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
| `metadata.name` starts with the reserved `kuik-inject-` prefix | confines it to a namespace of names it owns |

The prefix is reserved **both ways**: the syncer may write only under it, and no other identity may
write under it at all. The second half is what keeps the names it owns from being squatted or
tampered with — the syncer applies blind, so it would otherwise overwrite, or be overwritten by,
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
[`perPrefixFallbackAuth`](./spec.md#global-config) belongs to no resource, so there is no identity to
derive a name from: it serves the controllers' own reads and is never injected
([`injectPullSecret`](./spec.md#injectpullsecret)).

> [!NOTE]
> A pod rewritten by two different CRs gets two references in its `imagePullSecrets`. That is fine —
> it is a list, and the kubelet aggregates all of them when pulling.

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

**`permissive`** deploys it. The webhook's admission-time probes, the `ImageMonitor` checks and the
`ImageMirror` checks and copies can then read the `imagePullSecrets` of the pods they concern, and
resolve credentials for a private registry with nothing declared anywhere. This is how kuik behaved
before v3, and it is what makes a fresh install work against a private registry out of the box.

**`restricted`** does not deploy it. kuik keeps working — nothing about the loops changes — but a
private registry it has no credential for answers `401`, and the affected images are reported as
`Unauthorized` rather than checked. Credentials are supplied instead by the operator, declared once
per prefix in [`perPrefixFallbackAuth`](./spec.md#global-config) (or via `auth` in `ImageAlternative`).

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

- everything in these documents describes the **`restricted`** behaviour. `permissive` only *adds*
  one step to the same credential resolution, and anything unavailable there degrades to exactly what
  `restricted` would have done — there is no branch where a loop behaves differently
- the mode never changes the API. A `secretRef` resolves in `kuik-system` either way
  ([Authentication](./spec.md#authentication)), so a resource written for one cluster applies
  unchanged to the other

And it never touches the syncer, in either mode: the component holding the broadest write privilege
in the cluster holds no read privilege at all, and that is not something an install-time flag can
turn off.

> [!NOTE]
> `permissive` mode is the default. to makes a fresh install work against private registries with
> no declaration at all, as previous kuik version did. Switching to `restricted` may require more
> configuration and should be a conscious choice.

## Failing open

The mutating webhook is registered with `failurePolicy: Ignore`. A kuik that is down, slow or
misconfigured therefore stops rewriting images — pods are admitted with the references their authors
wrote — and never stops pods from being scheduled.

This is the single most important property of the deployment, and it is what the rest of the design
is arranged around: routing is an improvement applied when it can be, never a dependency of the
cluster's ability to start a workload. It is the same reasoning that keeps the webhook free of any
write of its own, free of an informer on pods, and that bounds every probe it makes with
[`availabilityCheck.timeout`](./spec.md#global-config) — each of those is one more way an unhealthy
kuik could have delayed an admission, removed.

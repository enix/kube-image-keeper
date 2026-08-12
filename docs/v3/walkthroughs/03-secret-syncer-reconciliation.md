# Secret-syncer — reconciliation walkthrough

## The reconcile unit

The syncer does not reconcile pods, and it does not reconcile CRs. Its unit of work is the pair:

```
(routing CR, namespace)
```

One such pair produces **exactly one Secret**. A pod event fans out to one key per CR that rewrote one of its containers; a CR event fans out to one key per namespace where that CR currently has rewritten pods.

This is what makes the rest simple: for a given pair, the desired Secret is a **pure function of informer state** — recomputable at any time, without reading anything back. What exactly it is a function of depends on the CR's `rewritePolicy`, which is the subject of A.1 and A.2.

---

## Part A — Materializing a secret

### A.1 Notice that a namespace needs one

The trigger depends on the CR's `rewritePolicy`, and the difference is not cosmetic — it decides whether the need can be *predicted* or only *observed*.

- **`Always`** — a namespace entering the CR's scope is enough. Every matched pod there will be rewritten by definition, so the credentials are known to be needed before any pod exists. The syncer reacts to namespace label changes and to CR selector changes.
- **`OnFailure`** — a rewrite has to actually happen first. Whether an alternative is used at all depends on an origin registry being unavailable at admission time, which nothing can predict. The syncer watches pods and reacts to the first one whose `kuik.enix.io/rewritten-by` annotation names this CR.

In both cases the pair `(C, N)` comes into existence for the syncer at that moment, and stops being reconciled only when the CR or the namespace goes away.

### A.2 Determine which credentials are needed

| `rewritePolicy` | Desired set | Derived from |
|---|---|---|
| `Always` | every injectable entry of the CR | the CR spec and the namespace labels |
| `OnFailure` | the entries used by live rewritten pods of that namespace, plus those used recently (C.1) | the observed pods |

Under **`Always`**, no observation is required: if the namespace is in scope, its pods will land on the CR's alternatives, so all of them are provisioned up front. The set is stable across pod churn — a scale-to-zero, a rollout, an evicted node change nothing.

Under **`OnFailure`**, the set has to be read off the pods. For each relevant pod, and for each of its containers that `rewritten-by` attributes to `C`:

1. Look at the container's **current** image — the rewritten one, as it stands in the pod spec.
2. Match it against `C`'s alternatives (or, for an `ImageMirror`, against its destination path) to identify **which entry** served that rewrite.
3. If that entry carries an `auth` and injection is enabled for it, add the entry to the set.

The result is usually one entry, sometimes two, almost never all the ones the CR declares: a CR with five authenticated alternatives whose pods in this namespace only ever landed on one of them contributes exactly one credential here. Entries that were in the set recently are kept for a while longer — see C.1.

> Injection is enabled per entry: on by default for a `secretRef` (if the controller needs the credential to check the registry, the kubelet needs it to pull), off by default for a `provider` (the common case is a cluster whose nodes are already authorized by the cloud platform). Either default can be overridden.

### A.3 Resolve each credential to actual bytes

- **`secretRef`** → read the named Secret from `kuik-system`. This is the one namespace the syncer may read, and referencing a credential means having been able to put it there.
- **`provider`** → obtain a token from the cloud platform: request a ServiceAccount token for the referenced ServiceAccount, exchange it for registry credentials. The result is short-lived, which is what the refresh loop (Part B) exists for.

A credential that cannot be resolved — missing source Secret, rejected token request — does not block the others: the entry is skipped, an event is emitted, and the Secret is written with what could be resolved. A partially useful pull secret is better than none.

### A.4 Build the desired object

One Secret, fully determined by the pair:

| Field | Value |
|---|---|
| `name` | `kuik-<CR name>` — derived from **identity**, never from configuration |
| `namespace` | the target namespace |
| `type` | `kubernetes.io/dockerconfigjson` |
| `.dockerconfigjson` | one `auths` entry per registry from A.2/A.3 |
| labels | the `managed-by` marker the admission policy requires |
| `ownerReferences` | the cluster-scoped routing CR |

Three properties are worth understanding rather than just implementing.

**The name never encodes configuration.** If it derived from, say, the source secret's name or the matched entry, then editing the CR would rename the object — leaving the previous one orphaned in every namespace, with no way to find it again (the syncer cannot list Secrets). Deriving the name from the CR's identity means a configuration change alters the *content* of a stable object, and orphans are impossible by construction.

**One merged Secret, not one per registry.** `dockerconfigjson` natively holds several registries, and the kubelet picks the right entry by matching the image's registry at pull time. Merging means one object to reconcile, one reference to inject, and atomic updates. Splitting per registry would multiply objects and references for no gain.

**The owner is the CR, not the pod.** A namespaced object may legally have a cluster-scoped owner. This is what makes deletion free (Part C) and, just as importantly, what keeps the Secret alive across pod churn: pods come and go, the CR persists.

> A pod rewritten by two different CRs gets two references in its `imagePullSecrets`. That is fine — it is a list, and the kubelet aggregates all of them when pulling.

### A.5 Apply it blind

The desired object is applied with **server-side apply**, using kuik's field manager. No read precedes it: the `patch` verb does not require `get`, and the object being applied was computed entirely from informers and from `kuik-system`.

Two consequences of applying blind:

- If a Secret already exists at that name — pre-created by a chart, left over from a previous run — the field manager takes ownership of the fields kuik manages and overwrites them. That is the intended behavior for a name that contractually belongs to the syncer.
- If the content is identical to what is already stored, the API server treats the apply as a **no-op**: no new resourceVersion, no watch event, no write. Correctness therefore never depends on the syncer knowing whether it already applied something.

### A.6 Skip the work when nothing changed

Blind applies are cheap but not free, and a rollout can generate hundreds of pod events that change nothing. Two dedup layers, in this order:

1. **Debounce per pair.** Events for `(C, N)` arriving in a short window collapse into one reconcile. A 100-pod rollout is a burst of creates and deletes that almost always leaves the *set of used entries* unchanged.
2. **In-memory desired-state hash per pair.** Hash the computed object; if it matches the last applied hash for that pair, skip the apply entirely.

The hash is an optimization and nothing more: losing it on restart costs one redundant apply per pair, which the server turns into a no-op anyway.

### A.7 The first-pull race (`OnFailure` only)

Under `Always` this section does not apply: the Secret is provisioned when the namespace enters scope, well before any pod is admitted.

Under `OnFailure`, at the very first rewrite in a namespace, the webhook injects the reference into the pod spec at admission, but the Secret itself only appears a moment later — the syncer has to see the pod through its informer and apply.

The kubelet tolerates this: a missing pull secret produces a warning, the pull is attempted without it, and the retry backoff **re-reads the Secrets on every attempt**. So the first pod may spend a few seconds retrying, and every subsequent pod finds the Secret already in place.

This is bounded to **one occurrence per (namespace, CR), for the lifetime of the CR**. And it happens while an origin registry is down — a situation that is already degraded, and in which one or two backoff intervals do not change the nature of the incident.

---

## Part B — Keeping secrets valid

### B.1 A source credential changed

The syncer watches Secrets in `kuik-system`. When one changes, every pair whose content includes it is invalidated (drop the cached hash) and re-applied. The map of which pair uses which source is already in memory from Part A — no lookup in the cluster is needed.

### B.2 A token is about to expire

Provider-issued credentials are short-lived — on the order of hours. Each one gets a refresh scheduled comfortably **before** expiry, not at it: the point is to still have a working credential while retrying if the refresh fails.

A refresh recomputes and re-applies every pair that includes that provider entry. Two practical consequences:

- A namespace's Secret is rewritten at the pace of its **most volatile** credential. A namespace holding one static `secretRef` and one provider token is rewritten on the provider's schedule.
- A refresh that fails while the old credential is still valid is the moment to raise a warning. Once it has expired, the symptom is `ImagePullBackOff` in application namespaces, and the cause is far less obvious.

### B.3 A CR changed

Editing a routing CR — new alternative, changed `auth`, injection toggled, selectors narrowed — invalidates every pair for that CR, and the content is recomputed as in A.2. A selector change also adds or removes pairs: under `Always`, a namespace entering scope gets its Secret provisioned there and then.

### B.4 Someone edited a managed Secret

The syncer cannot detect this: detecting it would mean reading. Instead, it bounds how long a foreign edit can survive by **re-applying every known pair periodically**, on the informer's resync interval. Server-side apply restores the fields kuik owns; every one of those applies is a server-side no-op unless something actually diverged.

### B.5 The controller restarted

Nothing is progressive here. The pod informer's **initial List** delivers every existing pod before the first event is processed, so the full map of `(CR, namespace) → used entries` is rebuilt up front. A **startup pass** then applies every pair.

That pass is how a source credential that changed while the controller was down gets propagated: it does not wait for a new rewrite to happen in each namespace. Almost all of those applies are no-ops; the ones that are not are exactly the ones that were missed.

---

## Part C — Release

### C.1 A namespace stops using a credential

This only concerns `OnFailure`. Under `Always` the desired content does not depend on pods at all, so it does not shrink when they go away — the Secret keeps every injectable entry as long as the namespace is in scope, which is precisely what makes that policy free of the first-pull race.

Under `OnFailure`, the content follows the live rewritten pods, and it **has** to: leaving an entry in place would require knowing it is there, which means either reading the Secret — off-limits — or persisting the fact somewhere. The desired content must be computable from what is observed, so it necessarily shrinks. The only real choice is *how fast*.

**Not immediately.** An entry that leaves the computed set is kept for a grace period before it is actually dropped. This distinguishes the two very different reasons an entry stops being used:

| Why the entry disappeared | What it means | What the grace does |
|---|---|---|
| The origin registry recovered; pods are no longer rewritten | The credential is genuinely no longer needed, and durably so | The entry ages out and is dropped |
| Scale-down, rollout, eviction — while the origin is still down | The pods are coming back, and they will need it again in seconds | The entry survives; the returning pods find a working Secret |

Without the grace, that second row is not merely wasteful — dropping the entry means the next pod to come up during the outage meets an emptied Secret, which fails a pull exactly like a missing one. The race would be replayed on every scale-up, throughout the incident, which is the worst possible moment for it.

The grace is tracked in memory, per (pair, entry). Losing it on restart makes the entries in their grace window drop at the next reconcile — so a restart *shortens* how long a credential lingers, never lengthens it. For a credential, that is the safe direction to fail, and the resulting behavior is simply the ungraced one.

**The object itself is not deleted.** It stays, its `auths` map possibly reduced to nothing. The `.dockerconfigjson` key is always written — the API requires it to be present and to hold valid JSON — so the emptied form is `{"auths":{}}`, which is accepted.

Keeping it buys nothing operationally: an emptied Secret fails a pull exactly like a missing one, so it does not shield the next rewrite from the race. It is kept because deleting it would add an operation and a decision point for no benefit — the `ownerReferences` already handle removal when it actually matters (C.2), and an object holding no credential leaks nothing.

### C.2 A CR is deleted

Nothing to do. Every Secret the syncer created carries an `ownerReference` to that CR, so Kubernetes garbage-collects all of them, in every namespace, without the syncer needing to know where they were.

This is also why the owner must be the CR and not something else: it is the only handle that covers "everything this CR ever caused to exist".

---

## The no-read discipline

Reconciliation never requires reading a Secret outside `kuik-system`. It is worth seeing the four techniques together, because each one replaces a lookup that would otherwise be necessary:

| Would normally require | Replaced by |
|---|---|
| Reading the existing object before updating it | Blind server-side apply — `patch` needs no `get`, and identical content is a server-side no-op |
| Searching for objects created earlier | Names derived from CR identity — always recomputable, never stale |
| Tracking what to delete | `ownerReferences` — Kubernetes does the cleanup |
| Listing to detect drift | Periodic blind re-apply — bounded staleness instead of detection |

**The one thing this rules out**: deciding whether a pod *already* has a working credential for a registry, and skipping injection if so. A pod's existing `imagePullSecrets` are visible by name, but a name says nothing about which registries the Secret covers — only its content does.

This is not blocking. Injecting a redundant credential is harmless: the kubelet tries every credential matching the registry until one works. And where the redundancy is known in advance, injection can be turned off on the entry itself.

---

## Cross-cutting invariants

- **The desired state is a pure function of informer state** — the CR spec and namespace labels under `Always`, the live rewritten pods under `OnFailure`. The only thing added on top is the in-memory grace of C.1, which can only *retain* an entry a little longer and whose loss degrades to the pure function. Nothing accumulates; every reconcile recomputes from scratch. This is what allows blind writes, cheap restarts, and correctness without bookkeeping.
- **The syncer never deletes a Secret it created.** It empties them; removal happens through ownership.
- **Nothing the syncer keeps in memory is load-bearing.** The pair→hash map and the entry map are rebuilt from the informers at startup; the grace timers are not rebuilt at all. Losing any of it costs redundant applies or a slightly earlier credential removal, never correctness.
- **Writing is bounded by an admission policy, not by trust in the code.** Writing Secrets in every namespace is a broad permission, and RBAC cannot narrow it — it grants verbs on a resource type, not on the shape of the objects written. A `ValidatingAdmissionPolicy` closes that gap: it rejects, at the API server, any Secret write by the syncer's identity that is not of type `kubernetes.io/dockerconfigjson`, does not carry the managed-by label, or falls outside the reserved name prefix. The prefix is reserved **both ways** — the syncer may only write under it, and nobody else may write under it at all, so the names the syncer owns cannot be squatted or tampered with. The result: a bug, or a compromise, of the syncer can produce a useless pull secret under a reserved name; it cannot touch an application's own Secrets, cannot read anything, and cannot exfiltrate.
- **The syncer is never in the admission path.** It reacts to pods that have already been admitted, so a slow or unavailable syncer delays a first pull — it never delays or blocks scheduling.

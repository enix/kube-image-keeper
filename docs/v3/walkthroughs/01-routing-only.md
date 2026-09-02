# walkthrough: routing only (example 01)

This walkthrough follows [example 01](../examples/01-routing-only.yaml) through every component that
touches it, in the order things happen. It defines nothing: the rules it invokes live in
[spec v3](../spec.md) and [status v3](../status.md) and are linked from each step.

## The case

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ImageAlternative
metadata:
  name: docker-library
spec:
  alternatives:
    - repositoryGroup: public.ecr.aws/docker/library
    - repositoryGroup: mirror.gcr.io/library
    - repositoryGroup: docker.io/library
```

Everything here is a default: no `podSelector` and no `namespaceSelector` (it applies to every pod in
the cluster), no `rewritePolicy` (so `OnFailure`), three entries in `repositoryGroup` form, three public
registries. Nothing is ever copied to a registry: this is pure routing, the v3 equivalent of a v2
`ClusterReplicatedImageSet`. The pod is created with `image: nginx:1.27`.

The relevant [global config](../spec.md#global-config): `availabilityCheck.timeout: 2s`,
`activeCheckCache.ttl: 10s`, `demoteKnownFailures` enabled, and `dockerhub-creds` declared
in `fallbackAuth` for `repositoryGroup: docker.io`.

## 1. `kubectl apply`, validating webhook

Runs once, before any controller sees the object, and checks only what is local to the CR: exactly
one of `repository`/`repositoryGroup` per entry, no tag or digest, no mixing of the two forms
([invalid `alternatives`](../spec.md#invalid-alternatives)). Both are CEL-expressible:

```text
self.alternatives.all(a, has(a.repository) != has(a.repositoryGroup))
self.alternatives.all(a, has(a.repository)) || self.alternatives.all(a, has(a.repositoryGroup))
```

Cross-CR overlap is deliberately *not* checked here: it needs a cluster-wide view and would make
admission outcomes depend on apply order. Overlap is resolved at lookup time instead, by
[candidate ordering](../spec.md#candidate-ordering).

## 2. Pod admission, mutating webhook

1. **gates** — per container: one whose reference kuik produced itself, or one with
   `imagePullPolicy: Never`, is left alone
   ([what the webhook never rewrites](../spec.md#what-the-webhook-never-rewrites)). The first cannot
   fire on a first admission: it decides what happens on a
   [reinvocation or a replayed spec](../architecture.md#reinvocation).
2. **normalization** — `nginx:1.27` becomes `docker.io/library/nginx:1.27`, which is what matching,
   cache keys and status keys all use. It matters here: only after normalization does the pod's image
   match the third entry
3. **CR selection** — three cluster-wide informer-backed lists against v2's four (two cluster-scoped
   kinds plus two namespaced ones listed per pod namespace), then `podSelector` and
   `namespaceSelector`. Both are empty here, so the CR always applies. `ImageMonitor` is one of the
   three even though it [never contributes a candidate](../spec.md#candidate-ordering): the webhook
   reads it for the negative check results
   [`demoteKnownFailures`](../spec.md#demoteknownfailures-reusing-what-the-loops-already-know)
   consumes
4. **matching** — [alternatives matching](../spec.md#alternatives-matching) selects entry 3, the
   `repositoryGroup` `docker.io/library`, with remainder `nginx` and tag `1.27`
5. **ordering** — [candidate ordering](../spec.md#candidate-ordering). Only this CR matches and it is
   `OnFailure`, so the original stays at the pivot and the other two entries follow it:

   ```text
   0. docker.io/library/nginx:1.27           <- the original, at the pivot
   1. public.ecr.aws/docker/library/nginx:1.27
   2. mirror.gcr.io/library/nginx:1.27
   ```

6. **probing** — [availability probing](../spec.md#availability-probing). All three registries are
   public so probes are anonymous, except on `docker.io` where the global `fallbackAuth`
   supplies credentials
7. **rewrite and annotate** — nothing to do if the original answers; otherwise the container is
   patched and [annotated](../observability.md#annotations):

   ```yaml
   kuik.enix.io/original-images: '{"nginx":"docker.io/library/nginx:1.27"}'
   kuik.enix.io/rewritten-by:    '{"nginx":"ImageAlternative/docker-library"}'
   kuik.enix.io/reason:          '{"nginx":"OnFailure"}'
   ```

8. **pull secret** — if the retained candidate's `auth` asks for an injection
   ([`injectPullSecret`](../spec.md#injectpullsecret)), the webhook appends the syncer's Secret name
   for that CR to `spec.imagePullSecrets`, computed from identity alone and never read back
   ([the name of an injected Secret](../architecture.md#the-name-of-an-injected-secret)). Nothing to
   inject here: this CR declares no `auth`, and the `fallbackAuth` of step 6 is never injected

> [!IMPORTANT]
> That `fallbackAuth` entry is not cosmetic: anonymous Docker Hub `HEAD`s are the ones that
> get 429'd, and a 429 on the *check* would read as "the original is down" and reroute the whole
> cluster to ECR.

Two properties worth noting for this CR. A pod that directly references
`public.ecr.aws/docker/library/nginx:1.27` matches entry 1 of the same CR and gets the same three
candidates, with ECR now at the pivot. And the rewrite is not sticky: nothing re-decides it, because
the pod that carries it never returns to admission. The next pod of that Deployment is built from
the template, with `nginx:1.27` in it, and gets the candidate that is available *then* — so a Docker
Hub outage that ends is followed back on the next rollout, with nothing to unwind.

Two implementation costs, both consequences of this CR carrying no selector, so that every pod in the
cluster goes through it:

- `namespaceSelector` is a label selector on the `Namespace` object, so the webhook needs a namespace
  lister where v2's `spec.filter.namespace` was a regex on the name. A selector that is exactly
  `matchLabels: {kubernetes.io/metadata.name: <ns>}` (the form replacing v2 namespaced resources, as
  in [example 04](../examples/04-x509-cert-exporter-namespaced-resource.yaml)) can be answered from
  `pod.Namespace` alone
- matching every pod in the cluster is what makes the O(segments) trie lookup worth it over v2's
  per-CR regex evaluation

## 3. ImageAlternative status controller

Leader-elected and informer-driven, triggered by pod and CR events and debounced because pod churn is
high. Per [status v3](../status.md) it makes **no registry calls at all**, and the webhook writes
**no status**: the annotations from step 2 are the entire channel between the two.

Per reconcile: select pods with the selectors, match each container's original reference the same way
the webhook did, classify from `rewritten-by`, `reason` and `no-alternatives` per
[attribution](../spec.md#attribution), then aggregate into `activeFallbacks` and `noAlternatives` and
patch only on change.

Steady state here: `rewritten: 0`, `activeFallbacks: []`, conditions green. During a Docker Hub
incident:

```yaml
status:
  activeFallbacks:
    - image: docker.io/library/nginx:1.27
      routedTo: public.ecr.aws/docker/library/nginx:1.27
      pods: 12
      since: "2026-07-31T09:14:00Z"
  conditions:
    - type: FallbackActive
      status: "True"
      reason: OriginUnavailable
      message: "1 image routed to fallback (12 pods)"
```

That condition flipping is the alert signal, and the main reason this controller exists at all for a
routing-only CR.

> [!NOTE]
> Two implementation costs: `since` has to be carried forward for entries that already exist, or it
> resets on every controller restart and becomes useless for alerting; and counting pods naively means
> a full pod list per reconcile, so it wants an incremental image → pods index fed from informer
> events.

## 4. ImageMirror and ImageMonitor controllers

**Not involved.** No `destination`, so no copy loop, no self-check loop, no `cleanup` GC, no
`repositories` inventory, no push credentials, no secret injected into user namespaces.

What changes when an `ImageMirror` matches these pods too — which
[example 05](../examples/05-global-mirror-force-rewrite.yaml) does cluster-wide — is
[example 07](../examples/07-alternative-and-mirror-composition.yaml).

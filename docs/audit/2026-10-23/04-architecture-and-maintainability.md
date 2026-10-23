# Architecture and maintainability

The package boundaries are generally useful: routing, registry access, pacing, status projection and Secret materialization are separable and well tested. The major defects arise where those components disagree about authorization, ownership, identity or lifetime. Repair those contracts before undertaking broad package moves.

## Component and data-flow map

| Boundary | Inputs and outputs | State and consequential interactions |
| --- | --- | --- |
| `cmd/`, `internal/config` | Process flags, strict YAML configuration, reloads → webhook/reconciler/syncer wiring | Readiness must cover the first usable routing snapshot as well as cluster ID and TLS trust (D07). |
| `api/kuik/v1alpha1`, generated `config/`, Helm CRDs | User resources → defaults, validation and status schemas | Generated artifacts agree with source at this revision. Existing installations need a separate CRD upgrade contract (D04). |
| `internal/imagepath`, `routing`, `mirrorpath` | Normalized origin and selectors → ordered candidates and destination names | Admission preference is not copy-source preference (C03). An output reference does not identify the entry that produced it (R03). |
| `internal/webhook/core/v1` | Pod CREATE plus cached resources → probes, rewritten images, annotations and pull-secret references | Records cross back from a user-writable object. Probe waiters, cache entries and admission requests have distinct lifetimes (R01/R04/RA01). |
| `internal/auth`, `registry/keychain`, `auth/pullsecret` | Declared credentials and Pod pull Secrets → authenticators or injected Secret data | Per-image keyring lookup and prefix projection are not equivalent (R02). Explicit `secretRef` sources are installation-scoped; Pod pull Secrets may be read in the Pod namespace according to `secretAccess`. Syncer writes are constrained separately. |
| `internal/controller/secretsyncer` | Routing resources, namespace/Pod/source-Secret events → one deterministic Secret per resource/namespace | Blind writes avoid broad destination Secret reads. Process memory cannot prove absence after restart (C01). |
| `internal/controller/kuik` | Live Pods, resource specs and observations → monitor/mirror status, copy and cleanup work | Resource UID, shared destinations, durable repository inventory and pending deletions must outlive the right asynchronous operations (C05–C08). |
| `internal/registry/pacing`, `registry`, `registry/tlstrust` | Host windows, endpoints, auth and trust material → HEAD, copy, tag list/delete | Quota sharing does not imply verdict equivalence (R05). Secure transport policy must apply before dependency fallback (R07). |
| `internal/status/*`, metrics/events | Observations → capped API samples, aggregate counts and Prometheus series | A sample is not a population, and a status cap is not a memory/cardinality bound (C08/A05). |
| Helm, Task, CI, acceptance tooling | Source → generated manifests, images, chart, installed operator and cleanup | Build completion, public promotion, readiness and cleanup each need explicit dependency ordering (D04–D07/B01). |
| `docs/`, website sync and overlay | Markdown plus maintained v2 snapshot → static pages and local preview | Full build and incremental deletion need the same merge precedence (B02). No publication occurred in this audit. |

The critical round trip is **candidate → probe credential → Pod record → injected Secret → kubelet pull**. Current tests cover individual stages more strongly than the equivalence of the whole chain. The mirror's analogous round trip is **persist inventory → commit upload → observe destination → retire/delete**. A passing memory race detector cannot establish either invariant.

## A01 — P1 — Define one authenticated routing and credential contract

**Classification: architecture recommendation grounded in R01/R02/R03/R05.** References: `internal/routing/podrecord/podrecord.go:26`; `internal/auth/pullsecret/pullsecret.go:163,194,239`; `internal/registry/pacing/scheduler.go:355`.

Attribution, credential authorization and candidate identity currently share an annotation without sharing a complete contract. The webhook has the selected entry and actual probe credentials. The syncer reconstructs intent from an output prefix. Background checks then reuse a verdict without its authentication context.

Define explicit types and invariants for three distinct things: a displayable routing record, authority to distribute a particular credential, and the effective context of a registry check. Retain the chosen entry identity in a verifiable form. Handle resource edits, Secret rotation, resource UID changes and historical selector grants deliberately. A Pod name can be reused after deletion; define provenance that handles CREATE as well as subsequent object incarnations.

Keep the Kubernetes keyring's per-image matching and retry semantics as the compatibility reference; the repository's vendored implementation and caller tests already provide a local starting point. Do not widen a narrow repository credential to solve projection. Add one integrated contract suite through candidate selection, record handling and materialization, followed by actual kubelet pulls on Kind. Acceptance criterion: the generated credential set can authorize the selected image and cannot authorize distribution to an excluded namespace.

## A02 — P1 — Make repository ownership and write reservations explicit

**Classification: architecture recommendation grounded in C05/C06.** References: `internal/mirrorpath/plan/sweep.go:75`; `internal/controller/kuik/imagemirror_copy.go:409,593`; `internal/controller/kuik/imagemirror_controller.go:285`.

A cluster suffix distinguishes clusters but not mirror resources. A recorded repository distinguishes known from unknown storage but does not currently reserve it against retirement during upload. Those are separate ownership and concurrency dimensions.

Choose either an enforced exclusive destination domain or coordinated ownership across mirrors. Include effective repository overlap, nested paths and finalization in that decision. Then model repository retirement as a transition that excludes in-flight commits and live expectations. Preserve inventory before external writes; a second status write after upload alone leaves a crash window. Tests should pause at each durable-state boundary and restart with changed Pods.

Keep the pure `mirrorpath/plan` calculations, but give them all necessary ownership/reservation information. The acceptance criterion is stronger than a clean status: no active owner's tag is deleted, and every committed tag remains discoverable until intentionally removed.

## A03 — P2 — Carry operation context and resource incarnation through asynchronous work

**Classification: architecture recommendation grounded in C02/C03/C07/R04.** References: `internal/controller/kuik/imagemirror_copy.go:279,378,445`; `internal/controller/kuik/imagemonitor_checks.go:433`; `internal/webhook/core/v1/probe.go:52,110`.

The code reuses operations whose policies differ: admission order for source copying, full source copy for destination-only retag, and name-only callbacks after UID replacement. Make copy, resync and retag distinct operations with explicit source/destination rules. Attach a resource incarnation token to queued work and validate it on completion; cancellation alone cannot prevent a late callback.

For shared registry reads, keep the shared request's timeout/process lifetime separate from each admission waiter's lifetime. Reuse common low-level registry operations without reusing higher-level policy accidentally. Regression tests should verify observable requests and digests, not just whether a work item was scheduled.

## RA01 — P2 — Cache insertion performs a full locked scan

**Classification: measured performance concern and architecture recommendation; no production outage demonstrated.** Reference: `internal/webhook/core/v1/probe.go:77`.

Every store scans the entire cache under one mutex to expire old entries. A burst filling N distinct keys therefore performs quadratic aggregate scan work; hits contend with the same lock. There is no entry-capacity bound. TTL limits lifetime, not the number of distinct references and credential identities admitted during it.

An audit-only overlay benchmark exercised the actual `checkCache.store`, replacing one entry in a preloaded unexpired cache. Each case used a 100 ms benchmark budget and ran separately from baseline coverage:

| Live entries | Observed cost per store |
| --- | --- |
| 100 | 4,023 ns |
| 1,000 | 34,836 ns |
| 10,000 | 417,146 ns |

These measurements establish growth, not a production capacity limit. Introduce bounded capacity and amortized expiration, such as expiration buckets or a heap with bounded cleanup work. Preserve credential-sensitive keys and singleflight behavior. Benchmark mixed hit/miss traffic, churn and concurrent waiters before selecting defaults; measure tail latency, allocations and lock wait time.

## A04 — P2 — Measure and reduce event fanout before scaling claims

**Classification: code-inspection recommendation; no large-cluster throughput test.** References: `internal/controller/kuik/imagealternative_controller.go:245`; `internal/controller/secretsyncer/syncer.go:423,441`; `internal/webhook/core/v1/pod_webhook.go:514,524,556`.

`selectedPods` lists all namespaces and Pods per resource reconciliation. The syncer enumerates every namespace for a routing-resource event, including status updates. The webhook rebuilds its complete index on every resource event, including status. Its buffered notification coalesces bursts, but not sustained updates separated by each rebuild. These are generally informer-cache reads, not necessarily API-server requests; their cost is local copying, allocation and repeated computation.

Benchmark resource count × namespaces × live Pods, including steady status churn. Add dependency-aware update predicates where status cannot affect that consumer, while preserving monitor demotion and configuration changes. Consider shared namespace-label snapshots and reverse indexes only where profiling shows a bottleneck. Avoid a cache invalidation refactor without tests for selector edits, namespace deletion, credential rotation and resource replacement.

## A05 — P2 — Separate reporting samples, durable truth and capacity budgets

**Classification: architecture/operational recommendation grounded in C08.** References: `internal/controller/kuik/imagemirror_copy.go:495`; `internal/status/imagemetrics/imagemetrics.go:100,120`; `internal/controller/kuik/imagemonitor_checks.go:106`.

Three state classes need different policies: correctness-bearing inventory must survive restart; a capped anomaly list is a sample; per-image process state and metrics consume resources. Increasing or decreasing `maxListSize` cannot solve all three. Preserve aggregates or explicitly report incomplete reconstruction while paced checks rebuild omitted observations.

Metric removal paths exist for departed images and deleted resources; this audit did not establish a separate metric leak. Active cardinality is nevertheless proportional to resources/images, independent of API list caps. Establish supported capacity with churn benchmarks, then document memory/series budgets and actionable saturation signals. Do not truncate repository ownership to meet a display limit.

## A06 — P2 — Test deployment and release transitions as contracts

**Classification: architecture recommendation grounded in D01/D03–D07/B01.** References: `Taskfile.yaml:439`; `.github/workflows/build.yaml:33,85`; `.github/workflows/website.yaml:28`; `test/acceptance/guard.go:436`.

Rendering, installing, becoming ready and publishing are different success conditions. Keep render fixtures for values, validate final object names, test an upgrade from prior installed CRDs, and gate readiness on usable state. Promote charts/indexes only after their required images exist. Keep PR validation concurrency separate from publication concurrency.

Acceptance cleanup should encode the reverse resource dependency graph, including workload termination before mirror finalization while retaining management credentials until registry cleanup completes. Exercise this against isolated Kind. The acceptance program intentionally performs actions without assertions; that design is not itself a bug, but its guard and cleanup state machine need their own tests.

## Preserve deliberate boundaries

Do not rewrite frozen specifications, remove the kubebuilder layout, hand-edit generated manifests, or replace the package structure merely to lower file sizes. Provider authentication and the drift-policy-default question are explicitly deferred in `notes/0010-spec-as-is-until-the-alpha-runs.md` and `docs/v3/open-questions.md`. They were not counted as missing implementations. Contract changes above need a maintained decision and regression coverage; mechanical cleanup can follow once behavior is settled.

Detailed failures are in [correctness](./01-bugs-and-correctness.md) and [security/deployment](./02-security-deployment-and-ci.md). These recommendations do not add duplicate defect counts.

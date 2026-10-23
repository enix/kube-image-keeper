# Project audit — 2026-10-23

The existing test suite passes, but targeted reproductions expose credential-boundary failures, destructive mirror interactions and restart defects. Address the seven P1 findings before broader rollout. No P0 was established. This is a deep audit of the current project, not a guarantee that no other defect exists.

## Audited baseline and deliverables

| Item | Recorded value |
| --- | --- |
| Repository | `enix/kube-image-keeper` |
| Revision | `bdbe1013584dd6c26bca0c4a22f6664b96dfe2c4` |
| Branch | `main` |
| Auditor | GPT-6 Astra (effort Ultra) |
| Audit date | 2026-10-23, Europe/Paris |
| Initial worktree | Clean; no pre-existing tracked or untracked changes |
| Durable changes | Exactly these six Markdown reports under `docs/audit/2026-10-23/` |
| Application changes | None; no fix, commit, push, deployment or publication |

Repository instructions, the v3 specification/status/walkthroughs/open questions, contribution rules, relevant area conventions and the notes index informed the review. Three parallel reviewers covered controllers/lifecycle/API, routing/registry/credentials, and deployment/CI/tooling. Findings were consolidated and challenged against implementation, tests and positive controls. Temporary reproductions used synthetic credentials, local registries and isolated envtest; they were excluded from baseline coverage and removed after evidence was recorded.

| Report | Contents |
| --- | --- |
| [Bugs and correctness](./01-bugs-and-correctness.md) | State, copying, cleanup, concurrency, credential projection and tooling defects |
| [Security, deployment and CI](./02-security-deployment-and-ci.md) | Authorization, transport, chart/upgrade/readiness, publication and operational risks |
| [Tests and coverage](./03-tests-and-coverage.md) | Commands, results, weighted coverage, controls, missing scenarios and regression plan |
| [Architecture and maintainability](./04-architecture-and-maintainability.md) | Component map, contract repairs, measured cache cost and scaling recommendations |
| [Documentation cleanup](./05-documentation-cleanup.md) | Concise source/comment/example corrections, with historical and frozen-document boundaries |

## Priorities and confidence

| Priority | Meaning in this audit |
| --- | --- |
| P0 | Immediate stop/containment: demonstrated broad compromise, catastrophic loss or equivalent critical impact. None established. |
| P1 | Fix before broader rollout: credential exposure, deletion of active data, persistent lifecycle loss or materially wrong image content under supported conditions. |
| P2 | Schedule a targeted repair: bounded correctness, availability, deployment, observability or tooling failure; consequential risk requiring validation. |
| P3 | Lower-impact input consistency, development experience, documentation or maintenance cleanup. |

Priority describes impact and urgency; it does not substitute for evidence. Every finding states its trigger and proof scope:

- **Reproduced defect:** a bounded execution exercised the relevant production path and observed the stated deviation. A computation-level reproduction is identified as such; it is not presented as a live kubelet or cluster exploit.
- **Code-inspection conclusion:** a traced path establishes the behavior or a documented external contract establishes its consequence; the complete runtime scenario was not executed.
- **Risk to confirm:** a configuration/scanner signal is real, but exposure or exploitability depends on an unverified deployment condition.
- **Architecture recommendation:** a proposed structural repair or capacity improvement, tied to concrete findings or measurement, not an additional independently demonstrated outage.

IDs are stable across reports. C04 was rejected after a positive control and intentionally remains unused. Architecture and documentation references to a defect do not count it again.

## Highest-priority findings

| ID | Finding and concrete consequence | Evidence |
| --- | --- | --- |
| R01 | A forged standing Pod record can cause an OnFailure resource's credential to be provisioned into an excluded namespace. | Actual `Needed → Build` computation; admission/syncer path inspected; no live-cluster exploitation. |
| R07 | `Insecure:false` can still send authenticated HTTP to private-IP/loopback registries without a TLS entry. | Authenticated local HEAD reproduced; pinned dependency confirms private-IP behavior. This exception is documented, but weakens the secure-endpoint boundary. |
| C01 | Restart with no remaining rewritten Pod leaves previously injected credentials indefinitely. | Envtest restart and persisted Secret; partial-restart positive control. |
| C02 | Destination tag repair under Ignore/Warn can replace snapshot D with current upstream D2. | Real local-registry digest change; unchanged-upstream control. |
| C03 | Mirror copying chooses an Always alternative before a healthy origin and can prevent Sync convergence. | Local registries with distinct digests; origin-first contract inspected. |
| C05 | Two mirrors sharing a destination delete each other's live tags. | Live destination tag becomes 404 while the other mirror's Pod remains. |
| C06 | Sweep retires inventory during an upload, leaving a committed tag undiscoverable after restart. | Blocked manifest PUT, inventory removal, restart, and orphan surviving later sweeps. |

R01 and R07 are detailed in [security](./02-security-deployment-and-ci.md); the C findings are in [correctness](./01-bugs-and-correctness.md).

Other actionable findings are C07/C08 (resource incarnation and truncated restart reporting), R02/R03/R05 (credential and verdict equivalence), R04 (cancellation), R06 (PEM ordering), B01/B02 (acceptance cleanup and incremental docs), and D01–D07 (Helm zeros/names, approval classification, CRD upgrades, publication ordering/concurrency and readiness). DEP01 and OPS01 separately record dependency exposure and metrics-network risks to confirm.

## Verification results

| Verification | Result |
| --- | --- |
| Full Go suite with Kubernetes 1.37.0 envtest | Pass |
| Full Go race run | Pass; no reported memory race in executed paths |
| Short suite | Pass; envtest suites intentionally skip |
| Full statement coverage | **63.6%**, 4,218/6,628 statements |
| Owned internal packages, excluding vendored keyring and registry test helper | **92.2%**, 4,091/4,438 statements |
| Go vet including E2E build tag, formatting and module tidiness | Pass |
| Pinned generation and chart-copy consistency | Pass; no tracked drift in the temporary clone |
| Helm lint, values checker and both existing hook suites | Pass; supplemental probes nevertheless demonstrate D01–D03 |
| Acceptance fixture validation | Pass for 14 files, offline |
| Static website build | Pass; baseline 29 pages, 35 including the audit, internal links valid |
| Audit artifact validation | Six Markdown reports pass repository lint; references, IDs and numeric totals cross-checked |
| Go vulnerability scan | Five symbol-level advisories in one module; exposure qualified in DEP01 |
| npm audit | Three moderate package entries for one underlying build-time advisory |

The passing suites omit the counterexamples demonstrated by the audit. High statement coverage did not prevent cross-component identity, authorization and recovery gaps. Full commands, coverage interpretation, artifact validation and environmental setup limits are in [tests and coverage](./03-tests-and-coverage.md).

## Domain coverage and remaining uncertainty

| Domain | Review and evidence | Remaining limit |
| --- | --- | --- |
| Business rules, parsing and configuration | Source/spec/test comparison; routing, image paths, strict config, defaults and reload reviewed; Helm matrix executed | No sustained fuzz campaign or exhaustive input proof |
| State, rotation, deletion and restart | Controller/syncer reproductions, Secret/TLS rotation tests, sweep/finalizer tracing | External registry dialects and real garbage collection untested |
| Concurrency, cancellation, startup/shutdown | Full race run, controlled copy interleaving, stale completion, canceled admission and startup tracing | No large-cluster rollout or long-running soak |
| Authentication, authorization and secrets | Keyring/projection review, synthetic credential proofs, generated roles, restricted identity tests and rendered binding review | No real credentials, cloud exchanges or production authorization review |
| Performance and resource consumption | Bounded actual cache benchmark; fanout, locking and cardinality inspection | No production capacity number or sustained admission load |
| Public API and compatibility | CRD markers/CEL/defaults, generated equivalence, prior CRD comparison and Helm upgrade contract | No live previous-version upgrade or minimum Kubernetes 1.30 runtime matrix |
| Deployment, containers, CI and publication | Helm lint/rendering, Dockerfile and workflow dependency/permission review, inert guard probes | Docker socket unavailable; no image build/scan or publication failure exercise |
| Observability | Status, aggregate/sample semantics, metrics cleanup/labels, event paths and readiness inspected | No deployed Prometheus rules, dashboard behavior, alert delivery or network policy validation |
| Tests and architecture | Baseline/targeted tests, assertions and inter-package contracts reviewed; component/flow map prepared | Kind E2E and actual kubelet pulls not run |
| Documentation and examples | Current docs read with code; offline fixtures, website build and incremental-sync reproduction | External links were consulted when relevant, not exhaustively crawled; historical changelogs preserved |

`actionlint`, `shellcheck`, `kubeconform` and the custom `golangci-lint` binary were unavailable, so no dedicated result is claimed for them. Envtest exercised API behavior and some restricted identities, but has no kubelet or production deployment network. Provider authentication and the existing drift-policy-default question remain declared roadmap decisions, not newly asserted defects. Frozen specifications and historical notes were not edited.

## Remediation order

1. **Close credential boundaries:** R01, C01 and R07. Add side-effect assertions through admission/syncer and registry transport. Coordinate R01 with the selected-entry contract in R02/R03.
2. **Protect mirror data and recovery:** C05/C06 ownership/inventory first, then C02/C03 content semantics. Use interrupted uploads, restart and multiple-resource controls before changing cleanup.
3. **Repair asynchronous and reporting semantics:** C07/C08, R04/R05 and D07. Separate resource incarnation, operation context and initial readiness.
4. **Make delivery and rollback predictable:** D01–D06 and B01. Validate rendered objects and prior-version upgrades; gate public promotion on dependencies. Update known vulnerable dependencies and validate release artifacts under DEP01.
5. **Confirm operational boundaries and capacity:** OPS01, RA01 and architecture recommendations. Measure realistic cardinality/fanout, then set supported budgets.
6. **Refresh current guidance with the repairs:** documentation tables, R06 and B02. Do not weaken normative guarantees merely to describe a bug, rewrite historical changelogs, or silently change deferred specification decisions.

Each detailed finding names the required regression. The next phase should implement those repairs; this audit intentionally stops at a reviewable diagnosis and validation plan.

# Tests and coverage

The existing full suite and race run pass. Targeted probes nevertheless demonstrate missing security and lifecycle scenarios. A passing baseline is not evidence that those scenarios are covered.

## Execution record

The audited source was `bdbe1013584dd6c26bca0c4a22f6664b96dfe2c4`. Go caches, envtest binaries, reproductions, coverage files and the website checkout were isolated under `/tmp`. No temporary regression was included in baseline coverage.

The execution environment used Go `1.26.8-X:nodwarf5` on linux/amd64, Kubernetes envtest `1.37.0`, Node `22.23.1`, Task `3.53.1` and Helm `4.2.2`. `GOMODCACHE`, `GOCACHE` and `KUBEBUILDER_ASSETS` pointed to the isolated audit directory. Temporary overlays were supplied only to targeted `go test -overlay ... -ginkgo.focus ... -count=1` commands, never to the baseline runs below.

| Check | Result and scope |
| --- | --- |
| `go test -short -coverprofile=... ./...` | Pass; 31 package results, including intentionally skipped envtest suites. Global statement coverage 28.7%. |
| `go test -p 2 -coverprofile=... ./...` with Kubernetes 1.37.0 envtest | Pass; 31 package results. Global statement coverage 63.6%. |
| `go test -p 2 -race ./...` with the same envtest assets | Pass; no data race reported in the exercised paths. |
| `go vet -p 2 -tags e2e ./...` | Pass, including compilation/static checking of E2E code. This does not execute E2E. |
| `gofmt -l` on tracked Go files | Pass; no paths returned. |
| `go mod tidy -diff` | Pass; no module-file drift. |
| Pinned controller-gen v0.22.0 manifests and DeepCopy generation | Pass in a temporary clone; no tracked diff after Taskfile-equivalent chart copies. |
| Helm v4.2.2 lint | Pass, one chart, zero failures. Additional renders expose D01/D03 despite lint success. |
| Helm configuration matrix | Default, root/process replica overrides, and restricted Secret access with zero/two extra namespaces render. Secret-reader binding scope matches the inputs; zero replicas do not. |
| `go run ./hack/valuescheck helm/kube-image-keeper/values.yaml` | Pass. Checks key presence, not all value semantics. |
| Both existing outbound/implementer hook suites | Pass. New inert command probes expose D02. |
| `go run ./test/acceptance validate` | Pass for all 14 fixture files, offline. This proves decoding/local guards, not successful admission or execution. |
| Website `npm ci --ignore-scripts`, then `ASTRO_TELEMETRY_DISABLED=1 npm run build` | Pass: baseline 29 pages; 35 pages with these six audit reports. Internal link validation passes. No preview or site was published. |
| Markdown lint with repository rules, selected audit files only | Pass: all six reports, markdownlint-cli2 v0.24.0. File/line existence, cross-report IDs, priorities and coverage totals were also checked. |
| `govulncheck v1.9.0 ./...` | Exit 3: five symbol-level advisories in one module, plus one imported-package advisory not reported as called. See DEP01. |
| Website `npm audit --json` | Three moderate package entries stemming from one underlying advisory. Build-time reachability qualification is in DEP01. |
| Source search for native `Fuzz*` / `Benchmark*` functions | None found. A bounded audit-only cache benchmark was run separately. |

The initial test attempts failed before compilation because the user's module cache was read-only and DNS/network access was restricted. Public dependencies and isolated test binaries were then downloaded to temporary caches with tool approval. Loopback listeners were allowed for local fixtures. These setup failures are not test failures in the repository.

The website's first build was blocked by the telemetry config directory and then the temporary clone's missing maintenance ref. Disabling telemetry and adding the already-local `origin/2.3.x` ref resolved both. Its archived documentation snapshot was `4987cae199a64277578510127ea5966004bd9c68`. The successful build emitted an Astro configuration deprecation warning recorded in the documentation cleanup.

## Coverage interpretation

The full profile covers **4,218 of 6,628 statements: 63.6%**. Owned `internal/` packages, excluding vendored credential-provider code and `registrytest`, cover **4,091 of 4,438 statements: 92.2%**. These are weighted statement totals, not averages of package percentages.

| Package / area | Full coverage | Interpretation |
| --- | --- | --- |
| `internal/controller/kuik` | 91.9% | Broad behavior coverage, but multi-mirror ownership, restart inventory and stale-completion scenarios are missing. |
| `internal/controller/secretsyncer` | 86.6% | Restart tests miss the case where no rewritten Pod survives. |
| `internal/webhook/core/v1` | 91.2% | High coverage misses request cancellation, initial routing readiness and hostile records. |
| `internal/auth/pullsecret` | 91.7% | Isolated expectations do not prove equivalence with the actual winning candidate. |
| `internal/registry/pacing` | 98.6% | Reference-only shared verdicts are tested without differing authentication contexts. |
| `internal/registry` / `tlstrust` | 92.0% / 90.1% | Custom TLS paths are well exercised; the unconfigured-host HTTP path is a missing boundary. |
| `internal/config` | 90.4% | Strict parsing, defaults and reload behavior are exercised; startup consumer coordination is separate. |
| `internal/routing/podrecord` | 55.9% | Record parsing is not an authorization test. |
| `cmd` | 10.8% | Process wiring/readiness requires integration coverage beyond helper tests. |
| `api/kuik/v1alpha1` | 3.4% | Dominated by generated Go methods; CEL validation executes in the API server and is not measured as Go statement coverage. |
| `test/acceptance` | 0.0% | No package tests; fixture validation was a separate command without coverage instrumentation. |
| Vendored keyring, `registrytest`, `test/utils` | 0.0% in their package results | Default package-local instrumentation does not credit their execution as dependencies of other packages. Do not equate this with never being exercised. |

The short run intentionally skips API, auth, controller, syncer, TLS-trust and webhook envtest suites. See, for example, `internal/controller/kuik/suite_test.go:34`, `api/kuik/v1alpha1/suite_test.go:40` and `internal/webhook/core/v1/webhook_suite_test.go:45`. Keep it as a smoke test, not the sole acceptance gate.

## Reproduction evidence and controls

Temporary Go overlays and diagnostic modules exercised production functions without changing the original tree. Synthetic credentials and bounded local registries were used. The following table distinguishes these failures from the passing baseline:

| Findings | Targeted evidence | Control / limit |
| --- | --- | --- |
| R01 | Excluded-namespace forged record produces credential bytes through `Needed → Build`. | No record and `Always` policy produce no credential. Actual controller application was inspected, not exploited on a cluster. |
| R02/R03 | Probe credential lookup and generated Secret disagree for child paths/nested targets. | The probe resolves the synthetic credential; output computation identifies the mismatch. Runtime kubelet pulls were not executed. |
| R04 | Already-canceled admission still makes three delayed manifest requests and waits about 166 ms. | Three candidates, 50 ms per response; no load attack. |
| R05 | Ring B receives A's Unauthorized response before B executes any read. | B's direct checker succeeds; later windows do run B, disproving permanent starvation. |
| R06 | Invalid PEM before a valid certificate is accepted; reversing order rejects it. | Both inputs contain the same valid certificate. No trust bypass claimed. |
| R07 | A secure-flag endpoint sends authenticated HTTP to a local fixture. | Adding a TLS host entry rejects HTTP; no real credential used. |
| C01 | Fresh syncer leaves a prior injected credential intact when no rewritten Pod survives. | Existing partial-restart coverage retains a live Pod and does not exercise this state. |
| C02/C03 | Destination repair changes the old digest under Ignore; Always alternative precedes healthy origin during copy. | Local registry digests distinguish expected and actual bytes; existing simpler restoration cases still pass. |
| C05/C06 | One mirror deletes another's live tag; an in-flight upload loses its persisted repository inventory. | Separate CRs and blocked upload make the ordering explicit. |
| C07/C08 | Old name-only completion reaches a replacement resource; restart loses a truncated failure count. | C07 deterministically injects a stale callback, rather than relying on network timing. |
| D01/D03 | Successful Helm renders contain wrong zeros or overlong Service names. | Nonzero values and ordinary release names render correctly. |
| D02 | Inert hook input yields no approval decision for write-equivalent forms. | Plain kubectl write and main push still ask. No command represented by the input runs. |
| B02 | Removing a shadowed source removes the generated overlay page. | Source-change events preserve overlay precedence; a full rebuild restores it. |
| RA01 | Actual cache-store microbenchmark grows with live cache size. | At most 10,000 entries and a 100 ms benchmark budget; separate from coverage. |

**Discarded suspicion C04:** an actual manager/envtest deletion experiment showed that deletion of this CR increments generation, so the update predicate does enqueue finalization. The proposed deletion-watch defect was removed. Likewise, current GitHub documentation supports the Dependabot permission pattern used here; no such CI defect is reported.

## Prioritized regression plan

1. **Security and destructive effects:** real admission-to-syncer tests for forged CREATE/UPDATE records (R01), secure private-IP registry requests (R07), disjoint mirrors sharing a destination (C05), and restart credential withdrawal (C01). Assert the absence of credential/tag side effects, not merely an error return.
2. **Crash and lifecycle boundaries:** pause an upload after inventory persistence, sweep and restart (C06); restore tags with offline/moved upstream under Ignore (C02); complete a check from an old resource UID (C07); restore truncated status before rechecks (C08).
3. **Cross-component contracts:** route through actual candidate generation, probe, record and secret materialization (R02/R03); copy source ordering with Always alternatives (C03); different credential contexts sharing a reference (R05); routing snapshot readiness (D07).
4. **Cancellation and tooling:** canceled/shared probe waiters (R04), cleanup with live copied Pods (B01), Helm zero/name matrix (D01/D03), previous-version CRD upgrades (D04), publication dependency/concurrency fixtures (D05/D06), and the hook command corpus (D02).
5. **Fuzzing and scale:** bounded property/fuzz tests for reference parsing, tag mapping, globs, annotations, PEM and config decoding. Assert deterministic round trips, no panic, selector isolation and cluster-tag ownership. Add benchmarks for high-cardinality admissions, CR snapshot rebuilds, many pods/resources and metric churn. Fuzzing supplements the required Ginkgo behavior specs.

`internal/auth/pullsecret/desired_test.go:181` is a concrete example of an assertion sharing the implementation's assumption: it endorses most-specific output matching without deriving that output from routing. Keep genuine tests of that documented behavior, but first resolve the contradictory end-to-end contract in R03; do not silently weaken expectations to make a fix pass.

## Unexecuted checks

No Kind E2E run, previous-version cluster upgrade, Kubernetes 1.30 runtime matrix, cloud-provider identity flow, production networking, live Prometheus alert evaluation, container build/image scan, or release/publish operation was performed. Docker's socket was inaccessible in the audit environment. `actionlint`, `shellcheck`, `kubeconform` and the repository's custom `golangci-lint` binary were unavailable; dedicated runs of those tools were not performed. No sustained load or fuzz campaign was run.

Envtest has no kubelet, scheduler or registry garbage collector. Existing suites do exercise generated roles and restricted identities, including the syncer's dedicated user. They do not establish the deployed chart's bindings, pull success, multi-node rollout behavior, registry-specific GC, or production network isolation. The race detector checks executed memory accesses, not logical ownership or crash-ordering invariants.

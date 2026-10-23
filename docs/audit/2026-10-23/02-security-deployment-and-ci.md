# Security, deployment and CI

Priorities and evidence classes are defined in [the audit summary](./README.md). References identify the audited revision, not proposed fixes. No production system or real credential was used.

## R01 — P1 — Forged Pod annotations can provision credentials outside the authorized namespace

**Evidence: reproduced production computation; admission and controller integration traced by inspection.**

**References:** `internal/auth/pullsecret/pullsecret.go:163,180,185`; `internal/routing/podrecord/podrecord.go:109`; `internal/webhook/core/v1/pod_webhook.go:281`; `internal/controller/secretsyncer/syncer.go:253,277,299,501`.

**Trigger:** an `OnFailure` routing resource injects a credential and restricts its namespaces. A tenant creates a Pod in an excluded namespace with a standing `kuik.enix.io/rewrites` record naming that resource. Its `rewrittenTo` equals the container image under the resource's alternative or destination. A Pending Pod is sufficient.

**Expected / observed:** namespace restrictions should prevent distribution of that credential. `Needed` checks the namespace only under `Always`; under `OnFailure`, it trusts the user-writable record. The generated Secret meets the admission policy's type/name/label requirements. The webhook retains a standing record before candidate selection and its selector checks. It may omit the pull-secret reference, but that does not prevent the syncer from creating the Secret.

**Impact:** the tenant can read or mount the predictable injected Secret. Pod creation rights can expose Secrets in that namespace, as documented by [Kubernetes](https://kubernetes.io/docs/concepts/configuration/secret/). This crosses a credential authorization boundary without requiring permission to edit the cluster-scoped resource.

**Proof:** production `FromImageAlternative → Needed → Build`, an excluded namespace, and a synthetic source credential produced a Secret containing the synthetic password with no failures. Removing the annotation yielded no needed credential. Changing the policy to `Always` also yielded none because its namespace check worked. No live-cluster exploitation was attempted.

**Remediation / regression:** stop using editable attribution as authorization. Check provisioning scope for both policies and authenticate any historical authorization that must survive selector edits. Bind trusted records to resource UID, namespace and workload identity, while preserving legitimate reinvocation. Test both CR kinds, forged CREATE and subsequent annotation UPDATE, genuine rewrites, and actual Secret creation under the admission policy. Scope checks alone do not establish record provenance.

## R07 — P1 — Secure registry endpoints can transmit credentials over HTTP

**Evidence: reproduced on loopback; private-IP applicability verified in the pinned dependency.**

**References:** `internal/registry/registry.go:53`; `internal/registry/tls.go:185,193,223`; `internal/registry/check.go:49`; `docs/configuration.md:211`; `docs/crds.md:316`.

**Trigger:** `Insecure: false`, a localhost/loopback or RFC1918 literal registry, no effective global `tls` entry, and a plain-HTTP endpoint answering the dependency's fallback.

**Expected / observed:** a secure endpoint should require explicit authorization before sending credentials in cleartext. The pinned dependency permits HTTP fallback for these hosts independently of the `insecure` flag. The TLS router returns the base transport for unconfigured hosts before reaching its HTTP rejection. Registry credentials, including management credentials, can follow the fallback. This exception is explicitly documented in `docs/configuration.md:211–213`; it is a demonstrated security design problem, not an undisclosed implementation deviation. It also conflicts with the secure-entry credential separation described in `docs/crds.md:316`.

**Proof:** a local HTTP registry requiring synthetic Basic authentication accepted `Check(Endpoint{Insecure:false, Auth:...})`; the fixture recorded an authenticated manifest HEAD. Adding a `TLSHost{InsecureSkipVerify:true}` entry caused HTTP rejection with no manifest request. The latter is a diagnostic control, not a recommended workaround.

**Impact:** the endpoint's HTTPS contract is broken and credentials can cross the network in cleartext. RFC1918 behavior is explicit in [Registry.Scheme](https://github.com/google/go-containerregistry/blob/v0.22.1/pkg/name/registry.go) and the [transport fallback](https://github.com/google/go-containerregistry/blob/v0.22.1/pkg/v1/remote/transport/ping.go). Plain HTTP and skipping certificate verification are distinct policies in [containerd's registry configuration](https://github.com/containerd/containerd/blob/main/docs/hosts.md).

**Remediation / regression:** enforce HTTPS for every secure endpoint independently of custom TLS configuration. Preserve explicit HTTP authorization and host-specific redirect/token-service rules. Test a synthetic private IP mapped to loopback: no authenticated HTTP under `insecure=false`, successful explicit HTTP as the control. Cover Check, Copy and tag operations. Existing localhost fixtures must opt into HTTP deliberately.

## D01 — P2 — Helm ignores zero replicas and zero-valued overrides

**Evidence: reproduced rendering defect.** **References:** `helm/kube-image-keeper/templates/deployments.yaml:17,35`; `docs/configuration.md:96`.

`helm template audit helm/kube-image-keeper --set reconciler.replicas=0` renders **two** replicas. Setting root `replicas=0` omits the field for the reconciler and syncer, leaving Kubernetes to default it to one. `--set verbosity=2 --set webhook.verbosity=0` renders webhook verbosity two. Nonzero replica overrides work.

The template uses `default` and `with`, which treat zero as empty. Operators cannot pause copying/deletion with the documented value. Distinguish null/unset from zero and render explicit replicas. Regression cases must cover root and per-process zero/one/two and zero verbosity; default Helm lint passes despite this defect.

## D02 — P2 — Outbound approval guards miss equivalent command forms

**Evidence: reproduced hook decisions using inert JSON; no represented command was executed.** **References:** `.claude/hooks/confirm-outbound-actions.sh:70,143,145,149`; `.claude/hooks/implementer-guard.sh:25`.

The documented approval boundary misses absolute executable paths, some publication verbs, Go global options, and maintenance publication branch suffixes:

| Inert command input | Observed decision |
| --- | --- |
| `kubectl apply -f deployment.yaml` | Ask: positive control |
| `/usr/bin/kubectl apply -f deployment.yaml` | Exit zero, no decision |
| `helm push chart.tgz oci://registry.example/charts` | Exit zero, no decision |
| `go -C test/acceptance run . cleanup` | Exit zero, no decision |
| `git push origin 2.3.x-stable` in an isolated repository with a stubbed PR query | Exit zero, no decision |
| `git push origin main` in the same fixture | Ask: positive control |

**Impact:** sessions relying on this hook can lose its promised confirmation for cluster writes or publication. Other permission layers may still block them; this is not evidence of a sandbox escape.

**Remediation / regression:** normalize executable paths/global options with a real parser, or ask for unclassified write-capable forms. Share classification logic with the implementer guard. Add these cases, release branch variants and quoted executables alongside safe-read controls. Both existing hook suites pass and miss these cases.

## D03 — P2 — Legal Helm release names generate illegal Service names

**Evidence: reproduced render; rejection follows Kubernetes naming constraints.** **References:** `helm/kube-image-keeper/templates/_helpers.tpl:13`; `helm/kube-image-keeper/templates/metrics-services.yaml:10`; `helm/kube-image-keeper/templates/webhook.yaml:40`.

A valid 53-character Helm release name renders Service names of **71, 79, 82 and 85 characters**. The helper truncates the base to 63 before suffixes are appended. The expected limit applies to the final name: [Kubernetes DNS-label names](https://kubernetes.io/docs/concepts/overview/working-with-objects/names/) allow 63 characters. Installation can therefore fail after a successful template/lint run. A short release name is the passing control.

Bound final names while preserving uniqueness. Test maximum release names, both override fields and truncation collisions, using Kubernetes schema/API validation in addition to Helm lint.

## D04 — P2 — The documented upgrade does not upgrade installed CRDs

**Evidence: code inspection plus the Helm lifecycle contract; no cluster upgrade executed.** **References:** `Taskfile.yaml:439`; `docs/installation.md:14`; `api/kuik/v1alpha1/imagemirror_types.go:34`; `helm/kube-image-keeper/crds/kuik.enix.io_imagemirrors.yaml:159`.

After an earlier v3 installation, `helm upgrade --install` and `task deploy` update workloads but leave existing CRDs unchanged. Regenerating chart files locally does not change this. [Helm explicitly does not upgrade CRDs in the chart's `crds/` directory](https://helm.sh/docs/chart_best_practices/custom_resource_definitions/).

The repository's `v3.0.0-alpha.1` CRD lacks the current destination-path immutability rule. An upgraded controller can therefore run against a schema that still permits a destination edit which a fresh installation rejects. Schema additions can similarly be pruned or refused.

Provide a versioned, non-destructive CRD upgrade step before workload promotion, or a reviewed CRD lifecycle mechanism. Regression: upgrade from earlier CRDs through the documented procedure, preserve custom resources, and verify new fields and immutability. Deleting/recreating CRDs is not an acceptable migration strategy.

## D05 — P2 — Pull-request website builds can cancel production publication

**Evidence: workflow inspection and documented concurrency semantics.** **References:** `.github/workflows/website.yaml:18,28,67`.

Push and pull-request runs share `group: pages` with `cancel-in-progress: true`. A PR run arriving during a publication cancels the push run, then skips its own deploy job. Merged documentation stays unpublished until another trigger. [GitHub concurrency](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency) applies cancellation to runs in the same group.

Separate per-PR validation groups from the publication group. Test event-to-group mapping and cancellation using a safe CI fixture. No live workflow was dispatched during this audit.

## D06 — P2 — A chart can be published before its image build succeeds

**Evidence: workflow dependency inspection; publication failure was not executed.** **References:** `.github/workflows/build.yaml:33,85,110,126`; `.github/workflows/release.yaml:113`.

`chart` and `containers` run independently. The chart job publishes the versioned chart and triggers index publication even if image publication is still running or eventually fails. A later skipped release job cannot undo those writes. A user can install an offered chart whose image does not exist and encounter `ImagePullBackOff`.

Separate build/package from promotion. Gate chart/index publication on successful required image publication and update public aliases at the final successful promotion step. Regression: force the image job to fail in a sandbox release fixture and assert no chart-index promotion. Do not promise atomic transactions across independent registries.

## D07 — P2 — Webhook readiness does not cover its first routing snapshot

**Evidence: code inspection, independently cross-checked across startup and routing.** **References:** `cmd/main.go:347,359,382`; `cmd/registrytls.go:56`; `internal/webhook/core/v1/pod_webhook.go:146,524,536,568`.

The constructor installs an empty routing index. A non-leader runnable dynamically starts the three CR informers and eventually publishes the first complete index. Readiness checks the cluster ID and TLS trust, but not that publication. If CR lists are slow or forbidden while ConfigMap/Secret reads succeed, the webhook can be ready with an empty index. Pod CREATE is then admitted unchanged and will not be revisited. A rollout can retire populated replicas while replacements are in this state.

Expose readiness only after the first complete routing snapshot is installed. Keep fail-open admission semantics. Regression: block initial CR lists while allowing ID/TLS reads; readiness must fail until routing becomes usable. The missing check is the routing snapshot, not simply the absence of a generic webhook StartedChecker.

## DEP01 — P2 — Known HTTP/2 dependency advisories require an update and exposure check

**Evidence: scanner result and primary advisory inspection; project exploitability remains a risk to confirm.** **References:** `go.mod:16`; `cmd/main.go:89,125`; `internal/registry/tls.go:188`; `Dockerfile:2`.

`govulncheck v1.9.0 ./...` reports five symbol-level findings in `golang.org/x/net v0.59.0`, fixed in `v0.60.0`:

| Advisory | Relevant consequence and qualification |
| --- | --- |
| [GO-2026-6617](https://pkg.go.dev/vuln/GO-2026-6617) | HTTP/2 server HPACK race/crash. Server HTTP/2 is disabled by default here. |
| [GO-2026-6612](https://pkg.go.dev/vuln/GO-2026-6612) | HTTP/2 server flow-control refund error; same default-server qualification. |
| [GO-2026-6611](https://pkg.go.dev/vuln/GO-2026-6611) | A malicious HTTP/2 peer can consume client or server CPU. Outbound clients remain relevant. |
| [GO-2026-6610](https://pkg.go.dev/vuln/GO-2026-6610) | Malformed framing headers can contribute to response smuggling in a reverse proxy; that deployment shape was not established here. |
| [GO-2026-6603](https://pkg.go.dev/vuln/GO-2026-6603) | HTTP/2 server Trailer allocation amplification; server HTTP/2 is disabled by default. |

One further imported-package advisory was not reported as called. Scanner traces include broad interface reachability and test helpers; they are not five demonstrated attacks on kuik. Update the module and rebuild with a patched Go toolchain, then rescan the actual release binary. The audit used Go `1.26.8-X:nodwarf5`; the Dockerfile uses a floating Go 1.27 image whose resolved patch was not inspected. The primary advisories also identify patched Go branches.

The website's npm audit reports **three moderate package entries for one underlying PostCSS advisory**, [GHSA-rj75-hqrm-r3gf](https://github.com/postcss/postcss-selector-parser/security/advisories/GHSA-rj75-hqrm-r3gf). They form a transitive dependency chain. This project uses the parser at static build time on repository content; no untrusted runtime CSS endpoint was found. Treat that refresh as P3 maintenance, not a demonstrated public-site denial of service.

## OPS01 — P2 — Confirm the metrics network trust boundary

**Evidence: configuration inspection; deployment exposure remains a risk to confirm.** **References:** `helm/kube-image-keeper/templates/deployments.yaml:80`; `helm/kube-image-keeper/templates/metrics-services.yaml:10`; `internal/status/imagemetrics/imagemetrics.go:68`.

The chart hard-codes unauthenticated metrics and provides ClusterIP Services without a chart NetworkPolicy. Image-reference and resource labels disclose internal inventory to any workload whose network can reach them. No production network policy was inspected, so cross-tenant access is not asserted for a particular installation.

Document the trust assumption and support authenticated scraping or Prometheus-only network isolation. Verify allowed scraping and rejected unrelated-namespace access on isolated Kind. This is inventory exposure, not credential disclosure.

## Related findings and boundaries

Credential retention after restart is **C01** in [correctness](./01-bugs-and-correctness.md). Mirror ownership and inventory races are **C05/C06** there. Tests, generated-file checks and unexecuted runtime checks are recorded in [verification](./03-tests-and-coverage.md). Deferred cloud providers and a v2 migration plan were treated as declared roadmap items, not new vulnerabilities.

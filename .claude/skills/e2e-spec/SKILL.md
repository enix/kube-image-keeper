---
name: e2e-spec
description: Use when adding, changing or fixing e2e specs under test/e2e/ in kuik (the Kind suite run by task test-e2e), or when deciding what a PR should check end to end.
argument-hint: "[behaviour to cover]"
allowed-tools: Bash(task test-outline *) Read Grep Glob Edit Write
---

# e2e specs

An e2e spec checks what only a real cluster can answer: the API server, admission, the
kubelet, the chart as deployed. Everything else belongs to a unit or envtest spec. Target:
`$ARGUMENTS`.

## When

e2e specs come last in a PR ([0012](../../../notes/0012-e2e-specs-last-in-a-pr.md)): once the
PR is otherwise ready (automated review addressed, unit and envtest specs green, the feature
believed to work), one final `test` commit adds them. A failure is fixed by `fixup!`
commits, autosquashed once like review fixes. This is the one exception to "a `test` commit
before each `feat` commit" of [`tests.md`](../../rules/tests.md); unit and envtest specs keep
that order. The reason: a Kind run takes minutes.

On a PR, the suite runs only under the `e2e-ready` label
([0013](../../../notes/0013-e2e-ready-label.md)): once the PR is reviewed, its e2e specs are
pushed and its `fixup!` commits are autosquashed, ask the user to add it. The required `E2E`
check fails until then.

## Workflow

1. **Outline first.** **REQUIRED SUB-SKILL:** follow [`test-outline`](../test-outline/SKILL.md):
   the `It` / `Entry` strings are drafted as `PIt` / `PEntry`, shown with
   `task test-outline -- ./test/e2e/`, and reviewed before any body.
2. **One spec per rule**, never one per verb, kind or field
   ([`tests.md`](../../rules/tests.md)). Drop a case a unit or envtest spec already proves.
3. **Bodies**, with the patterns below, in the final `test` commit.
4. **Run** only through `task test-e2e`, on an isolated Kind cluster, never against a real
   cluster. The task creates `KIND_CLUSTER` when it is missing and deletes it afterwards only
   in that case. An existing Kind cluster of that name is reused as is and the suite installs
   and uninstalls kuik on it, so never point `KIND_CLUSTER` at a cluster used for anything
   else. Give the run a dedicated kubeconfig:

   ```sh
   KUBECONFIG=<tmp>/e2e-kubeconfig task test-e2e
   ```

   `kind create cluster` switches the current context of the kubeconfig it writes to, and
   the suite uninstalls the CRDs at the end: on a shared kubeconfig, a later `kubectl`
   could hit the wrong cluster. The e2e tasks are outbound for the confirm-outbound hook:
   the user approves each run.

## Patterns

| Need | Do |
| ---- | -- |
| An origin that is unavailable | A `.invalid` host (`kuik-e2e.invalid/...`): it fails fast, at DNS |
| A slow or blackholed candidate | A documentation address (`192.0.2.1`, TEST-NET-1, never routed): the connection hangs until the timeout |
| A registry that answers | `registry.k8s.io/pause:3.10`: HTTPS, non-root, fits the restricted Pod Security profile |
| The webhook has seen a new CR | It reads CRs through an informer: create the CR, then poll `kubectl create --dry-run=server -f <pod> -o jsonpath=...` until the rewrite shows. The dry run goes through admission and creates nothing |
| A rewrite happened | Assert the rewritten image and the `kuik.enix.io/rewrites` annotation (`kuik.enix.io/no-alternatives` when every candidate failed) |
| A counter moved | `kuik_routing_rewrites_total`, `kuik_routing_alternatives_exhausted_total` are per replica: sum them over every webhook pod, read one by one with `kubectl get --raw /api/v1/namespaces/<ns>/pods/<pod>:8080/proxy/metrics` |
| kuik left a pod untouched | Proves nothing alone. Order the specs so that a rewrite proves the webhook is called before any "untouched" spec |
| A config change | `kubectl patch` the ConfigMap, never `helm upgrade`, and allow up to 3m for the kubelet to sync it into the pods |

The suite needs internet access: there is no in-cluster registry and no containerd or Kind
configuration (a plain HTTP registry does not work: the webhook probes an original over
HTTPS even when it matches an `insecure: true` entry). cert-manager is pulled the same way.

The chart is deployed once per suite, and never a second time. On `main` it is still the
`BeforeAll` / `AfterAll` of the `Manager` container (see below). A spec in another top-level
container needs it moved to `BeforeSuite` / `AfterSuite`, not duplicated: Ginkgo shuffles
the top-level containers, so one container's `BeforeAll` does not cover the others. A top-level `AfterEach` dumps logs, events and pod descriptions on failure.

## Fixtures and helpers

> [!NOTE]
> Placeholder. The shared Kind fixtures and helpers (pod manifests, kuik annotations,
> summed webhook metrics, config changes) are being added to `test/e2e/`. This section
> lists them once they land. Until then, only what is below exists.

On `main` today:

- [`e2e_suite_test.go`](../../../test/e2e/e2e_suite_test.go): `BeforeSuite` builds the manager
  image, loads it into Kind and installs cert-manager unless present
  (`CERT_MANAGER_INSTALL_SKIP=true` skips it); `AfterSuite` removes cert-manager if the suite
  installed it.
- [`e2e_test.go`](../../../test/e2e/e2e_test.go): one `Ordered` container, `Manager`, whose
  `BeforeAll` installs the CRDs and deploys the chart (`make install`, `make deploy`) and
  whose `AfterAll` removes them. Its `AfterEach` dumps logs, events and pod descriptions on
  failure. Specs: the 3 Deployments are available, each process runs under its own
  ServiceAccount, the RBAC table of `docs/v3/architecture.md` (`canI`, over
  `kubectl auth can-i --as`), the webhook metrics endpoint (a `curl-metrics` pod,
  `getMetricsOutput`), the cert-manager Secret and the CA injection. The chart's names
  (namespace, selectors, Services) are constants at the top of the file.
- [`test/utils`](../../../test/utils/utils.go): `Run` (runs a command from the project
  directory), `LoadImageToKindClusterWithName`, `GetNonEmptyLines`, the cert-manager install
  helpers.

## Common mistakes

| Mistake | Fix |
| ------- | --- |
| e2e specs written with the first `test` commit | Add them in the last commit, once the PR is ready |
| Running the suite on the current kubeconfig, or on a real cluster | `KUBECONFIG=<tmp>/e2e-kubeconfig task test-e2e`, Kind only |
| Creating the pod right after the CR | Poll a server-side dry run until the webhook sees the CR |
| Reading the metrics of one webhook pod | Sum over every replica |
| An "untouched" spec with nothing proving the webhook ran | Order it after a spec that shows a rewrite |
| An in-cluster plain HTTP registry | `registry.k8s.io` for an answer, `.invalid` for a failure |
| `helm upgrade` to change the config | `kubectl patch` the ConfigMap, wait up to 3m |

---
name: preprod-smoke
description: Use when asked to smoke test a kuik v3 release on a pre-production or other shared cluster where kuik is already installed, for example "smoke test kuik 3.0.0-alpha.3 on preprod" or "validate the release on our cluster".
argument-hint: "<version>"
---

# kuik smoke test on a shared cluster

## Overview

Runs, for one kuik v3 release, the smoke suite of [`test/smoke/`](../../../test/smoke/) on a
cluster that runs real workloads, then reports the results. The tests stay simple: kuik
breaks nothing, routes when needed, ignores otherwise. Edge cases belong to the e2e suite, on
Kind.

The cluster is not a test cluster: every rule below protects the workloads already on it.

## Hard rules

- **Existing workloads**: never touch, restart or roll out an existing workload. If a test
  ever needs one, name that specific workload ahead and wait for the user's explicit
  approval.
- **The suite only**: the cluster is reached through the 3 tasks of the
  [Reference](#reference), one per Bash call. `smoke-check` only reads and is allowed in the
  shared settings; `smoke-run` and `smoke-cleanup` write, so they wait for the user's go at
  the matching stop below, and the settings and the outbound hook ask for it again. A command
  outside the suite is a read-only diagnosis, one per call, always with `--context`. The one
  exception, without GitOps only: the `helm upgrade` that switches the verbosity for the test
  (step 2) and the one that restores it (step 6), each after the user's go.
- **kuik config**: when kuik is managed by GitOps (Flux, Argo CD), never `kubectl edit`,
  `patch` or `helm upgrade` it; a config change is a GitOps change the user makes.
- **Kubeconfig**: passed to the suite as `--kubeconfig <path>`, or as
  `KUBECONFIG=<path> kubectl --context <name> ...` for a diagnosis. Never open, cat or print
  the file, never copy it.
- **Unexpected result**: stop and report. Do not improvise a fix on the cluster.

## Reference

| Item | Value |
| ---- | ----- |
| Tasks | `task smoke-check`, `task smoke-run`, `task smoke-cleanup`, from the repository root, each followed by `-- --kubeconfig <path> --context <name>` and the suite's flags |
| Flags | `--kuik-namespace` (default `kuik-system`), `--release` (default `kube-image-keeper`), `--kuik-version <v>` (check), `--private-repo <repo> --pull-auth <secret>` (test 11) |
| Suite | [`test/smoke/`](../../../test/smoke/), Go on `sigs.k8s.io/e2e-framework` behind the build tag `smoke` ([note 0014](../../../notes/0014-e2e-framework-for-the-smoke-suite.md)): `TestInstall` reads, `TestRouting` creates and deletes its test resources |
| Test resources | every one carries the label `kuik.enix.io/smoke-test: "true"`; they live in the namespaces `kuik-test` (selected by the test CRs) and `kuik-test-unlabeled`, or are named `kuik-test-*` (the test CRs, the placeholder Secret `kuik-test-quay-creds` in the kuik namespace) |
| Deployments | `<fullname>-webhook` (2 replicas by default), `<fullname>-reconciler`, `<fullname>-secret-syncer`; `<fullname>` is the release name, suffixed with `-kube-image-keeper` unless it contains it |

## Workflow

### 1. Inputs

Ask the user for anything missing:

1. the version under test (for example `3.0.0-alpha.3`);
2. the kubeconfig path and the context to use;
3. the kuik namespace and Helm release name (defaults `kuik-system` / `kube-image-keeper`);
4. whether kuik is managed by GitOps (Flux, Argo CD): if it is, never change kuik's config
   with kubectl or helm;
5. where to write the report: by default a Markdown report in the conversation (see step 5).
   The user, or their own memory, may name another destination;
6. for the real pull (test 11): a private repository holding a copy of
   `quay.io/nginx/nginx-unprivileged:1.31.6-alpine`, and the name of a docker config Secret
   in the kuik namespace that can pull it. The user creates that Secret; never create, read
   or print it. Without them, test 11 is skipped and reported as not tested.

The previous tag is the latest `v3.*` tag before the version (`git tag --list 'v3.*'`).

### 2. Read-only checks

Run `task smoke-check -- --kubeconfig <path> --context <name> --kuik-version <version>`
(`git fetch --tags` first, for the revision). `TestInstall` checks the release (chart, app
version, deployed), every kuik pod Running without restart, no error in their logs, and
`kuik_build_info` on every webhook replica against the version and the commit of its tag. It
logs, without failing, the admission policies the test pods could break (Kyverno in Enforce,
ValidatingAdmissionPolicies) and the verbosity of every process with the Helm values
`verbosity`, `webhook.verbosity`, `reconciler.verbosity` and `secretSyncer.verbosity`. Note
each value as it is, empty included: an empty process value inherits the root `verbosity`,
and cleanup restores it empty.

**STOP. Report the results to the user and wait for the go.** If any process does not run at
`debug`, propose to switch them all for the test. `debug` is the highest level kuik logs at.
The webhook logs there why a candidate was dropped (`Candidate failed its check`), the
secret syncer each pull Secret it applies, the reconciler each status it writes: without
them, a missed rewrite or a missing Secret cannot be traced. Without GitOps, on approval,
set the root `verbosity` and any process value that is not empty:
`KUBECONFIG=<path> helm --kube-context <name> upgrade <release> oci://quay.io/enix/charts/kube-image-keeper --version <version> -n <ns> --reuse-values --set verbosity=debug`
(add `--set <process>.verbosity=debug` for each non-empty one). It restarts the kuik pods
only; the reconciler and the secret syncer elect a leader again. With GitOps, the user makes
the change.

### 3. Adapt the plan

1. Read `git log --oneline v<previous>..v<version>`; open `docs/v3/` or the code only where
   a commit is unclear.
2. Keep the base tests of `TestRouting`: no CR, fallback, origin up, out of scope,
   ImageMirror with nothing copied, ImageAlternative status, pull Secret provisioned,
   missing source Secret, pull Secret deleted, real pull from the private repository of
   step 1.
3. Propose simple new tests for what the release adds (for example: the mirror pod rewritten
   once copies happen). A new test is a feature of `TestRouting` in `test/smoke/`: it creates
   its objects with `create`, which labels them and refuses anything outside the rules of the
   [Reference](#reference) or a kuik CR not scoped twice (`namespaceSelector` on
   `kuik.enix.io/test: "true"` and a non-empty `podSelector`). Update the expectation of an
   existing test when the release changes it (for example the ImageMirror test once copies
   happen). Check with `task lint` and a run on a test cluster before the shared one.
4. Show the plan: tests kept, tests added or changed with their expected results, any new
   cluster-scoped kind.

**STOP. Wait for the user's approval of the plan.** This go covers `task smoke-run`.

### 4. Execute

Run `task smoke-run -- --kubeconfig <path> --context <name>` with the inputs of step 1
(`--private-repo` and `--pull-auth` for test 11, else it is skipped). It refuses to start
while a test resource of an earlier run is left (run `task smoke-cleanup` first), and to
touch an object it did not create.

Relay the result of each feature. On a failure the suite keeps the test resources for the
diagnosis and prints the webhook log lines naming the pod: report them, add read-only
diagnosis if needed, and do not fix anything on the cluster.

### 5. Report

Write the report where step 1 said. The default is Markdown in the conversation:

- the version, the context, the date, and whether any existing workload was touched;
- a test table: #, Test, Expected, Observed, Status;
- caveats;
- implemented but not tested;
- not shipped yet in this version.

### 6. Cleanup

**STOP. Only on the user's approval.** Then:

1. A green `smoke-run` already deleted its resources. After a failure, run
   `task smoke-cleanup -- --kubeconfig <path> --context <name>`: it deletes the test CRs and
   Secrets, then the 2 test namespaces if they carry the label, and checks none is left. The
   user's Secret of test 11 carries no label and stays: offer its deletion, the user decides.
2. If the verbosity was switched for the test and the user wants it back, restore every value
   noted in step 2 the same way it was switched (`helm --kube-context <name> upgrade ...
   --reuse-values`, without GitOps): the root `verbosity` to its value, and
   `--set <process>.verbosity=""` for a process value that was empty, never the effective
   level it resolved to.
3. Offer to delete the kubeconfig. The user decides; do not read it.

## Common mistakes

| Mistake | Fix |
| ------- | --- |
| Going on after a context mismatch | Stop: the commands would hit another cluster |
| Running `go test -tags smoke` or kubectl writes by hand | Go through the tasks: they fix which tests run, and the hook asks before the ones that write |
| A test object created without `create` | Use `create`: it labels the object and refuses what cleanup could not find |
| Reading metrics through the Service or a single pod | Query each webhook pod: only the replica that served the admission counts the rewrite |
| Fixing a surprise on the cluster (edit, restart, re-apply with changes) | Stop, report to the user |
| Changing kuik's config with kubectl or helm on a GitOps cluster | The user changes it in the GitOps repository |

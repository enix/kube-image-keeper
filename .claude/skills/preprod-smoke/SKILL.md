---
name: preprod-smoke
description: Use when asked to smoke test a kuik v3 release on a pre-production or other shared cluster where kuik is already installed, for example "smoke test kuik 3.0.0-alpha.3 on preprod" or "validate the release on our cluster".
argument-hint: "<version>"
---

# kuik smoke test on a shared cluster

## Overview

Replays, for one kuik v3 release, a manual smoke test on a cluster that runs real workloads,
then reports the results. The tests stay simple: kuik breaks nothing, routes when needed,
ignores otherwise. Edge cases belong to the e2e suite, on Kind.

The cluster is not a test cluster: every rule below protects the workloads already on it.

## Hard rules

- **Existing workloads**: never touch, restart or roll out an existing workload. If a test
  ever needs one, name that specific workload ahead and wait for the user's explicit
  approval.
- **Scoping**: every test CR carries a `namespaceSelector` on `kuik.enix.io/test: "true"` AND
  a `podSelector`. The CRs are cluster-scoped and the webhook intercepts every namespace: a
  CR without both selectors can reroute the cluster's pods.
- **kuik config**: when kuik is managed by GitOps (Flux, Argo CD), never `kubectl edit`,
  `patch` or `helm upgrade` it; a config change is a GitOps change the user makes.
- **Commands**: one command per Bash call, no chaining. Nothing that writes to the cluster
  (apply, delete) without the user's approval at the matching stop below.
- **Kubeconfig**: only as `KUBECONFIG=<path> kubectl ...` (or `helm`). Never open, cat or
  print the file, never copy it.
- **Unexpected result**: stop and report. Do not improvise a fix on the cluster.

## Reference

| Item | Value |
| ---- | ----- |
| Deployments | `<fullname>-webhook` (2 replicas by default), `<fullname>-reconciler`, `<fullname>-secret-syncer`; `<fullname>` is the release name, suffixed with `-kube-image-keeper` unless it contains it |
| Pod selectors | `app.kubernetes.io/name=kube-image-keeper`, plus `app.kubernetes.io/component=<process>` |
| Manifests | [`manifests/`](./manifests/README.md), next to this file (its README has the expected results) |
| Test CRs | ImageAlternatives `kuik-test-nginx`, `kuik-test-pull-secret`, `kuik-test-missing-secret`, ImageMirror `kuik-test-mirror` |
| Test Secret | `kuik-test-quay-creds` in the kuik namespace: a placeholder, never used for a pull |
| Test namespaces | `kuik-test` (labelled), `kuik-test-unlabeled` |

Metrics without port-forward, one call per kuik pod (the counters are per replica: only the
webhook replica that served the admission shows the rewrite, so sum over the replicas):

```sh
KUBECONFIG=<path> kubectl get --raw /api/v1/namespaces/<ns>/pods/<pod>:8080/proxy/metrics
```

Filter the output for `kuik_routing` and `kuik_build_info` when reading it.

## Workflow

### 1. Inputs

Ask the user for anything missing:

1. the version under test (for example `3.0.0-alpha.3`);
2. the kubeconfig path and the context to use;
3. the kuik namespace and Helm release name (defaults `kuik-system` / `kube-image-keeper`);
4. whether kuik is managed by GitOps (Flux, Argo CD): if it is, never change kuik's config
   with kubectl or helm;
5. where to write the report: by default a Markdown report in the conversation (see step 5).
   The user, or their own memory, may name another destination.

The previous tag is the latest `v3.*` tag before the version (`git tag --list 'v3.*'`).

### 2. Read-only checks

One command each, all with `KUBECONFIG=<path>`:

1. `kubectl config current-context` is the context the user named. Otherwise stop.
2. `helm -n <ns> list` shows chart `kube-image-keeper-<version>` and app version `<version>`.
3. `kubectl -n <ns> get pods`: every kuik pod Running, 0 restarts.
4. Logs of each kuik pod (every webhook replica): no error.
5. `kuik_build_info` from the metrics shows `<version>` and a revision equal to
   `git rev-parse v<version>^{commit}` (compare the short form if the metric carries one).
6. Admission policies that could reject the test pods: if Kyverno or another admission
   policy engine is installed, list its policies in enforce mode (for Kyverno,
   `kubectl get clusterpolicies -o custom-columns=NAME:.metadata.name,ACTION:.spec.validationFailureAction`).
   Report any policy the test pods could break.
7. The verbosity of every process: `-zap-log-level` in the args of each kuik Deployment, and
   the Helm values `verbosity`, `webhook.verbosity`, `reconciler.verbosity` and
   `secretSyncer.verbosity` (`helm -n <ns> get values <release> --all`). Note each value as
   it is, empty included: an empty process value inherits the root `verbosity`, and cleanup
   restores it empty.

**STOP. Report the results to the user and wait for the go.** If any process does not run at
`debug`, propose to switch them all for the test. `debug` is the highest level kuik logs at.
The webhook logs there why a candidate was dropped (`Candidate failed its check`), the
secret syncer each pull Secret it applies, the reconciler each status it writes: without
them, a missed rewrite or a missing Secret cannot be traced. Without GitOps, on approval,
set the root `verbosity` and any process value that is not empty:
`KUBECONFIG=<path> helm upgrade <release> oci://quay.io/enix/charts/kube-image-keeper --version <version> -n <ns> --reuse-values --set verbosity=debug`
(add `--set <process>.verbosity=debug` for each non-empty one). It restarts the kuik pods
only; the reconciler and the secret syncer elect a leader again. With GitOps, the user makes
the change.

### 3. Adapt the plan

1. Read `git log --oneline v<previous>..v<version>`; open `docs/v3/` or the code only where
   a commit is unclear.
2. Keep the base tests:
   1. health (step 2 above);
   2. no CR: pod untouched (`10-pod-no-cr.yaml`);
   3. fallback when the origin is `.invalid` (`fallback` in `30-pods-routing.yaml`);
   4. untouched when the origin answers (`origin-up`);
   5. untouched outside the namespaceSelector (`out-of-scope`);
   6. ImageMirror with nothing copied keeps the origin (`40-imagemirror.yaml`);
   7. ImageAlternative status after test 3: `activeFallbacks`, `FallbackActive`, `Ready`, the
      `ImageFallback` event on the pod (reads only, no manifest);
   8. pull Secret provisioned in the selected namespace only (`50-pull-auth.yaml`);
   9. missing source Secret reported (same file);
   10. pull Secret deleted with its ImageAlternative.
3. Propose simple new tests for what the release adds (for example: status and conditions
   of the CRs once the reconcilers are no longer empty; the mirror pod rewritten once copies
   happen). A new test gets its own manifest file in `manifests/`, scoped as the hard rules
   say, with its expected result as a header comment. Update the expectations of an existing
   test when the release changes them (for example test 6 once ImageMirror copies).
4. Show the plan: tests kept, tests added or changed with their expected results, manifests
   to create or edit, any new cluster-scoped CR.

**STOP. Wait for the user's approval of the plan.**

### 4. Execute

Apply one manifest file at a time, in order, from the repository root, and check before
the next:

```sh
KUBECONFIG=<path> kubectl apply -f .claude/skills/preprod-smoke/manifests/00-namespaces.yaml
```

After each file, record expected versus observed for every pod it creates:

- image: `KUBECONFIG=<path> kubectl get pods -n <test-namespace> -o custom-columns='NAME:.metadata.name,IMAGE:.spec.containers[0].image,PHASE:.status.phase'`;
- annotations `kuik.enix.io/rewrites` and `kuik.enix.io/no-alternatives`;
- phase (`out-of-scope` is expected in `ErrImagePull` / `ImagePullBackOff`);
- metrics of every webhook pod after a routing test;
- CR status and conditions, once the release fills them.

Report each step as you go, in one short line: expected, observed, OK or not. On any
unexpected result, stop and report.

### 5. Report

Write the report where step 1 said. The default is Markdown in the conversation:

- the version, the context, the date, and whether any existing workload was touched;
- a test table: #, Test, Expected, Observed, Status;
- caveats;
- implemented but not tested;
- not shipped yet in this version.

### 6. Cleanup

**STOP. Only on the user's approval.** Then, one command each:

1. Delete the cluster-scoped CRs first (the test CRs of [Reference](#reference), any CR
   added in step 3): deleting the namespaces does not remove them.
2. Delete the Secret `kuik-test-quay-creds` from the kuik namespace.
3. Delete namespaces `kuik-test` and `kuik-test-unlabeled`.
4. Restore every verbosity value noted in step 2, the same way they were switched: the root
   `verbosity` to its value, and `--set <process>.verbosity=""` for a process value that was
   empty, never the effective level it resolved to.
5. Verify nothing named `kuik-test` remains (namespaces, imagealternatives, imagemirrors,
   the Secret, and any new kind).
6. Offer to delete the kubeconfig. The user decides; do not read it.

## Common mistakes

| Mistake | Fix |
| ------- | --- |
| Going on after a context mismatch | Stop: the commands would hit another cluster |
| Deleting only the namespaces | The CRs are cluster-scoped: delete them first, by name |
| Reading metrics through the Service or a single pod | Query each webhook pod: only the replica that served the admission counts the rewrite |
| A new CR with a namespaceSelector only | Add a podSelector too |
| Fixing a surprise on the cluster (edit, restart, re-apply with changes) | Stop, report to the user |
| Changing kuik's config with kubectl or helm on a GitOps cluster | The user changes it in the GitOps repository |

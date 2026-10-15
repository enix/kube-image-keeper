---
name: release-acceptance
description: Use when asked to run the acceptance tests of a kuik v3 release (also called a smoke test) on a pre-production or other cluster where kuik is already installed, for example "validate kuik 3.0.0-alpha.3 on preprod", "smoke test the release on our cluster" or "run the acceptance tests".
argument-hint: "<version>"
---

# kuik release acceptance on a shared cluster

## Overview

Accepts one kuik v3 release on a cluster that runs real workloads. The program [`test/acceptance/`](../../../test/acceptance/README.md) applies the fixtures and prints what the cluster holds; it never decides. **You judge**: you compare what it prints with what [`docs/v3/`](../../../docs/v3/) says, look for what nobody wrote a check for, and report ([note 0015](../../../notes/0015-acceptance-tooling-acts-the-agent-judges.md)). Read the [README](../../../test/acceptance/README.md) first (its commands and its guard), then [`scenarios.md`](../../../test/acceptance/scenarios.md): the user stories you run, each with what to prepare, what to expect and how to check it. They are the floor every run covers, not the ceiling: a run that only ticks them found nothing an e2e spec would not.

You run the scenarios; you do not write them. New ones are drafted per release with the user by the [`acceptance-scenarios`](../acceptance-scenarios/SKILL.md) skill, before this run.

The cluster is not a test cluster: every rule below protects the workloads already on it.

## Hard rules

- **Existing workloads**: never touch, restart or roll out an existing workload, kuik's own pods included. If a test ever needs one, name that specific workload ahead and wait for the user's explicit approval.
- **The tasks only**: the cluster is reached through the tasks of the [Reference](#reference), one per Bash call, alone: no pipe, redirection or `grep`/`cut` after it, or the hook asks every time. Filter with `--grep` and `--metrics`, a regex in single quotes, and read a long output as it is. `acceptance-check` and `acceptance-inspect` only read; `acceptance-setup`, `-apply` and `-delete` write test objects only, through the program's guard. The shared settings allow all five: run them without asking. A command outside the tasks is a read-only diagnosis, one per call, always with `--context`, and never reads a Secret's data. The one exception, without GitOps only: the `helm upgrade` that switches the verbosity for the test (step 2) and the one that restores it (step 7), which the outbound hook asks for.
- **The guard is reviewed code**: never edit `test/acceptance/` during a run, scenarios and fixtures included. A change to the program (Go code, Taskfile) runs on a cluster only once the user has approved it, with `task acceptance-approve` after your explanation (step 4). A branch of new scenarios and fixtures, written beforehand by the `acceptance-scenarios` skill, needs no approval: each fixture goes through the guard of `origin/main`. A scenario worth keeping goes to the report (step 6).
- **Cleanup waits for the user**: the test objects stay until the user says the results are accepted, so they can look at the cluster too. Never clean up on your own initiative; `acceptance-cleanup` asks for permission again.
- **kuik config**: when kuik is managed by GitOps (Flux, Argo CD), never `kubectl edit`, `patch` or `helm upgrade` it; a config change is a GitOps change the user makes.
- **Kubeconfig**: passed as `--kubeconfig <path> --context <name>`, or as `KUBECONFIG=<path> kubectl --context <name> ...` for a diagnosis. Never open, cat or print the file, never copy it.
- **Unexpected result**: investigate with reads, report it. Do not fix anything on the cluster.

## Reference

| Item | Value |
| ---- | ----- |
| Tasks | [Commands](../../../test/acceptance/README.md#commands): `acceptance-check`, `-setup`, `-inspect`, `-apply`, `-delete` (allowed), `-approve` and `-cleanup` (ask); each followed by `-- --kubeconfig <path> --context <name>` |
| Test objects | what [the guard](../../../test/acceptance/README.md#the-guard) lets through; anything else is refused before a write |
| Exploration files | write the YAML of `acceptance-apply` in your scratchpad, never in the repository; the report says what you tried |

## Workflow

### 1. Inputs

Ask the user for anything missing:

1. the version under test (for example `3.0.0-alpha.3`);
2. the kubeconfig path and the context to use;
3. the kuik namespace and Helm release name (defaults `kuik-system` / `kube-image-keeper`);
4. whether kuik is managed by GitOps (Flux, Argo CD): if it is, never change kuik's config with kubectl or helm;
5. where to write the report: by default a Markdown report in the conversation (see step 6). The user, or their own memory, may name another destination;
6. for the real pull (test 11): a private repository holding a copy of `quay.io/nginx/nginx-unprivileged:1.31.6-alpine`, and the name of a docker config Secret in the kuik namespace that can pull it. The user creates that Secret; never create, read or print it. Without them, test 11 is skipped and reported as not tested.
7. `--public-repo`: a repository holding a copy of `nginx-unprivileged:1.31.6-alpine`, pulled anonymously by the test pods, when a real ImageAlternative or ImageMirror of the cluster covers the default `quay.io/nginx/nginx-unprivileged` (list them with a read-only `KUBECONFIG=<path> kubectl --context <name> get imagealternatives,imagemirrors`). On a cluster whose egress IP is shared, prefer a registry without an anonymous pull quota;
8. only if a scenario needs an ImageMirror that copies for real: `--mirror-prefix`, a registry path the user dedicates to the test. Never guess one: a test mirror deletes the tags of this cluster it finds under its path.

The previous tag is the latest `v3.*` tag before the version (`git tag --list 'v3.*'`).

### 2. The installation

First, the approval: every acceptance task asks while the program differs from `origin/main` unapproved. `git fetch origin --tags`, then list what the working copy changes beyond `origin/main`, the fixtures and the Markdown files left out: `git diff --stat origin/main -- . ':(exclude,glob)test/acceptance/fixtures/*.yaml' ':(exclude,glob)**/*.md'` and the untracked files. When it lists nothing, go on: the tasks run without asking. Otherwise the user approves the program once for the whole run:

1. Read the diff, and each untracked source file in full (an untracked `.go` file in `test/acceptance` is compiled into the program, while the diff and the approval list show only its name). Never open an untracked file that may hold a secret (a kubeconfig, `.env`, a key, a token): name it to the user, who checks it and removes it from the tree. Then explain the changes to the user, one line per change: what it does, and whether it touches the guard, a skip rule, what the program writes to the cluster or the credentials it reads. Name any reference outside the working copy (a `replace` or `use` to a local path in `go.mod` or `go.work`, a script the Taskfile calls outside the tree): the approval does not cover what it points to. Detailed, clear and concise: the user approves on that explanation, so leave nothing out and add nothing.
2. Run `task acceptance-approve`, alone, from the top of the working copy. The hook asks the user, listing the changed and new files; on a refusal, stop the run.
3. Any code change afterwards voids the approval: the hook asks again at the next task. Explain the new change and approve again.

Then `task acceptance-check -- --kubeconfig <path> --context <name> --kuik-version <version>`. Judge it against test 1 of [`scenarios.md`](../../../test/acceptance/scenarios.md) and tell the user what you see. A broken installation stops the run there. Note the Helm `verbosity` values as they are, empty included: an empty process value inherits the root `verbosity`, and step 7 restores it empty.

If any process does not log at `debug`, switch them all for the test: the webhook logs there why a candidate was dropped (`Candidate failed its check`), the secret syncer each pull Secret it applies, the reconciler each status it writes. Without them, what you observe cannot be explained. Without GitOps, set the root `verbosity` and any process value that is not empty (the user accepts or refuses it at the permission prompt; on a refusal, go on at the current level): `KUBECONFIG=<path> helm --kube-context <name> upgrade <release> oci://quay.io/enix/charts/kube-image-keeper --version <version> -n <ns> --reuse-values --set verbosity=debug` (add `--set <process>.verbosity=debug` for each non-empty one). It restarts the kuik pods only. With GitOps, the user makes the change: stop until they say it is done.

### 3. The plan

1. Read `git log --oneline <previous>..v<version>` and the release notes (`gh release view v<version>`); open `docs/v3/` or the code only where a commit is unclear.
2. Run every scenario of [`scenarios.md`](../../../test/acceptance/scenarios.md) whose **Since** is not newer than the version under test; `setup` skips the fixtures of the others.
3. **Coverage**: when the release adds a behaviour a user can observe (a field, a reason, an event, a metric, a chart value) that no scenario covers, tell the user what is missing, propose to run the [`acceptance-scenarios`](../acceptance-scenarios/SKILL.md) skill first, and **STOP**: the run goes on only once the scenarios are merged or the user says to go without them.
4. Add, for what the release changes, what to look at and what to try beyond the scenarios: their **Explore** variants first, then your own. They are explorations with `acceptance-apply` on top of the base objects, never a change to `test/acceptance/`.
5. Show the plan in a few lines, then go on to step 4 in the same turn.

### 4. Setup

`task acceptance-setup -- --kubeconfig <path> --context <name> --kuik-version <version>`, with `--private-repo` and `--pull-auth` for tests 11 and 13, `--mirror-prefix` for test 14, and `--public-repo` from step 1, passed again to every `acceptance-apply`. It skips, with a line, every fixture whose `since` is newer than the version. It prints the time it started, the rewrite counters of the test CRs before any test pod existed (keep them: test 3 compares with them), and every object it created, fixture by fixture. It waits for nothing kuik does, except the pull Secret of test 11 before its pod.

### 5. Evaluate and explore

Give kuik a minute, then `task acceptance-inspect -- --kubeconfig <path> --context <name>`. Narrow it with `--grep` and `--metrics` when you dig into one test.

1. **The floor**: for each scenario, run its **Do** actions and its **Check** commands, and compare what they print with **They expect**, and with the spec section each point links when they differ. A status written a few seconds after a pod is not a failure: inspect again before you conclude.
2. **Look further**: read the whole output, not only the lines you expected. Signs that deserve a closer look: a status that contradicts the events or the pod annotations, a counter that does not add up across replicas, a log line at `info` or above you cannot explain, an event repeating, a Secret nobody asked for, a delay much longer than the others.
3. **Explore**: try what the floor does not cover, one change at a time, then inspect. Directions: change an object (`rewritePolicy`, an alternative's order, a selector), a pod kuik must treat differently (several containers, an init container, a digest-pinned image, `imagePullPolicy: Always` on a mirrored image), a dependency that disappears (the placeholder Secret `kuik-test-quay-creds`, an injected pull Secret: does it come back?). Pick by what the release touched. Keep each change inside the test objects; the program refuses the rest.
4. When something is off, find out why with reads (the inspect logs, the spec, the code at the tag) before you report it: say whether it is a bug, a behaviour the spec allows, or something not shipped in this version.

### 6. Report

Write the report where step 1 said. The default is Markdown in the conversation:

- the version, the context, the date, and whether any existing workload was touched;
- a test table, one row per scenario run: #, Test, Expected, Observed, Verdict;
- **found while exploring**: what you tried, what you saw, your verdict and why, with the spec section;
- **scenarios to add**: the findings worth checking on every release, each as a one-line user story with its spec link, for the next run of the [`acceptance-scenarios`](../acceptance-scenarios/SKILL.md) skill;
- caveats;
- implemented but not tested;
- not shipped yet in this version.

**STOP. The test objects stay in place.** Ask the user whether they accept the results or want to look at something first.

### 7. Cleanup

Only once the user has accepted the results:

1. `task acceptance-cleanup -- --kubeconfig <path> --context <name>`: it deletes the test CRs and Secrets, then the 2 test namespaces, and checks none is left. The user's Secret of test 11 carries no label and stays: offer its deletion, the user decides.
2. If the verbosity was switched for the test and the user wants it back, restore every value noted in step 2 the same way it was switched (`helm --kube-context <name> upgrade ... --reuse-values`, without GitOps): the root `verbosity` to its value, and `--set <process>.verbosity=""` for a process value that was empty, never the effective level it resolved to.
3. Offer to delete the kubeconfig. The user decides; do not read it.

## Common mistakes

| Mistake | Fix |
| ------- | --- |
| Going on after a context mismatch | Stop: the commands would hit another cluster |
| Reporting the scenarios and nothing else | Explore: they are what an e2e spec would check; acceptance is for what it would not |
| Writing a scenario or a fixture during the run | Report it under "scenarios to add"; the `acceptance-scenarios` skill writes it with the user |
| Running on when the release adds a behaviour no scenario covers | Stop at the plan and propose the `acceptance-scenarios` skill |
| Calling a delay a failure | Inspect again; report the delay itself when it is far beyond the others |
| Reporting a hunch as a bug | Trace it to the spec or the code at the tag first; say which |
| Reading metrics through the Service or a single pod | Inspect prints every replica: only the replica that served an admission counts it |
| Cleaning up once the report is written | Wait for the user to accept the results |
| `kubectl` writes, or a `go run` of the program around the tasks | Go through the tasks: the program holds the guard, and the hook asks before each write |
| Fixing a surprise on the cluster (edit, restart, re-apply with changes) | Stop, report to the user |
| Changing kuik's config with kubectl or helm on a GitOps cluster | The user changes it in the GitOps repository |

# Release acceptance

Before a kuik v3 release is announced, it is accepted on a cluster where it already runs, often a shared one with real workloads. This directory holds the tooling for it: the [scenarios](./scenarios.md) every run covers, the [fixtures](./fixtures/) that prepare them, and a small program that creates test objects and prints what the cluster holds.

The program never decides whether kuik works. Whoever runs the acceptance does: in practice an agent driven by the [`release-acceptance`](../../.claude/skills/release-acceptance/SKILL.md) skill. It reads what the program prints, compares it with [`docs/v3/`](../../docs/v3/), and explores beyond the scenarios to find what nobody wrote a check for. The Kind e2e suite ([`test/e2e/`](../e2e/)) already covers fixed assertions. Why it is split this way: [note 0015](../../notes/0015-acceptance-tooling-acts-the-agent-judges.md).

## Commands

Each command is a task; every one but `validate` takes `-- --kubeconfig <path> --context <name>`, plus `--kuik-namespace` (default `kuik-system`) and `--release` (default `kube-image-keeper`) when they differ. `setup` and `apply` take `--mirror-prefix <path>` for a test ImageMirror that must write to a real registry, and `--public-repo <repository>` for the public image the test pods pull (default `quay.io/nginx/nginx-unprivileged`). `validate` takes `--kuik-namespace` only.

| Task | Does |
| ---- | ---- |
| `acceptance-check [--kuik-version V]` | Reads the installation: the Helm release, the kuik pods and their error lines, `kuik_build_info` of each webhook replica next to the commit of the tag, the log levels, the admission policies that could reject a test pod |
| `acceptance-validate [--kuik-namespace NS]` | Needs no cluster and no `--kubeconfig`/`--context`; `--kuik-namespace` fills `${KUIK_NAMESPACE}`, as for `setup`. Decodes every fixture strictly with example values for its variables, runs the guard on every object, checks its `# since:` line, and prints one line per fixture |
| `acceptance-setup [--kuik-version V] [--private-repo R --pull-auth S] [--mirror-prefix P] [--public-repo R]` | Applies the fixtures in order, skipping those newer than `V`. Refuses to start while an object of an earlier run is left, and prints the rewrite counters before any test pod |
| `acceptance-inspect [--since D] [--grep RE] [--metrics RE]` | Reads the test objects: CRs with their status, pods with their image and kuik annotations, test Secrets (keys only, never the data), events, kuik metrics per pod, kuik log lines |
| `acceptance-get [-n NS] [-l SELECTOR] [--jsonpath T] KIND[/NAME]` | Reads one object, or the list of a kind in a namespace (`-n`, default `kuik-test`), and prints it as YAML without `managedFields`, or through `--jsonpath` (kubectl's syntax, ranges included). `KIND` is `pod`, `configmap`, `secret`, `namespace`, `imagealternative`, `imagemirror`, `imagemonitor`, `validatingadmissionpolicy` or `validatingadmissionpolicybinding`. A Secret prints as in `inspect`, never its data, and refuses `--jsonpath`. A missing object prints `NotFound: <kind> <name> [in <namespace>]` and exits 0 |
| `acceptance-apply --file F [--private-repo R] [--pull-auth S] [--mirror-prefix P] [--public-repo R]` | Creates the objects of a YAML file, or updates those the test created; fills its variables like `setup` |
| `acceptance-delete [--namespace NS] KIND/NAME` | Deletes one test object |
| `acceptance-config --file F [--kuik-version V] \| --restore [--timeout D]` | Merges a config fixture (`test/acceptance/fixtures/*.config.yaml` only, refused when its `# since:` is newer than `V`) over kuik's original global config and writes it to the ConfigMap `<fullname>-config`, refusing a result kuik would reject; `--restore` puts the original back. Both wait up to `--timeout` (default 3 minutes) for every running kuik pod to log `Reloaded global config` (a pod started after the write counts as reloaded), print that line per pod, and fail naming the pods that did not reload; a config kuik reads the same is not waited for |
| `acceptance-cleanup` | Restores the global config when a test config is in place, then deletes every test object and checks none is left |
| `acceptance-approve` | Needs no cluster and takes no flag. Records the user's approval of the program as it differs from `origin/main` (see [the guard](#the-guard)) |

## The guard

`setup`, `apply` and `delete` share one guard, in [`guard.go`](./guard.go). It lets through:

- the namespaces `kuik-test` (labelled `kuik.enix.io/test: "true"`) and `kuik-test-unlabeled`, both enforcing the restricted Pod Security profile;
- a Pod, Secret or ConfigMap inside them. A Pod sets no priority, no `nodeName`, no host namespace, no `hostPath` and no privileged container;
- a Secret named `kuik-test-*` in the kuik namespace, the source Secret a test CR refers to;
- an ImageAlternative, ImageMirror or ImageMonitor named `kuik-test-*` whose `namespaceSelector` matches both `kuik.enix.io/test: "true"` and `kubernetes.io/metadata.name: kuik-test`, and whose `podSelector` is not empty. The CRs are cluster-scoped and the webhook sees every namespace: this is what keeps a test CR off the real workloads.

A test CR reads only `kuik-test-*` Secrets or the user's `--pull-auth`, never a cloud provider identity. An ImageMirror writes only to a `.invalid` host or under `--mirror-prefix`, a registry path the user dedicates to the test, and never overlaps a real ImageMirror's path: kuik deletes the tags of this cluster it finds under a mirror's destination.

Every object created is labelled `kuik.enix.io/acceptance-test: "true"`. An existing object without that label is never changed or deleted, except inside a test namespace (a pull Secret kuik injected there, say). `cleanup` deletes what carries the label and a test name, and waits for every one to be gone. An update keeps the finalizers others set. The writing commands refuse a `--kuik-namespace` that holds no pod of the release.

`config` writes 2 objects the test did not create, and only these fields of them, whatever its input:

- the key `config.yaml` of the ConfigMap `<fullname>-config` in the kuik namespace (`kube-image-keeper-config` for the default release), and its annotations `kuik.enix.io/acceptance-original-config` (the config of before the first change of the run, kept on the ConfigMap so an interrupted run can still restore it), `kuik.enix.io/acceptance-original-config-absent` (the ConfigMap held no `config.yaml`: `--restore` removes the key) and `kuik.enix.io/acceptance-suspended-release`. Each write is one merge patch carrying the `resourceVersion` it was read at, so a change made in between fails it;
- `spec.suspend` of the Flux HelmRelease (`helm.toolkit.fluxcd.io/v2`) the labels `helm.toolkit.fluxcd.io/name` and `helm.toolkit.fluxcd.io/namespace` of that ConfigMap name, when it carries them: the first change suspends it, so Flux does not put the chart's config back, and records it in the annotation; `--restore` resumes only a HelmRelease the program suspended. Without the labels, only the ConfigMap is written.

The program bounds which object and fields `config` writes, not what the config says: a config fixture is trusted like reviewed code, so it is reviewed with its pull request.

A config fixture is merged over the original config as a JSON merge patch (a map merges key by key, `null` removes a key), so the rest of the cluster's config stays. The result is decoded and validated by `internal/config` of the checked-out tree before anything is written: the release under test may validate differently when the tree is not at its tag. The order of the writes keeps a record of each change before it happens: the first change writes the original and, when it is about to suspend the HelmRelease, `acceptance-suspended-release`, then suspends it, then writes the config; `--restore` writes the original back, resumes the HelmRelease, and only then removes `acceptance-suspended-release`, so a failed resume is retried by the next `--restore` or `cleanup`. `check` reports in its section "test config" whether a test config is in place and whether the HelmRelease is suspended. `cleanup` fails before any deletion when the ConfigMap of `--release` does not exist and another ConfigMap of the kuik namespace holds a test config.

The kubeconfig user of `config` needs `get` and `patch` on `configmaps` in the kuik namespace (`list` too, for `cleanup`), and `get` and `patch` on `helmreleases.helm.toolkit.fluxcd.io` in the HelmRelease's namespace when kuik runs under Flux.

The client is built from `--kubeconfig` and `--context` only, never from the current context or `~/.kube/config`.

Because of the guard, the Claude Code settings let an agent run every task without asking, except `cleanup`, which ends a run once the user has accepted it, `approve`, and `config`, which changes kuik's own config on a shared cluster and asks every time. That holds for the reviewed guard only: the outbound hook asks before any acceptance task when anything but the fixtures and the Markdown files differs from `origin/main`, or when git could not compare against it (a local ref of that name, a file hidden from the index, a task run from a subdirectory). A fixture goes through the guard of `origin/main` object by object, so a branch of new scenarios runs before its pull request. A change to the program runs once the user approves it as it is, with `task acceptance-approve`: the hook asks there, listing the changed files, and the tasks run without asking until the code moves again. The approval is a fingerprint of the change in the git directory of the worktree, written only by that task.

`inspect` and `get` print no Secret data and no Secret annotation value: `last-applied-configuration` would hold the data.

`get` reads only, and refuses before any read what is not:

- a test object as the guard defines it: the 2 test namespaces, a Pod, Secret or ConfigMap inside them, a Secret named `kuik-test-*` in the kuik namespace;
- a kuik CR, test or not: they are cluster-scoped and hold no credential;
- a pod of the kuik release in the kuik namespace, for its readiness and restarts: `get` selects them by the release labels (`app.kubernetes.io/name: kube-image-keeper`, `app.kubernetes.io/instance: <release>`), so no other pod of that namespace is read;
- the ConfigMaps `<fullname>-config` and `kuik-cluster-id` of the kuik namespace;
- the ValidatingAdmissionPolicy and ValidatingAdmissionPolicyBinding `<fullname>-secret-syncer`.

A list (`KIND` without a name) is allowed only where every object it reads is: the kuik CRs, the pods, Secrets and ConfigMaps of a test namespace, the pods of the kuik release.

## Scenarios

[`scenarios.md`](./scenarios.md) holds one scenario per test, numbered like the acceptance report: what the user does, what they expect with its `docs/v3/` link, the fixture that prepares it, and the read-only command that shows each expected point. A scenario is the floor of a run, not its ceiling.

The fixtures under [`fixtures/`](./fixtures/) are one file per test, applied by `setup` in the order of their names. Each starts with a `# since: <version>` line, the kuik version that introduced its test: `setup --kuik-version` skips a fixture newer than the version under test, and a fixture without the line is an error. A fixture may use `${KUIK_NAMESPACE}`, `${PRIVATE_REPO}`, `${PULL_AUTH}`, `${MIRROR_PREFIX}` and `${PUBLIC_REPO}`, filled from `--kuik-namespace`, `--private-repo`, `--pull-auth`, `--mirror-prefix` and `--public-repo` (`quay.io/nginx/nginx-unprivileged` by default: the repository the test pods pull anonymously, which no resource of the cluster may cover); a variable the file uses and no flag fills is refused before any write. The `11-` and `13-` files are applied only with `--private-repo` and `--pull-auth`, the `14-` and `22-` files only with `--mirror-prefix`, and the `16-` and `17-` files never: their scenario applies them by hand with `apply`, once the monitors have reported for test 16, and expecting the API server to refuse it for test 17; `setup` waits for the pull Secret of test 11 between its two files. A `*.config.yaml` file is a piece of kuik's global config, not objects: `setup` skips it and `apply` refuses it, `acceptance-config --file` sets it, and `validate` checks its `# since:` line and decodes and validates it as kuik's config. Fixtures decode strictly into the kuik API types: a fixture that no longer decodes is a change in the API, which users would hit too. `validate` checks all of this without a cluster.

To run every scenario, `acceptance-setup` with `--kuik-version`, then the **Check** commands of each scenario, or one `acceptance-inspect`. To run one by hand, apply [`00-namespaces.yaml`](./fixtures/00-namespaces.yaml), then follow its **Prepare**, **Do** and **Check** with `acceptance-apply` and `acceptance-delete`.

A new scenario, kept for every release, is added with the [`acceptance-scenarios`](../../.claude/skills/acceptance-scenarios/SKILL.md) skill before the acceptance run, reviewed like any other change. What an agent tries during one run stays in its scratchpad and in its report.

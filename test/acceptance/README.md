# Release acceptance

Before a kuik v3 release is announced, it is accepted on a cluster where it already runs, often a shared one with real workloads. This directory holds the tooling for it: the [scenarios](./scenarios.md) every run covers, the [fixtures](./fixtures/) that prepare them, and a small program that creates test objects and prints what the cluster holds.

The program never decides whether kuik works. Whoever runs the acceptance does: in practice an agent driven by the [`release-acceptance`](../../.claude/skills/release-acceptance/SKILL.md) skill. It reads what the program prints, compares it with [`docs/v3/`](../../docs/v3/), and explores beyond the scenarios to find what nobody wrote a check for. The Kind e2e suite ([`test/e2e/`](../e2e/)) already covers fixed assertions. Why it is split this way: [note 0015](../../notes/0015-acceptance-tooling-acts-the-agent-judges.md).

## Commands

Each command is a task; every one but `validate` takes `-- --kubeconfig <path> --context <name>`, plus `--kuik-namespace` (default `kuik-system`) and `--release` (default `kube-image-keeper`) when they differ. `setup` and `apply` take `--mirror-prefix <path>` for a test ImageMirror that must write to a real registry. `validate` takes `--kuik-namespace` only.

| Task | Does |
| ---- | ---- |
| `acceptance-check [--kuik-version V]` | Reads the installation: the Helm release, the kuik pods and their error lines, `kuik_build_info` of each webhook replica next to the commit of the tag, the log levels, the admission policies that could reject a test pod |
| `acceptance-validate [--kuik-namespace NS]` | Needs no cluster and no `--kubeconfig`/`--context`; `--kuik-namespace` fills `${KUIK_NAMESPACE}`, as for `setup`. Decodes every fixture strictly with example values for its variables, runs the guard on every object, checks its `# since:` line, and prints one line per fixture |
| `acceptance-setup [--kuik-version V] [--private-repo R --pull-auth S]` | Applies the fixtures in order, skipping those newer than `V`. Refuses to start while an object of an earlier run is left, and prints the rewrite counters before any test pod |
| `acceptance-inspect [--since D] [--grep RE] [--metrics RE]` | Reads the test objects: CRs with their status, pods with their image and kuik annotations, test Secrets (keys only, never the data), events, kuik metrics per pod, kuik log lines |
| `acceptance-apply --file F [--private-repo R] [--pull-auth S]` | Creates the objects of a YAML file, or updates those the test created; fills its variables like `setup` |
| `acceptance-delete [--namespace NS] KIND/NAME` | Deletes one test object |
| `acceptance-cleanup` | Deletes every test object and checks none is left |

## The guard

`setup`, `apply` and `delete` share one guard, in [`guard.go`](./guard.go). It lets through:

- the namespaces `kuik-test` (labelled `kuik.enix.io/test: "true"`) and `kuik-test-unlabeled`, both enforcing the restricted Pod Security profile;
- a Pod, Secret or ConfigMap inside them. A Pod sets no priority, no `nodeName`, no host namespace, no `hostPath` and no privileged container;
- a Secret named `kuik-test-*` in the kuik namespace, the source Secret a test CR refers to;
- an ImageAlternative, ImageMirror or ImageMonitor named `kuik-test-*` whose `namespaceSelector` matches both `kuik.enix.io/test: "true"` and `kubernetes.io/metadata.name: kuik-test`, and whose `podSelector` is not empty. The CRs are cluster-scoped and the webhook sees every namespace: this is what keeps a test CR off the real workloads.

A test CR reads only `kuik-test-*` Secrets or the user's `--pull-auth`, never a cloud provider identity. An ImageMirror writes only to a `.invalid` host or under `--mirror-prefix`, a registry path the user dedicates to the test, and never overlaps a real ImageMirror's path: kuik deletes the tags of this cluster it finds under a mirror's destination.

Every object created is labelled `kuik.enix.io/acceptance-test: "true"`. An existing object without that label is never changed or deleted, except inside a test namespace (a pull Secret kuik injected there, say). `cleanup` deletes what carries the label and a test name, and waits for every one to be gone. An update keeps the finalizers others set. The writing commands refuse a `--kuik-namespace` that holds no pod of the release.

The client is built from `--kubeconfig` and `--context` only, never from the current context or `~/.kube/config`.

Because of the guard, the Claude Code settings let an agent run every task without asking, except `cleanup`, which ends a run once the user has accepted it. That holds for the reviewed guard only: the outbound hook asks before `setup`, `apply` or `delete` when this directory or the Taskfile differs from `origin/main`.

`inspect` prints no Secret data and no Secret annotation value: `last-applied-configuration` would hold the data.

## Scenarios

[`scenarios.md`](./scenarios.md) holds one scenario per test, numbered like the acceptance report: what the user does, what they expect with its `docs/v3/` link, the fixture that prepares it, and the read-only command that shows each expected point. A scenario is the floor of a run, not its ceiling.

The fixtures under [`fixtures/`](./fixtures/) are one file per test, applied by `setup` in the order of their names. Each starts with a `# since: <version>` line, the kuik version that introduced its test: `setup --kuik-version` skips a fixture newer than the version under test, and a fixture without the line is an error. A fixture may use `${KUIK_NAMESPACE}`, `${PRIVATE_REPO}`, `${PULL_AUTH}` and `${MIRROR_PREFIX}`, filled from `--kuik-namespace`, `--private-repo`, `--pull-auth` and `--mirror-prefix`; a variable the file uses and no flag fills is refused before any write. The `11-` and `13-` files are applied only with `--private-repo` and `--pull-auth`, the `14-` files only with `--mirror-prefix`; `setup` waits for the pull Secret of test 11 between its two files. Fixtures decode strictly into the kuik API types: a fixture that no longer decodes is a change in the API, which users would hit too. `validate` checks all of this without a cluster.

To run every scenario, `acceptance-setup` with `--kuik-version`, then the **Check** commands of each scenario, or one `acceptance-inspect`. To run one by hand, apply [`00-namespaces.yaml`](./fixtures/00-namespaces.yaml), then follow its **Prepare**, **Do** and **Check** with `acceptance-apply` and `acceptance-delete`.

A new scenario, kept for every release, is added with the [`acceptance-scenarios`](../../.claude/skills/acceptance-scenarios/SKILL.md) skill before the acceptance run, reviewed like any other change. What an agent tries during one run stays in its scratchpad and in its report.

# Acceptance scenarios

What a kuik v3 release must show on a real cluster, one scenario per test, numbered like the acceptance report. Each one is a user story a human can run by hand; the [`release-acceptance`](../../.claude/skills/release-acceptance/SKILL.md) skill runs every scenario whose **Since** is not newer than the version under test, then explores. A scenario is the floor of a run, not its ceiling. New scenarios come from the [`acceptance-scenarios`](../../.claude/skills/acceptance-scenarios/SKILL.md) skill.

The numbering has gaps on purpose: the scenarios the e2e suite or envtest cover were removed ([note 0016](../../notes/0016-acceptance-plays-the-use-cases.md)), and the others keep their number until they are regrouped by use case.

## Before any scenario

In every command, `<path>` is the kubeconfig file and `<name>` its context. `<kuik-namespace>` is the namespace kuik is installed in, `kuik-system` by default: add `--kuik-namespace <kuik-namespace>` to a task when it differs, and `--release <release>` when its Helm release is not `kube-image-keeper`. Every read goes through `acceptance-get` or `acceptance-inspect`, which never print a Secret's data, and every config change through `acceptance-config`: a run needs no `kubectl`. `acceptance-get` prints `NotFound: ...` for a missing object and exits 0.

`<public-repo>` is the repository the test pods pull anonymously, `quay.io/nginx/nginx-unprivileged` by default. On a cluster where a real ImageAlternative or ImageMirror covers it, every routing scenario shows that resource at work instead of the test one: pass `--public-repo <repo>` to `setup` and every `apply`, a repository holding a copy of `nginx-unprivileged:1.31.6-alpine` that no resource of the cluster covers (`task acceptance-get -- --kubeconfig <path> --context <name> imagealternative`, then `imagemirror`, lists what they cover). On a cluster whose egress IP is shared, prefer a registry without an anonymous pull quota: the tests check and copy it every minute.

Apply the 2 test namespaces first, once per run:

```sh
task acceptance-apply -- --kubeconfig <path> --context <name> --file test/acceptance/fixtures/00-namespaces.yaml
```

`kuik-test` carries `kuik.enix.io/test: "true"` and every test CR selects it; `kuik-test-unlabeled` is selected by none, so a pod there is out of scope. When done, and only then, `task acceptance-cleanup -- --kubeconfig <path> --context <name>` removes every test object.

## 1. The release is installed and healthy

**Since**: 3.0.0-alpha.2

**The user** installs or upgrades kuik to the release with Helm, then looks at what runs.

**They expect**:

- the Helm release deployed at the version;
- every kuik pod Running and Ready, never restarted, with no error line in its logs;
- `kuik_build_info` on every webhook replica, at the version and at the commit of the tag ([observability](../../docs/v3/observability.md#metrics)).

**Prepare**: nothing; `git fetch --tags` so the commit of the tag is known.

**Do**: nothing.

**Check**: one command shows the 3 points, in its sections "Helm release", "kuik pods" and "kuik_build_info of each webhook replica":

```sh
task acceptance-check -- --kubeconfig <path> --context <name> --kuik-version <version>
```

**Explore**: read the "verbosity" and "admission policies" sections: a policy that would reject a test pod explains a later failure before it happens.

## 2. A pod no kuik CR selects is left alone

**Since**: 3.0.0-alpha.2

**The user** runs a pod in a namespace where no kuik CR selects it.

**They expect**:

- the image unchanged and no `kuik.enix.io/` annotation on the pod ([Scope and filtering](../../docs/v3/spec.md#scope-and-filtering));
- the pod Running.

**Prepare**: [`02-no-cr.yaml`](./fixtures/02-no-cr.yaml):

```sh
task acceptance-apply -- --kubeconfig <path> --context <name> --file test/acceptance/fixtures/02-no-cr.yaml
```

**Do**: nothing.

**Check**:

- image and annotations: `task acceptance-get -- --kubeconfig <path> --context <name> -n kuik-test pod/no-cr --jsonpath '{.spec.containers[0].image}{"\n"}{.metadata.annotations}{"\n"}'`
- phase: `task acceptance-get -- --kubeconfig <path> --context <name> -n kuik-test pod/no-cr --jsonpath '{.status.phase}'`

**Explore**: a pod with several containers or an init container: none of them may be touched.

## 3. An OnFailure alternative reroutes a pod whose origin is down

**Since**: 3.0.0-alpha.2

**The user** declares an `OnFailure` ImageAlternative for an image, then runs a pod whose origin registry never answers.

**They expect**:

- the pod rewritten to `<public-repo>:1.31.6-alpine`, the first candidate that answers ([ImageAlternative](../../docs/v3/spec.md#imagealternative));
- the `kuik.enix.io/rewrites` annotation naming `ImageAlternative/kuik-test-nginx` ([Annotations](../../docs/v3/observability.md#annotations));
- the pod Running;
- an `ImageFallback` event on the pod ([Catalogue](../../docs/v3/observability.md#catalogue));
- `kuik_routing_rewrites_total` of `kuik-test-nginx`, summed over the webhook replicas, one above its value before the pod (only the replica that served the admission counts it) ([Counters](../../docs/v3/observability.md#counters--cumulative-safe-to-sum-across-replicas)).

**Prepare**: [`03-fallback.yaml`](./fixtures/03-fallback.yaml), shared with scenarios 7 and 12. First note the counter of every webhook replica (absent counts as 0); `acceptance-setup` prints it for you:

```sh
task acceptance-inspect -- --kubeconfig <path> --context <name> --metrics kuik_routing_rewrites_total
task acceptance-apply -- --kubeconfig <path> --context <name> --file test/acceptance/fixtures/03-fallback.yaml
```

**Do**: wait a minute.

**Check**:

- image and annotation: `task acceptance-get -- --kubeconfig <path> --context <name> -n kuik-test pod/fallback --jsonpath '{.spec.containers[0].image}{"\n"}{.metadata.annotations.kuik\.enix\.io/rewrites}{"\n"}'`
- phase: `task acceptance-get -- --kubeconfig <path> --context <name> -n kuik-test pod/fallback --jsonpath '{.status.phase}'`
- event: the `ImageFallback` line of `Pod/kuik-test/fallback` in the section "events" of `task acceptance-inspect -- --kubeconfig <path> --context <name>`
- counter, per replica: `task acceptance-inspect -- --kubeconfig <path> --context <name> --metrics 'kuik_routing_rewrites_total.*kuik-test-nginx'`

**Explore**: swap the order of the 2 alternatives, or switch `rewritePolicy` to `Always`, then create a new pod: which candidate wins, and does the annotation say which policy placed it?

## 7. The ImageAlternative status tells what it did

**Since**: 3.0.0-alpha.3

**The user** reads the status of the ImageAlternative of scenario 3.

**They expect** ([status](../../docs/v3/status.md#imagealternative), [conditions](../../docs/v3/status.md#conditions-and-their-reasons)):

- `Ready` `True`, reason `IsReady`;
- `FallbackActive` `True`, reason `OriginUnavailable`;
- `activeFallbacks` holding the rewrite of scenario 3;
- the container and pod counts matching the pods of its fixture, `fallback` rewritten and `origin-up` kept on its origin, which answers: pods `tracked` 2, `rewritten` 1; containers `tracked` 2, `rewritten` 1, `untouched` 1. The pod `out-of-scope`, in `kuik-test-unlabeled`, is not tracked.

**Prepare**: the fixture of [scenario 3](#3-an-onfailure-alternative-reroutes-a-pod-whose-origin-is-down).

**Do**: wait a minute: the status is written a few seconds after the pods.

**Check**:

- conditions: `task acceptance-get -- --kubeconfig <path> --context <name> imagealternative/kuik-test-nginx --jsonpath '{range .status.conditions[*]}{.type} {.status} {.reason}{"\n"}{end}'`
- fallbacks and counts: `task acceptance-get -- --kubeconfig <path> --context <name> imagealternative/kuik-test-nginx --jsonpath '{.status.activeFallbacks}{"\n"}{.status.pods}{"\n"}{.status.containers}{"\n"}'`

**Explore**: delete the pod `fallback` (`task acceptance-delete -- --kubeconfig <path> --context <name> pod/fallback`): `activeFallbacks` and `FallbackActive` must follow.

## 8. A declared credential becomes a pull Secret before any pod

**Since**: 3.0.0-alpha.3

**The user** declares an `Always` ImageAlternative whose candidate has `auth.secretRef`, with the source Secret in the kuik namespace.

**They expect** ([`injectPullSecret`](../../docs/v3/spec.md#injectpullsecret), [the name of an injected Secret](../../docs/v3/architecture.md#the-name-of-an-injected-secret)):

- the Secret `kuik-inject-imagealternative-kuik-test-pull-secret` in `kuik-test`, of type `kubernetes.io/dockerconfigjson`, before any pod exists, owned by the CR;
- no such Secret in `kuik-test-unlabeled`;
- the CR `Ready` `True`, reason `IsReady`.

**Prepare**: [`08-pull-auth.yaml`](./fixtures/08-pull-auth.yaml); its placeholder source Secret goes to `${KUIK_NAMESPACE}`, filled from `--kuik-namespace`:

```sh
task acceptance-apply -- --kubeconfig <path> --context <name> --file test/acceptance/fixtures/08-pull-auth.yaml
```

**Do**: wait a minute.

**Check**:

- type and owner: `task acceptance-get -- --kubeconfig <path> --context <name> -n kuik-test secret/kuik-inject-imagealternative-kuik-test-pull-secret`
- no pod selected by it (empty expected): `task acceptance-get -- --kubeconfig <path> --context <name> -n kuik-test pod -l kuik-test/routing=pull-secret --jsonpath '{.items[*].metadata.name}'`
- absent elsewhere (`NotFound` expected): `task acceptance-get -- --kubeconfig <path> --context <name> -n kuik-test-unlabeled secret/kuik-inject-imagealternative-kuik-test-pull-secret`
- condition: `task acceptance-get -- --kubeconfig <path> --context <name> imagealternative/kuik-test-pull-secret --jsonpath '{range .status.conditions[*]}{.type} {.status} {.reason}{"\n"}{end}'`

**Explore**: delete the injected Secret (`task acceptance-delete -- --kubeconfig <path> --context <name> secret/kuik-inject-imagealternative-kuik-test-pull-secret`): does it come back, and how fast?

## 11. A pod pulls from a private registry with the declared credential

**Since**: 3.0.0-alpha.3

**The user** declares an `Always` ImageAlternative to their private repository with their own docker config Secret, then runs a pod it selects.

**They expect** ([Authentication](../../docs/v3/spec.md#authentication)):

- the pod rewritten to `<private-repo>:1.31.6-alpine`;
- `imagePullSecrets` holding `kuik-inject-imagealternative-kuik-test-private`;
- a `Pulled` event: the pod pulls `Always`, so a cached image cannot hide a failed pull;
- the pod Running.

**Prepare**: a private repository holding a copy of `quay.io/nginx/nginx-unprivileged:1.31.6-alpine`, and a docker config Secret in the kuik namespace that can pull it, created by the user. Apply [`11-private-alternative.yaml`](./fixtures/11-private-alternative.yaml), wait for its pull Secret, then [`11-private-pod.yaml`](./fixtures/11-private-pod.yaml):

```sh
task acceptance-apply -- --kubeconfig <path> --context <name> --private-repo <private-repo> --pull-auth <secret-name> --file test/acceptance/fixtures/11-private-alternative.yaml
task acceptance-get -- --kubeconfig <path> --context <name> -n kuik-test secret/kuik-inject-imagealternative-kuik-test-private
task acceptance-apply -- --kubeconfig <path> --context <name> --file test/acceptance/fixtures/11-private-pod.yaml
```

**Do**: wait a minute.

**Check**:

- image and pull Secrets: `task acceptance-get -- --kubeconfig <path> --context <name> -n kuik-test pod/private --jsonpath '{.spec.containers[0].image}{"\n"}{.spec.imagePullSecrets[*].name}{"\n"}'`
- event: the `Pulled` line of `Pod/kuik-test/private` in the section "events" of `task acceptance-inspect -- --kubeconfig <path> --context <name>`
- phase: `task acceptance-get -- --kubeconfig <path> --context <name> -n kuik-test pod/private --jsonpath '{.status.phase}'`

**Explore**: a pod that already lists its own `imagePullSecrets`: kuik appends its Secret to them, never replaces them ([the name of an injected Secret](../../docs/v3/architecture.md#the-name-of-an-injected-secret)). A registry under a real corporate CA, declared in the `tls` key of the kuik config with a `caSecretRef` holding that CA ([Registry TLS](../../docs/configuration.md#registry-tls)): its images are available, and a missing or invalid CA Secret makes them unreachable, never verified more weakly. Without the private repository, the scenario is reported as not tested.

## 12. An ImageMonitor tracks the images of its pods and reports the one that fails

**Since**: 3.0.0-alpha.4

**The user** declares an ImageMonitor on the pods `fallback` and `origin-up` of scenario 3: `fallback` declares an origin that never resolves (kuik rerouted it), `origin-up` one that answers.

**They expect** ([ImageMonitor](../../docs/v3/spec.md#imagemonitor), [status](../../docs/v3/status.md#imagemonitor), [conditions](../../docs/v3/status.md#conditions-and-their-reasons), [Anomalies](../../docs/v3/observability.md#anomalies-signal-by-presence)):

- `status.images.origin` at `tracked` 2, `running` 1, `standby` 1 (the origin of `fallback`, routed elsewhere), `retained` 0; the pod `out-of-scope`, in `kuik-test-unlabeled`, is not tracked;
- once the rings have checked both images, `available` 1 and `unavailable` 1;
- `status.unavailableImages` holding `kuik-test.invalid/nginx/nginx-unprivileged:1.31.6-alpine`, reason `Unreachable`, `pods` 1;
- `ImagesUnavailable` `True`, reason `ChecksFailed`; `Ready` `True`, reason `IsReady`;
- `status.checks.registries` with an entry for the host of `<public-repo>` and one for `kuik-test.invalid`;
- `kuik_image_origin_unavailable` for the image that fails, and no such series for the `<public-repo>` one;
- since 3.0.0-alpha.5, no drift on a fixed tag with `driftDetection` at its default (`true`): `status.images.origin.drifted` 0, no `status.driftedImages`, no `ImagesDrifted` condition (absent: kuik removes an anomaly condition once it no longer holds), and no `ImageTagDrifted` or `ImageClusterSkew` event ([Events](../../docs/v3/observability.md#events)). A drift reported here means upstream rebuilt `1.31.6-alpine`, or a node whose runtime reports a platform manifest or a config digest in `imageID` ([ImageMonitor fields](../../docs/crds.md#fields-2)): check the digests before calling it a bug.

**Prepare**: the fixture of [scenario 3](#3-an-onfailure-alternative-reroutes-a-pod-whose-origin-is-down), then [`12-monitor.yaml`](./fixtures/12-monitor.yaml):

```sh
task acceptance-apply -- --kubeconfig <path> --context <name> --file test/acceptance/fixtures/12-monitor.yaml
```

**Do**: wait 5 minutes: each registry opens one check window per minute by default, shared with the real ImageMonitors of that host ([Scheduling](../../docs/v3/spec.md#scheduling)).

**Check**:

- counts and unavailable images: `task acceptance-get -- --kubeconfig <path> --context <name> imagemonitor/kuik-test-monitor --jsonpath '{.status.images.origin}{"\n"}{.status.unavailableImages}{"\n"}'`
- conditions: `task acceptance-get -- --kubeconfig <path> --context <name> imagemonitor/kuik-test-monitor --jsonpath '{range .status.conditions[*]}{.type} {.status} {.reason}{"\n"}{end}'`
- rings: `task acceptance-get -- --kubeconfig <path> --context <name> imagemonitor/kuik-test-monitor --jsonpath '{.status.checks.registries}'`
- metric: `task acceptance-inspect -- --kubeconfig <path> --context <name> --metrics 'kuik_image_origin_unavailable.*kuik-test-monitor'`
- drift (since 3.0.0-alpha.5): `task acceptance-get -- --kubeconfig <path> --context <name> imagemonitor/kuik-test-monitor --jsonpath '{.status.images.origin.drifted}{"\n"}{.status.driftedImages}{"\n"}'`, and the events of `task acceptance-inspect -- --kubeconfig <path> --context <name> --grep kuik-test-monitor`

**Explore**: delete the pod `origin-up` (`task acceptance-delete -- --kubeconfig <path> --context <name> pod/origin-up`): its image must move to `status.retainedImages` with `unusedSince` and a digest, and count as `retained` ([status](../../docs/v3/status.md#imagemonitor)). Does a `cycleDuration` appear once a lap completes, and is it close to what the shared windows of that host allow?

## 13. A private image checked anonymously is announced

**Since**: 3.0.0-alpha.4

**The user** monitors a pod that declares their private repository without any pull Secret.

**They expect** ([Authentication](../../docs/v3/spec.md#authentication), [Fallback credentials](../../docs/v3/spec.md#fallback-credentials), [status](../../docs/v3/status.md#imagemonitor)):

- `status.unavailableImages` holding `<private-repo>:1.31.6-alpine`, reason `Unauthorized`;
- one `AnonymousAccessDenied` event on the CR, not repeated while the image stays unavailable (an event `docs/v3/` does not list, documented in [observability](../../docs/observability.md));
- `Ready` `True`, reason `IsReady`: a credential missing for one image never touches `Ready` ([conditions](../../docs/v3/status.md#conditions-and-their-reasons));
- the pod in `ErrImagePull` or `ImagePullBackOff`, as without kuik.

**Prepare**: the private repository and `--pull-auth` of [scenario 11](#11-a-pod-pulls-from-a-private-registry-with-the-declared-credential), then [`13-anonymous-denied.yaml`](./fixtures/13-anonymous-denied.yaml). The scenario is reported as not tested when the kuik config holds a `fallbackAuth` entry matching the private repository (kuik checks with it, no event is expected), or when a real ImageMonitor also tracks the private image (its ring may check first and receive the event). A registry that answers an anonymous caller with 404 instead of 401 gives `ManifestNotFound` and no event: report it as such.

```sh
task acceptance-apply -- --kubeconfig <path> --context <name> --private-repo <private-repo> --pull-auth <secret-name> --file test/acceptance/fixtures/13-anonymous-denied.yaml
```

**Do**: wait 5 minutes.

**Check**:

- unavailable image and conditions: `task acceptance-get -- --kubeconfig <path> --context <name> imagemonitor/kuik-test-anonymous --jsonpath '{.status.unavailableImages}{"\n"}{range .status.conditions[*]}{.type} {.status} {.reason}{"\n"}{end}'`
- event (section "events"): `task acceptance-inspect -- --kubeconfig <path> --context <name> --grep kuik-test-anonymous`
- pull failure: `task acceptance-get -- --kubeconfig <path> --context <name> -n kuik-test pod/anonymous --jsonpath '{.status.containerStatuses[0].state.waiting.reason}'`

**Explore**: none worth it on a shared cluster: giving the pod a pull Secret would mean copying a credential, which the test never does. Without the private repository, the scenario is reported as not tested.

## 14. An ImageMirror copies an image to its destination

**Since**: 3.0.0-alpha.4

**The user** declares an ImageMirror with `rewritePolicy: None` to a registry path they dedicate to the test, with push credentials, then runs a pod it selects.

**They expect** ([ImageMirror](../../docs/v3/spec.md#imagemirror), [status](../../docs/v3/status.md#imagemirror), [conditions](../../docs/v3/status.md#conditions-and-their-reasons), [Catalogue](../../docs/v3/observability.md#catalogue), [Counters](../../docs/v3/observability.md#counters--cumulative-safe-to-sum-across-replicas)):

- the pod on its origin image, with no `kuik.enix.io/rewrites` annotation: `None` copies and never routes;
- one `ImageCopied` event on the CR;
- `status.images.copy` at `tracked` 1, `unavailable` 0, and no `failedImageCopies`;
- `status.repositories` holding one repository under the prefix;
- `DestinationOutOfSync` absent: kuik removes an anomaly condition once it no longer holds; `Ready` `True`, reason `IsReady`;
- `kuik_mirror_copies_total` of the CR at 1, reason `Initial`, on the reconciler.

**Prepare**: a registry path dedicated to the test, that nothing else uses: kuik deletes the tags of this cluster it finds under it. A docker config Secret `kuik-test-mirror-push` in the kuik namespace that can push there and delete tags, created by the user: cleanup is on, so a destination that refuses tag deletion holds the CR at deletion and `acceptance-cleanup` fails. Then [`14-mirror-copy.yaml`](./fixtures/14-mirror-copy.yaml):

```sh
task acceptance-apply -- --kubeconfig <path> --context <name> --mirror-prefix <prefix> --file test/acceptance/fixtures/14-mirror-copy.yaml
```

**Do**: wait 5 minutes: the copy waits for a copy window of the host of `<public-repo>`.

**Check**:

- image and annotations: `task acceptance-get -- --kubeconfig <path> --context <name> -n kuik-test pod/mirror-copy --jsonpath '{.spec.containers[0].image}{"\n"}{.metadata.annotations}{"\n"}'`
- counts, failures and repositories: `task acceptance-get -- --kubeconfig <path> --context <name> imagemirror/kuik-test-mirror-copy --jsonpath '{.status.images.copy}{"\n"}{.status.failedImageCopies}{"\n"}{.status.repositories}{"\n"}'`
- conditions: `task acceptance-get -- --kubeconfig <path> --context <name> imagemirror/kuik-test-mirror-copy --jsonpath '{range .status.conditions[*]}{.type} {.status} {.reason}{"\n"}{end}'`
- event (section "events") and counter: `task acceptance-inspect -- --kubeconfig <path> --context <name> --grep kuik-test-mirror-copy --metrics 'kuik_mirror_copies_total.*kuik-test-mirror-copy'`

**Explore**: the tag in the registry carries this cluster's `clusterID` suffix ([Multi-cluster](../../docs/v3/spec.md#multi-cluster-shared-destination-one-tag-per-cluster)); `selfChecked` appears within one `mirror.destinationScan.interval` (1 hour by default). Delete the CR (`task acceptance-delete -- --kubeconfig <path> --context <name> imagemirror/kuik-test-mirror-copy`): it is held until no pod runs its copies, then its tags are deleted and it goes ([Deleting an ImageMirror](../../docs/crds.md#deleting-an-imagemirror)). Without `--mirror-prefix`, the scenario is reported as not tested.

## 17. Nobody but the secret syncer writes a Secret under its reserved prefix

**Since**: 3.0.0-alpha.5

**The user** creates a Secret named `kuik-inject-*` in a namespace of theirs.

**They expect** ([Bounding what the syncer may write](../../docs/v3/architecture.md#bounding-what-the-syncer-may-write)):

- the chart installs a ValidatingAdmissionPolicy and its binding, both named `<fullname>-secret-syncer` (`kube-image-keeper-secret-syncer` for the default release), the binding with `validationActions` `[Deny]`;
- the API server refuses the Secret, with `the names kuik-inject-* and kuik-provider-* are reserved to the kuik secret syncer, <user> may not write kuik-test/kuik-inject-kuik-test-reserved`; nothing is created;
- the pull Secret the syncer wrote in [scenario 11](#11-a-pod-pulls-from-a-private-registry-with-the-declared-credential) still there: the policy bounds its writes, it does not block them. Without `--private-repo`, the one of [scenario 8](#8-a-declared-credential-becomes-a-pull-secret-before-any-pod) shows the same.

**Prepare**: nothing: `setup` never applies [`17-reserved-prefix.yaml`](./fixtures/17-reserved-prefix.yaml), since the API server must refuse it.

**Do**: apply it by hand; the task must fail:

```sh
task acceptance-apply -- --kubeconfig <path> --context <name> --file test/acceptance/fixtures/17-reserved-prefix.yaml
```

**Check**:

- policy: `task acceptance-get -- --kubeconfig <path> --context <name> validatingadmissionpolicy/<fullname>-secret-syncer --jsonpath '{.metadata.name}'`
- binding and its actions: `task acceptance-get -- --kubeconfig <path> --context <name> validatingadmissionpolicybinding/<fullname>-secret-syncer --jsonpath '{.spec.policyName} {.spec.validationActions}'`
- nothing created: `task acceptance-get -- --kubeconfig <path> --context <name> -n kuik-test secret/kuik-inject-kuik-test-reserved` answers `NotFound`
- the syncer still writes: `task acceptance-get -- --kubeconfig <path> --context <name> -n kuik-test secret/kuik-inject-imagealternative-kuik-test-private`, or without `--private-repo` `task acceptance-get -- --kubeconfig <path> --context <name> -n kuik-test secret/kuik-inject-imagealternative-kuik-test-pull-secret`

**Explore**: the refusal comes from the policy, not from RBAC: the error names the policy and its message, never `forbidden: User ... cannot create`. A Secret that slipped through means the policy is missing or not bound: report it, `acceptance-cleanup` removes it with the namespace.

## 18. An upgrade keeps the cluster ID of the install

**Since**: 3.0.0-alpha.6

**The user** upgrades from a release whose values set `config.clusterID`, following [Upgrading from a configured `clusterID`](../../docs/configuration.md#upgrading-from-a-configured-clusterid): they create the ConfigMap `kuik-cluster-id` with the old value, then upgrade without `config.clusterID`. On a fresh install, the reconciler generates the ID instead.

**They expect** ([Cluster ID](../../docs/configuration.md#cluster-id), [Tag naming constraints](../../docs/v3/spec.md#tag-naming-constraints)):

- `helm template` with `--set config.clusterID=<value>` failing, with an error that names `kuik-cluster-id` and the upgrade section;
- the ConfigMap `kuik-cluster-id` in the kuik namespace, holding the old value (or a generated `<adjective>-<surname>-<4 hex>` on a fresh install), never rewritten by the upgrade;
- the webhook and the reconciler Ready, as in [scenario 1](#1-the-release-is-installed-and-healthy);
- the mirror candidate of the ImageMirror `kuik-test-mirror`, whose destination `registry.invalid` never answers, tagged `1.31.6-alpine_<clusterID>`, with the value of the ConfigMap.

**Prepare**: [`06-mirror.yaml`](./fixtures/06-mirror.yaml), applied by `setup`. The upgrade itself is done before the run, by whoever installs the release.

```sh
task acceptance-apply -- --kubeconfig <path> --context <name> --file test/acceptance/fixtures/06-mirror.yaml
```

**Do**: nothing.

**Check**:

- chart refusal, no cluster needed: `helm template kube-image-keeper helm/kube-image-keeper --set config.clusterID=dev`
- the ID: `task acceptance-get -- --kubeconfig <path> --context <name> -n <kuik-namespace> configmap/kuik-cluster-id --jsonpath '{.data.clusterID}{"\n"}{.metadata.creationTimestamp}{"\n"}'`
- the suffix: `task acceptance-inspect -- --kubeconfig <path> --context <name> --grep registry.invalid`, the candidate of `Candidate failed its check`

**Explore**: the webhook reads the ID once at startup and reports not ready until it has; its readiness probe holds the check `cluster-id` next to `registry-tls`.

## 23. An ImageMirror keeps its spec as written

**Since**: 3.0.0-beta.1

**The user** declares an ImageMirror without `cleanup.retention`, as a GitOps tool would apply it, and lets kuik add its finalizer.

**They expect** ([ImageMirror](../../docs/v3/spec.md#imagemirror), [Collecting unused tags](../../docs/v3/spec.md#collecting-unused-tags), [ImageMirror fields](../../docs/crds.md#fields-1)):

- the finalizer `kuik.enix.io/mirror-cleanup` on the CR;
- `metadata.generation` 1: adding the finalizer never writes the spec back, so the defaulted `retention` stays `168h` as the API server stored it, and a GitOps tool sees no drift.

**Prepare**: [`06-mirror.yaml`](./fixtures/06-mirror.yaml), applied once: an `apply` of a changed file is a spec change and bumps the generation.

**Do**: wait a minute.

**Check**: `task acceptance-get -- --kubeconfig <path> --context <name> imagemirror/kuik-test-mirror --jsonpath '{.metadata.finalizers}{"\n"}{.metadata.generation}{"\n"}'`

`acceptance-get` reads the CR through the kuik API types, so it prints `spec.cleanup.retention` as `168h0m0s` whatever the API server stored: the generation is the check. A spec written back as `168h0m0s` is a change the API server counts, so it would read 2.

**Explore**: delete the CR (`task acceptance-delete -- --kubeconfig <path> --context <name> imagemirror/kuik-test-mirror`): the finalizer is removed the same way, and the CR goes within a minute, since no pod runs its copies and the fixture turns cleanup off ([Deleting an ImageMirror](../../docs/crds.md#deleting-an-imagemirror)).

## 24. kuik_registry_requests_total is exported, without mirror destinations

**Since**: 3.0.0-beta.1

**The user** reads the metrics of the webhook and the reconciler once pods were routed and an image copied.

**They expect** ([Per registry](../../docs/v3/observability.md#per-registry--the-granularity-an-incident-calls-for), [Registry requests](../../docs/observability.md#registry-requests)):

- on the reconciler, `kuik_registry_requests_total` series for the host of `<public-repo>`: the copies of the ImageMirror `kuik-test-mirror` read it, and the ImageMonitor of scenario 12 checks it;
- on the webhook replica that served the admission of scenario 3, series for the host of `<public-repo>`, the candidate that answered;
- no series with `registry="registry.invalid"`, on any pod: it is the destination of `kuik-test-mirror`, which kuik checks and pushes to, but a mirror destination is not counted. Series for `kuik-test.invalid`, a source of scenarios 3 and 12, are expected.

**Prepare**: the fixture of [scenario 3](#3-an-onfailure-alternative-reroutes-a-pod-whose-origin-is-down) and [`06-mirror.yaml`](./fixtures/06-mirror.yaml), the copy of `kuik-test-mirror` attempted: it waits for a copy window of the host of `<public-repo>`, 3 minutes apart by default ([Scheduling](../../docs/v3/spec.md#scheduling)).

**Do**: nothing.

**Check**: `task acceptance-inspect -- --kubeconfig <path> --context <name> --metrics 'kuik_registry_requests_total'`, per pod; the same with `--metrics 'kuik_registry_requests_total.*registry="registry\.invalid"'` prints no series line (`kuik_image_copy_failed` of `kuik-test-mirror` names that registry, as expected).

**Explore**: the `operation` and `result` of each series against what the scenarios did: `Check` and `Copy` on the reconciler, `Check` only on the webhook, `Unreachable` for `kuik-test.invalid`. A `QuotaExceeded` on a real registry says the shared egress IP is rate-limited.

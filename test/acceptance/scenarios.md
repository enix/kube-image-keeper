# Acceptance scenarios

What a kuik v3 release must show on a real cluster, one scenario per test, numbered like the acceptance report. Each one is a user story a human can run by hand; the [`release-acceptance`](../../.claude/skills/release-acceptance/SKILL.md) skill runs every scenario whose **Since** is not newer than the version under test, then explores. A scenario is the floor of a run, not its ceiling. New scenarios come from the [`acceptance-scenarios`](../../.claude/skills/acceptance-scenarios/SKILL.md) skill.

## Before any scenario

In every command, `<path>` is the kubeconfig file and `<name>` its context. Add `--kuik-namespace <ns>` to a task when kuik is not installed in `kuik-system`, and `--release <release>` when its Helm release is not `kube-image-keeper`. The `kubectl` commands only read, and never a Secret's data.

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

- image and annotations: `KUBECONFIG=<path> kubectl --context <name> get pod -n kuik-test no-cr -o jsonpath='{.spec.containers[0].image}{"\n"}{.metadata.annotations}{"\n"}'`
- phase: `KUBECONFIG=<path> kubectl --context <name> get pod -n kuik-test no-cr -o jsonpath='{.status.phase}'`

**Explore**: a pod with several containers or an init container: none of them may be touched.

## 3. An OnFailure alternative reroutes a pod whose origin is down

**Since**: 3.0.0-alpha.2

**The user** declares an `OnFailure` ImageAlternative for an image, then runs a pod whose origin registry never answers.

**They expect**:

- the pod rewritten to `quay.io/nginx/nginx-unprivileged:1.31.6-alpine`, the first candidate that answers ([ImageAlternative](../../docs/v3/spec.md#imagealternative));
- the `kuik.enix.io/rewrites` annotation naming `ImageAlternative/kuik-test-nginx` ([Annotations](../../docs/v3/observability.md#annotations));
- the pod Running;
- an `ImageFallback` event on the pod ([Catalogue](../../docs/v3/observability.md#catalogue));
- `kuik_routing_rewrites_total` of `kuik-test-nginx`, summed over the webhook replicas, one above its value before the pod (only the replica that served the admission counts it) ([Counters](../../docs/v3/observability.md#counters--cumulative-safe-to-sum-across-replicas)).

**Prepare**: [`03-fallback.yaml`](./fixtures/03-fallback.yaml), shared with scenarios 4, 5, 7 and 12. First note the counter of every webhook replica (absent counts as 0); `acceptance-setup` prints it for you:

```sh
task acceptance-inspect -- --kubeconfig <path> --context <name> --metrics kuik_routing_rewrites_total
task acceptance-apply -- --kubeconfig <path> --context <name> --file test/acceptance/fixtures/03-fallback.yaml
```

**Do**: wait a minute.

**Check**:

- image and annotation: `KUBECONFIG=<path> kubectl --context <name> get pod -n kuik-test fallback -o jsonpath='{.spec.containers[0].image}{"\n"}{.metadata.annotations.kuik\.enix\.io/rewrites}{"\n"}'`
- phase: `KUBECONFIG=<path> kubectl --context <name> get pod -n kuik-test fallback -o jsonpath='{.status.phase}'`
- event: `KUBECONFIG=<path> kubectl --context <name> get events -n kuik-test --field-selector involvedObject.name=fallback,reason=ImageFallback`
- counter, per replica: `task acceptance-inspect -- --kubeconfig <path> --context <name> --metrics 'kuik_routing_rewrites_total.*kuik-test-nginx'`

**Explore**: swap the order of the 2 alternatives, or switch `rewritePolicy` to `Always`, then create a new pod: which candidate wins, and does the annotation say which policy placed it?

## 4. An OnFailure alternative keeps a pod whose origin answers

**Since**: 3.0.0-alpha.2

**The user** runs a pod the same ImageAlternative selects, whose origin answers.

**They expect**:

- the image unchanged and no `kuik.enix.io/rewrites` annotation ([ImageAlternative](../../docs/v3/spec.md#imagealternative));
- the webhook log line `Kept container image` for the pod (at `debug` verbosity).

**Prepare**: the fixture of [scenario 3](#3-an-onfailure-alternative-reroutes-a-pod-whose-origin-is-down), pod `origin-up`.

**Do**: nothing.

**Check**:

- image and annotations: `KUBECONFIG=<path> kubectl --context <name> get pod -n kuik-test origin-up -o jsonpath='{.spec.containers[0].image}{"\n"}{.metadata.annotations}{"\n"}'`
- log line: `task acceptance-inspect -- --kubeconfig <path> --context <name> --grep origin-up`

**Explore**: pin the pod's image by digest: the origin answers, so it must stay as written ([Digest-pinned images](../../docs/v3/spec.md#digest-pinned-images)).

## 5. A pod outside the selected namespaces is never rerouted

**Since**: 3.0.0-alpha.2

**The user** runs a pod with the label the ImageAlternative selects, in a namespace it does not select, from an origin that never answers.

**They expect**:

- the image unchanged and no `kuik.enix.io/` annotation ([Scope and filtering](../../docs/v3/spec.md#scope-and-filtering));
- the pull failing as it would without kuik (`ErrImagePull`, then `ImagePullBackOff`).

**Prepare**: the fixture of [scenario 3](#3-an-onfailure-alternative-reroutes-a-pod-whose-origin-is-down), pod `out-of-scope` in `kuik-test-unlabeled`.

**Do**: wait a minute.

**Check**:

- image and annotations: `KUBECONFIG=<path> kubectl --context <name> get pod -n kuik-test-unlabeled out-of-scope -o jsonpath='{.spec.containers[0].image}{"\n"}{.metadata.annotations}{"\n"}'`
- pull failure: `KUBECONFIG=<path> kubectl --context <name> get pod -n kuik-test-unlabeled out-of-scope -o jsonpath='{.status.containerStatuses[0].state.waiting.reason}'`

**Explore**: none worth it; the guard keeps every test CR on `kuik-test`.

## 6. An ImageMirror whose destination is down leaves the pod on its origin

**Since**: 3.0.0-alpha.2

**The user** declares an `Always` ImageMirror whose destination registry never answers, then runs a pod it selects.

**They expect**:

- the pod on its origin image, with no `kuik.enix.io/rewrites` annotation ([ImageMirror](../../docs/v3/spec.md#imagemirror), [Candidate ordering](../../docs/v3/spec.md#candidate-ordering));
- the webhook log showing the destination candidate failing its check (`Candidate failed its check`, at `debug` verbosity).

Since 3.0.0-alpha.4, the mirror tries to copy the image and fails:

- `status.failedImageCopies` holding `quay.io/nginx/nginx-unprivileged:1.31.6-alpine`, reason `DestinationUnreachable`, and `status.images.copy` at `tracked` 1, `unavailable` 1 ([status](../../docs/v3/status.md#imagemirror));
- `DestinationOutOfSync` `True`, reason `MissingImages`; `Ready` `True`, reason `IsReady`: a failing image never touches `Ready` ([conditions](../../docs/v3/status.md#conditions-and-their-reasons));
- an `ImageCopyFailed` event on the CR ([Catalogue](../../docs/v3/observability.md#catalogue));
- `kuik_image_copy_failed` for the image, with the destination as `registry` ([Anomalies](../../docs/v3/observability.md#anomalies-signal-by-presence)).

**Prepare**: [`06-mirror.yaml`](./fixtures/06-mirror.yaml):

```sh
task acceptance-apply -- --kubeconfig <path> --context <name> --file test/acceptance/fixtures/06-mirror.yaml
```

**Do**: wait 5 minutes: a copy waits for a copy window of quay.io, 3 minutes apart by default ([Scheduling](../../docs/v3/spec.md#scheduling)).

**Check**:

- image and annotations: `KUBECONFIG=<path> kubectl --context <name> get pod -n kuik-test mirror -o jsonpath='{.spec.containers[0].image}{"\n"}{.metadata.annotations}{"\n"}'`
- log line: `task acceptance-inspect -- --kubeconfig <path> --context <name> --grep registry.invalid`
- copy failure and counts: `KUBECONFIG=<path> kubectl --context <name> get imagemirror kuik-test-mirror -o jsonpath='{.status.failedImageCopies}{"\n"}{.status.images.copy}{"\n"}'`
- conditions: `KUBECONFIG=<path> kubectl --context <name> get imagemirror kuik-test-mirror -o jsonpath='{range .status.conditions[*]}{.type} {.status} {.reason}{"\n"}{end}'`
- event (section "events") and metric: `task acceptance-inspect -- --kubeconfig <path> --context <name> --grep kuik-test-mirror --metrics 'kuik_image_copy_failed.*kuik-test-mirror"'`

**Explore**: a pod with `imagePullPolicy: Always` on the mirrored image ([`imagePullPolicy: Always` demotes a mirror](../../docs/v3/spec.md#imagepullpolicy-always-demotes-a-mirror)). This mirror never completes its copy: read `failedImageCopies[0].lastAttempt` across 2 copy windows of quay.io; it is refreshed at every retry ([status](../../docs/v3/status.md#imagemirror)), so a value that never moves means the copy is not retried. Delete the CR (`task acceptance-delete -- --kubeconfig <path> --context <name> imagemirror/kuik-test-mirror`): no pod runs its copies and cleanup is off, so it must be gone within a minute ([Deleting an ImageMirror](../../docs/crds.md#deleting-an-imagemirror)). The fixture turns cleanup off because the mirror records its repository before the push fails, and a finalizer listing the tags of a host that never answers would hold the CR.

## 7. The ImageAlternative status tells what it did

**Since**: 3.0.0-alpha.3

**The user** reads the status of the ImageAlternative of scenarios 3 to 5.

**They expect** ([status](../../docs/v3/status.md#imagealternative), [conditions](../../docs/v3/status.md#conditions-and-their-reasons)):

- `Ready` `True`, reason `IsReady`;
- `FallbackActive` `True`, reason `OriginUnavailable`;
- `activeFallbacks` holding the rewrite of scenario 3;
- the container and pod counts matching scenarios 3 and 4: pods `tracked` 2, `rewritten` 1; containers `tracked` 2, `rewritten` 1, `untouched` 1. The pod of scenario 5 is not tracked.

**Prepare**: the fixture of [scenario 3](#3-an-onfailure-alternative-reroutes-a-pod-whose-origin-is-down).

**Do**: wait a minute: the status is written a few seconds after the pods.

**Check**:

- conditions: `KUBECONFIG=<path> kubectl --context <name> get imagealternative kuik-test-nginx -o jsonpath='{range .status.conditions[*]}{.type} {.status} {.reason}{"\n"}{end}'`
- fallbacks and counts: `KUBECONFIG=<path> kubectl --context <name> get imagealternative kuik-test-nginx -o jsonpath='{.status.activeFallbacks}{"\n"}{.status.pods}{"\n"}{.status.containers}{"\n"}'`

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

- type and owner: `KUBECONFIG=<path> kubectl --context <name> get secret -n kuik-test kuik-inject-imagealternative-kuik-test-pull-secret -o jsonpath='{.type}{"\n"}{.metadata.ownerReferences}{"\n"}'`
- no pod selected by it: `KUBECONFIG=<path> kubectl --context <name> get pods -n kuik-test -l kuik-test/routing=pull-secret`
- absent elsewhere (`NotFound` expected): `KUBECONFIG=<path> kubectl --context <name> get secret -n kuik-test-unlabeled kuik-inject-imagealternative-kuik-test-pull-secret -o name`
- condition: `KUBECONFIG=<path> kubectl --context <name> get imagealternative kuik-test-pull-secret -o jsonpath='{range .status.conditions[*]}{.type} {.status} {.reason}{"\n"}{end}'`

**Explore**: delete the injected Secret (`task acceptance-delete -- --kubeconfig <path> --context <name> secret/kuik-inject-imagealternative-kuik-test-pull-secret`): does it come back, and how fast?

## 9. A missing source Secret is reported, not hidden

**Since**: 3.0.0-alpha.3

**The user** declares an ImageAlternative whose `auth.secretRef` names a Secret that does not exist.

**They expect** ([conditions](../../docs/v3/status.md#conditions-and-their-reasons), [Catalogue](../../docs/v3/observability.md#catalogue), [Anomalies](../../docs/v3/observability.md#anomalies-signal-by-presence)):

- `Ready` `False`, reason `SecretNotFound`;
- the events `ResourceNotReady` and `PullSecretInjectionFailed` on the CR;
- `kuik_resource_not_ready` for the CR.

**Prepare**: [`09-missing-source.yaml`](./fixtures/09-missing-source.yaml):

```sh
task acceptance-apply -- --kubeconfig <path> --context <name> --file test/acceptance/fixtures/09-missing-source.yaml
```

**Do**: wait a minute.

**Check**:

- condition: `KUBECONFIG=<path> kubectl --context <name> get imagealternative kuik-test-missing-secret -o jsonpath='{range .status.conditions[*]}{.type} {.status} {.reason}{"\n"}{end}'`
- events (section "events") and metric: `task acceptance-inspect -- --kubeconfig <path> --context <name> --grep kuik-test-missing-secret --metrics 'kuik_resource_not_ready.*kuik-test-missing-secret'`

**Explore**: create the missing Secret `kuik-test-absent-creds` in the kuik namespace with a placeholder, like the source of [`08-pull-auth.yaml`](./fixtures/08-pull-auth.yaml): `Ready` must turn `True` and the metric must disappear.

## 10. Deleting the CR removes its pull Secret

**Since**: 3.0.0-alpha.3

**The user** deletes the ImageAlternative of scenario 8.

**They expect**: its pull Secret gone from `kuik-test` within a minute; an `Always` pair has no grace period ([`injectPullSecret`](../../docs/v3/spec.md#injectpullsecret)).

**Prepare**: the fixture of [scenario 8](#8-a-declared-credential-becomes-a-pull-secret-before-any-pod), its pull Secret present.

**Do**:

```sh
task acceptance-delete -- --kubeconfig <path> --context <name> imagealternative/kuik-test-pull-secret
```

then wait a minute.

**Check** (`NotFound` expected): `KUBECONFIG=<path> kubectl --context <name> get secret -n kuik-test kuik-inject-imagealternative-kuik-test-pull-secret -o name`

**Explore**: the same with `rewritePolicy: OnFailure` and a pod it rewrites: the Secret appears only once the pod is rewritten; how long does it outlive the pod, then the CR, against the grace period of [walkthrough 03](../../docs/v3/walkthroughs/03-secret-syncer-reconciliation.md)?

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
KUBECONFIG=<path> kubectl --context <name> get secret -n kuik-test kuik-inject-imagealternative-kuik-test-private -o name
task acceptance-apply -- --kubeconfig <path> --context <name> --file test/acceptance/fixtures/11-private-pod.yaml
```

**Do**: wait a minute.

**Check**:

- image and pull Secrets: `KUBECONFIG=<path> kubectl --context <name> get pod -n kuik-test private -o jsonpath='{.spec.containers[0].image}{"\n"}{.spec.imagePullSecrets[*].name}{"\n"}'`
- event: `KUBECONFIG=<path> kubectl --context <name> get events -n kuik-test --field-selector involvedObject.name=private,reason=Pulled`
- phase: `KUBECONFIG=<path> kubectl --context <name> get pod -n kuik-test private -o jsonpath='{.status.phase}'`

**Explore**: a pod that already lists its own `imagePullSecrets`: kuik appends its Secret to them, never replaces them ([the name of an injected Secret](../../docs/v3/architecture.md#the-name-of-an-injected-secret)). Without the private repository, the scenario is reported as not tested.

## 12. An ImageMonitor tracks the images of its pods and reports the one that fails

**Since**: 3.0.0-alpha.4

**The user** declares an ImageMonitor on the pods of scenarios 3 and 4: `fallback` declares an origin that never resolves (kuik rerouted it), `origin-up` one that answers.

**They expect** ([ImageMonitor](../../docs/v3/spec.md#imagemonitor), [status](../../docs/v3/status.md#imagemonitor), [conditions](../../docs/v3/status.md#conditions-and-their-reasons), [Anomalies](../../docs/v3/observability.md#anomalies-signal-by-presence)):

- `status.images.origin` at `tracked` 2, `running` 1, `standby` 1 (the origin of `fallback`, routed elsewhere), `retained` 0; the pod of scenario 5 is not tracked;
- once the rings have checked both images, `available` 1 and `unavailable` 1;
- `status.unavailableImages` holding `kuik-test.invalid/nginx/nginx-unprivileged:1.31.6-alpine`, reason `Unreachable`, `pods` 1;
- `ImagesUnavailable` `True`, reason `ChecksFailed`; `Ready` `True`, reason `IsReady`;
- `status.checks.registries` with an entry for `quay.io` and one for `kuik-test.invalid`;
- `kuik_image_origin_unavailable` for the image that fails, and no such series for the quay.io one.

**Prepare**: the fixture of [scenario 3](#3-an-onfailure-alternative-reroutes-a-pod-whose-origin-is-down), then [`12-monitor.yaml`](./fixtures/12-monitor.yaml):

```sh
task acceptance-apply -- --kubeconfig <path> --context <name> --file test/acceptance/fixtures/12-monitor.yaml
```

**Do**: wait 5 minutes: each registry opens one check window per minute by default, shared with the real ImageMonitors of that host ([Scheduling](../../docs/v3/spec.md#scheduling)).

**Check**:

- counts and unavailable images: `KUBECONFIG=<path> kubectl --context <name> get imagemonitor kuik-test-monitor -o jsonpath='{.status.images.origin}{"\n"}{.status.unavailableImages}{"\n"}'`
- conditions: `KUBECONFIG=<path> kubectl --context <name> get imagemonitor kuik-test-monitor -o jsonpath='{range .status.conditions[*]}{.type} {.status} {.reason}{"\n"}{end}'`
- rings: `KUBECONFIG=<path> kubectl --context <name> get imagemonitor kuik-test-monitor -o jsonpath='{.status.checks.registries}'`
- metric: `task acceptance-inspect -- --kubeconfig <path> --context <name> --metrics 'kuik_image_origin_unavailable.*kuik-test-monitor'`

**Explore**: delete the pod `origin-up` (`task acceptance-delete -- --kubeconfig <path> --context <name> pod/origin-up`): its image must move to `status.retainedImages` with `unusedSince` and a digest, and count as `retained` ([status](../../docs/v3/status.md#imagemonitor)). Does a `cycleDuration` appear once a lap completes, and is it close to what the shared quay.io windows allow?

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

- unavailable image and conditions: `KUBECONFIG=<path> kubectl --context <name> get imagemonitor kuik-test-anonymous -o jsonpath='{.status.unavailableImages}{"\n"}{range .status.conditions[*]}{.type} {.status} {.reason}{"\n"}{end}'`
- event (section "events"): `task acceptance-inspect -- --kubeconfig <path> --context <name> --grep kuik-test-anonymous`
- pull failure: `KUBECONFIG=<path> kubectl --context <name> get pod -n kuik-test anonymous -o jsonpath='{.status.containerStatuses[0].state.waiting.reason}'`

**Explore**: none worth it on a shared cluster: giving the pod a pull Secret would mean copying a credential, which the test never does. Without the private repository, the scenario is reported as not tested.

## 14. An ImageMirror copies an image to its destination

**Since**: 3.0.0-alpha.4

**The user** declares an ImageMirror with `rewritePolicy: None` to a registry path they dedicate to the test, with push credentials, then runs a pod it selects.

**They expect** ([ImageMirror](../../docs/v3/spec.md#imagemirror), [status](../../docs/v3/status.md#imagemirror), [conditions](../../docs/v3/status.md#conditions-and-their-reasons), [Catalogue](../../docs/v3/observability.md#catalogue), [Counters](../../docs/v3/observability.md#counters--cumulative-safe-to-sum-across-replicas)):

- the pod on its origin image, with no `kuik.enix.io/rewrites` annotation: `None` copies and never routes;
- one `ImageCopied` event on the CR;
- `status.images.copy` at `tracked` 1, `unavailable` 0, and no `failedImageCopies`;
- `status.repositories` holding one repository under the prefix;
- `DestinationOutOfSync` `False`; `Ready` `True`, reason `IsReady`;
- `kuik_mirror_copies_total` of the CR at 1, reason `Initial`, on the reconciler.

**Prepare**: a registry path dedicated to the test, that nothing else uses: kuik deletes the tags of this cluster it finds under it. A docker config Secret `kuik-test-mirror-push` in the kuik namespace that can push there and delete tags, created by the user: cleanup is on, so a destination that refuses tag deletion holds the CR at deletion and `acceptance-cleanup` fails. Then [`14-mirror-copy.yaml`](./fixtures/14-mirror-copy.yaml):

```sh
task acceptance-apply -- --kubeconfig <path> --context <name> --mirror-prefix <prefix> --file test/acceptance/fixtures/14-mirror-copy.yaml
```

**Do**: wait 5 minutes: the copy waits for a copy window of quay.io.

**Check**:

- image and annotations: `KUBECONFIG=<path> kubectl --context <name> get pod -n kuik-test mirror-copy -o jsonpath='{.spec.containers[0].image}{"\n"}{.metadata.annotations}{"\n"}'`
- counts, failures and repositories: `KUBECONFIG=<path> kubectl --context <name> get imagemirror kuik-test-mirror-copy -o jsonpath='{.status.images.copy}{"\n"}{.status.failedImageCopies}{"\n"}{.status.repositories}{"\n"}'`
- conditions: `KUBECONFIG=<path> kubectl --context <name> get imagemirror kuik-test-mirror-copy -o jsonpath='{range .status.conditions[*]}{.type} {.status} {.reason}{"\n"}{end}'`
- event (section "events") and counter: `task acceptance-inspect -- --kubeconfig <path> --context <name> --grep kuik-test-mirror-copy --metrics 'kuik_mirror_copies_total.*kuik-test-mirror-copy'`

**Explore**: the tag in the registry carries this cluster's `clusterID` suffix ([Multi-cluster](../../docs/v3/spec.md#multi-cluster-shared-destination-one-tag-per-cluster)); `selfChecked` appears within one `mirror.destinationScan.interval` (1 hour by default). Delete the CR (`task acceptance-delete -- --kubeconfig <path> --context <name> imagemirror/kuik-test-mirror-copy`): it is held until no pod runs its copies, then its tags are deleted and it goes ([Deleting an ImageMirror](../../docs/crds.md#deleting-an-imagemirror)). Without `--mirror-prefix`, the scenario is reported as not tested.

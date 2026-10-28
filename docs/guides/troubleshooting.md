---
description: Find why a pod was not rewritten, why a rewritten pod cannot pull, or why a resource reports a failure, and fix it.
sidebar:
  order: 1
---

# Troubleshooting

Start from the symptom you see. Each section says where to look, what it means, and how to fix it.

## Where to look

kuik reports what it does in 5 places. Start with the first one that matches your question.

| Question | Look at | Command |
| -------- | ------- | ------- |
| What did kuik decide for this pod? | The pod annotations | `kubectl get pod <pod> -o jsonpath='{.metadata.annotations}'` |
| Is this resource working? | Its `Ready` condition and anomaly conditions | `kubectl get imagealternatives,imagemirrors,imagemonitors` |
| Which images fail, and why? | The lists of the resource's status | `kubectl get <kind> <name> -o yaml`, with `<kind>` one of `imagealternative`, `imagemirror`, `imagemonitor` |
| What just happened? | Events, on the pod and on the resource | `kubectl describe pod <pod>`, `kubectl describe <kind> <name>` |
| What did a process do, step by step? | Logs, at `debug` level | `kubectl logs -n kuik-system -l app.kubernetes.io/component=webhook --tail=-1` |

The annotations are described in [What the webhook records on a pod](../crds.md#what-the-webhook-records-on-a-pod), the events, metrics and logs in [Events, metrics and logs](../observability.md). To see every candidate the webhook tried, raise its verbosity:

```bash
helm upgrade kube-image-keeper oci://quay.io/enix/charts/kube-image-keeper:$VERSION \
  --namespace kuik-system --reuse-values --set webhook.verbosity=debug
```

`$VERSION` is the installed version, shown by `helm list -n kuik-system`.

## A pod was not rewritten

Read the pod's annotations first:

- **`kuik.enix.io/no-alternatives`**: kuik tried every candidate, and none answered. The value names the resources that offered one. Go to [No candidate answers](#no-candidate-answers).
- **`kuik.enix.io/conceded-rewrites`**: kuik rewrote the image, then another mutating webhook replaced it, and kuik stood down. Narrow the scope of one of the two.
- **No kuik annotation at all**: the original image answered first, or kuik never looked at the pod. Go through the list below.

When kuik never looked at the pod:

1. **The pod existed before the resource.** kuik only rewrites pods when they are created. Restart the workload (`kubectl rollout restart deployment/<name>`, or `statefulset/<name>`, `daemonset/<name>`), or delete the pod so that its controller creates it again.
2. **The selectors do not match.** Compare the pod's labels and its namespace's labels with `podSelector` and `namespaceSelector`. An empty selector matches everything. See [Selecting workloads](../crds.md#selecting-workloads).
3. **The image does not match any entry.** Entries match whole path segments: `quay.io/acme/foo` never matches `quay.io/acme/foo-bar`, and a `repository` entry never matches a deeper path. See [Matching](../crds.md#matching).
4. **The container is never rewritten**: it has `imagePullPolicy: Never`, it belongs to a static pod, or its image is already one kuik produced.
5. **The webhook was not ready.** The webhook admits pods unchanged when it is down or not ready: kuik never blocks a pod. It stays not ready until the reconciler has created the ConfigMap `kuik-cluster-id`, see [Cluster ID](../configuration.md#cluster-id). Check that every kuik pod runs: `kubectl get pods -n kuik-system`.
6. **The chart restricts the webhook to some namespaces**, with the `webhook.namespaceSelector` value.

When the original image answered first, the resource put its candidates after the original: that is `rewritePolicy: OnFailure`, the default. Use `Always` to put them first, see [`rewritePolicy`](../crds.md#rewritepolicy). Two more cases put a candidate after the original:

- An `ImageMirror` under `Always`, for a container with `imagePullPolicy: Always`: see [`webhook.demoteMirrorWithPullPolicyAlways`](../configuration.md#keys).
- A candidate that an `ImageMonitor` or an `ImageMirror` reports failing: see [`demoteKnownFailures`](../configuration.md#keys).

### No candidate answers

The webhook checks each candidate with a manifest `HEAD`, and gives up on one after 2 seconds ([`webhook.availabilityCheck.timeout`](../configuration.md#keys)). The `debug` logs of the webhook name each candidate and why it failed. The usual causes:

- **The candidate does not hold that tag.** Check it with `crane manifest <candidate>` or `docker manifest inspect <candidate>`.
- **The candidate needs a credential.** The webhook checks with the candidate's `auth`, then the pod's `imagePullSecrets`, as the node would. Add an [`auth`](../crds.md#auth) to the entry. With the chart value `secretAccess.mode: restricted`, the webhook cannot read the pod's pull secrets outside `secretAccess.namespaces`, and checks anonymously: it never uses `fallbackAuth`. List the pod's namespace in `secretAccess.namespaces`.
- **The candidate is an `ImageMirror` copy not made yet.** The first pods of a new image fall back to the original until the copy exists. See [A copy fails](#a-copy-fails).
- **The registry is slow**, a proxy cache fetching the image from upstream for instance. Raise `webhook.availabilityCheck.timeout`.
- **kuik cannot reach the registry**, or does not trust its certificate. See [Registry TLS](../configuration.md#registry-tls).

## A rewritten pod cannot pull

The pod shows `ErrImagePull` or `ImagePullBackOff` on the image kuik placed. The `kuik.enix.io/rewrites` annotation holds the original image and the resource that placed the new one.

- **`401 Unauthorized` or `403 Forbidden`**: the node has no credential for the new registry. With an `auth.secretRef` and `injectPullSecret` left to its default (`true`), kuik adds `kuik-inject-<kind>-<name>` to the pod's `imagePullSecrets`, the kind in lowercase (`kuik-inject-imagealternative-<name>`, `kuik-inject-imagemirror-<name>`), and the secret syncer writes that Secret in the pod's namespace (see [Injected pull secrets](../concepts/pull-secrets.md)). When a source Secret is missing or malformed, the secret syncer still writes the injected Secret, without that entry, and the resource has a `PullSecretInjectionFailed` event. Check that the Secret holds the registry: `kubectl get secret kuik-inject-<kind>-<name> -n <namespace> -o jsonpath='{.data.\.dockerconfigjson}' | base64 -d`. A Secret missing altogether points at the secret syncer: read its logs. Under `OnFailure`, the first pod of a namespace may start a few seconds before its Secret: the kubelet retries. An `auth.provider` credential is not injected yet.
- **`x509: certificate signed by unknown authority`, or `http: server gave HTTP response to HTTPS client`**: the node's container runtime does not trust the registry, or does not know it is plain HTTP. The `tls` and `insecure` settings of kuik apply to kuik only: configure the runtime of every node too (containerd's `certs.d`, CRI-O's `/etc/containers/certs.d`).
- **`not found`**: the candidate answered kuik's check, then lost the image before the pull. Restart the pod: kuik checks the candidates again.

## A resource is not `Ready`

`kubectl get` shows `READY False`. The reason is in the `Ready` condition, and in a `ResourceNotReady` event:

```bash
kubectl get imagemirror <name> -o jsonpath='{.status.conditions[?(@.type=="Ready")]}'
```

| Reason | Cause | Fix |
| ------ | ----- | --- |
| `SecretNotFound` | A `secretRef` names a Secret that does not exist in kuik's namespace | Create it in kuik's namespace (`kuik-system` by default): a `secretRef` carries no namespace |
| `SecretMalformed` | The Secret is not of type `kubernetes.io/dockerconfigjson`, or its content does not parse | Recreate it with `kubectl create secret docker-registry` |
| `InvalidConfig` | A selector or an entry the API server accepted cannot be used | The message names the field |
| `RegistryDeleteUnsupported` | The `ImageMirror` destination refuses tag deletion, so the cleanup cannot run | Allow tag deletion on the registry, or set `cleanup.enabled: false` |

The other conditions (`FallbackActive`, `ImagesUnavailable`...) are not errors of the resource: they report an image, see [Status conventions](../crds.md#status-conventions).

## A copy fails

An `ImageMirror` lists the copies that fail in `status.failedImageCopies`, with a reason:

```bash
kubectl get imagemirror <name> -o jsonpath='{.status.failedImageCopies}'
```

| Reason | Cause | Fix |
| ------ | ----- | --- |
| `SourceNotFound` | No source holds the image any more: the origin and every alternative answered not found | Rebuild or push the image. Pods still running it do so from a node cache |
| `PushRejected` | The destination refused the manifest: size, media type, or a policy such as nested paths or immutable tags | Read the registry's logs. The destination must accept nested repository paths, see [Destination requirements](../crds.md#destination-requirements) |
| `Unauthorized` | A registry refused the credential, on either side | On the destination, the `manage` credential needs read and write. On the source, kuik reads with the pods' pull secrets, then [`fallbackAuth`](../configuration.md#fallbackauth) |
| `QuotaExceeded` | A registry rate-limited the copy | kuik tries again on its next window. Raise the host's `copy.interval` if it lasts, see [Registry pacing](../concepts/pacing.md) |
| `SourceUnreachable` / `DestinationUnreachable` | That side did not answer: DNS, connection, TLS or timeout, or answered with a server error (5xx) | Check the network from kuik's namespace, and [Registry TLS](../configuration.md#registry-tls) |

**Copies are slow.** That is the pacing: kuik copies at most one image per source registry per `copy.interval`, 3 minutes by default. 100 images from one registry take 5 hours. For a registry you own, lower its interval, see [Registry pacing](../concepts/pacing.md).

## An image is reported unavailable

An `ImageMonitor` lists the images whose check fails in `status.unavailableImages`, with a reason:

| Reason | Cause | Fix |
| ------ | ----- | --- |
| `ManifestNotFound` | The tag or digest no longer exists in the registry | Rebuild or push the image before its pods restart |
| `Unauthorized` | No credential kuik holds can read it | Declare one in [`fallbackAuth`](../configuration.md#fallbackauth). With the chart value `secretAccess.mode: restricted`, kuik cannot read the pods' pull secrets outside the listed namespaces |
| `QuotaExceeded` | The registry rate-limited the check | Raise the host's `check.interval` |
| `Unreachable` | The registry did not answer: DNS, connection, TLS or timeout, or answered with a server error (5xx) | Check the network, and [Registry TLS](../configuration.md#registry-tls) |

An `AnonymousAccessDenied` event on the monitor says that a registry refused an anonymous check: the image is likely private, and no credential covers it. Declare a `fallbackAuth` entry for it.

**The counts lag behind.** `available` plus `unavailable` stays below `tracked` until every image has been checked once: kuik checks one image per registry per `check.interval`. `status.checks.registries[].cycleDuration` is how long a full round takes on each registry.

## An `ImageMirror` stays `Terminating`

A deleted `ImageMirror` waits until it can be released safely:

1. **A pod still runs one of its copies.** A rescheduled pod would find the copy gone. Restart those workloads: they go back to their origin.
2. **It cannot delete its tags**, with `cleanup.enabled`: the `manage` credential cannot be read, or the destination refuses tag deletion. A destination that refuses tag deletion sets `Ready` to `False` with the reason `RegistryDeleteUnsupported`, and emits an `ImageDeletionFailed` event on each try.

To release it while leaving the tags in place, set `cleanup.enabled: false` on it. See [Deleting an ImageMirror](../crds.md#deleting-an-imagemirror).

## A status list is truncated

The anomaly lists of a status hold 500 entries at most. From 80%, the `ListCapacityPressure` condition turns `True`, and `status.truncated` counts the entries left out. The metrics still carry every image: alert and count on them, see [Anomalies](../observability.md#anomalies).

## kuik does not start, or ignores a config change

- **A process does not start**: its global config file does not validate. The first lines of its logs say which key. See [Global config file](../configuration.md#global-config-file).
- **The ConfigMap `kuik-cluster-id` holds an invalid ID**: the reconciler does not start. The webhook starts, logs that the cluster ID is invalid, and stays not ready: it admits every pod unchanged. See [Cluster ID](../configuration.md#cluster-id).
- **A config change has no effect**: the kubelet updates the mounted file a minute or two after the ConfigMap changes, and `Reloaded global config` in the logs marks the reload. If it does not come, the reload was refused, and the previous config stays in effect. The logs say why, and `kuik_config_reload_errors_total` counts the refusals. `metrics` is only read at startup.
- **After an upgrade, a field is unknown**: Helm does not upgrade CRDs. Apply the CRDs of the new version, see [Installation](../installation.md).

## Still stuck?

Open an [issue](https://github.com/enix/kube-image-keeper/issues) with the kuik version, the resource, the pod annotations, the events, and the `debug` logs of the process involved.

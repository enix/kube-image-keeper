---
description: How kuik copies the pull credentials of its routing resources into the namespaces whose pods it rewrites, and keeps them valid.
---

# Injected pull secrets

When the webhook rewrites a container to a registry that needs a credential, the kubelet needs
that credential to pull. An `auth` whose [`injectPullSecret`](../crds.md#auth) is on
makes kuik provide it: the webhook adds `kuik-inject-<kind>-<name>` to the pod's
`imagePullSecrets`, and the `secret-syncer` process writes that Secret in the pod's namespace.

One Secret exists per routing resource and namespace. It is a
`kubernetes.io/dockerconfigjson` Secret labelled `app.kubernetes.io/managed-by:
kuik-secret-syncer` and owned by the resource, so deleting the resource deletes its Secrets in
every namespace.

## Where a Secret lands

| `rewritePolicy` | The Secret is written | It holds |
| --------------- | --------------------- | -------- |
| `Always` | in every namespace the `namespaceSelector` selects, before any pod exists | every entry of the resource whose `auth` injects |
| `OnFailure` | in a namespace the `namespaceSelector` selects, once a pod there has been rewritten by the resource | the entries that served the rewrites of its live pods |

Under `Always`, `podSelector` does not narrow where the Secret lands: an absent or empty
`namespaceSelector` puts it in every namespace of the cluster. Set a `namespaceSelector` to
bound it. A resource none of whose entries injects writes no Secret at all.

Under `OnFailure`, a namespace the `namespaceSelector` does not select gets no credential,
whatever the rewrite records its pods carry.

Under `OnFailure`, the first rewritten pod of a namespace may start before its Secret exists.
The kubelet retries the pull and reads the Secret again on each attempt, so the pod starts a few
seconds later; the next pods find the Secret in place.

## What a Secret holds

Each entry gets one `auths` key, its registry path: the `repository` or `repositoryGroup` value
as written, or an `ImageMirror`'s `destination.path` without its trailing slash. Two entries of
one host, `quay.io/acme` and `quay.io/other`, keep their own credentials: the kubelet picks the
most specific key matching the image.

The value is the credential of the `secretRef` Secret that the kubelet would use for that path,
the most specific of its own `auths` keys. A `secretRef` that is missing, is not a
docker-registry Secret, or holds no credential for the path is left out, and the other entries
are still written. kuik reports it with a [`PullSecretInjectionFailed`](../observability.md#on-a-resource)
event on the resource.

`provider` credentials are not injected yet: an entry with a `provider` is left out, whatever
its `injectPullSecret`.

## Keeping it valid

The syncer provisions credentials with a server-side apply. When no credential is needed, it
empties an existing Secret with a patch that cannot create one. Neither operation reads the
Secret first: the syncer cannot read any Secret outside the cluster resource namespace. It
writes again when:

- a `secretRef` Secret is created or changes in the cluster resource namespace;
- the resource changes, or a namespace enters or leaves its scope;
- the pods of a namespace change what an `OnFailure` resource needs;
- the informers resync, which restores a Secret someone else deleted.

A write that changes nothing is a no-op on the API server.

## What the syncer may write

The syncer may create and patch Secrets in every namespace, which RBAC cannot narrow further. A
`ValidatingAdmissionPolicy` installed by the chart bounds it at the API server. A Secret the
syncer creates or updates must:

- be of type `kubernetes.io/dockerconfigjson`;
- carry the label `app.kubernetes.io/managed-by: kuik-secret-syncer`;
- be named `kuik-inject-*` in any namespace, or `kuik-provider-*` in the namespace kuik is installed in.

The two prefixes are reserved both ways: any other identity is refused the creation or update
of a Secret named `kuik-inject-*` or `kuik-provider-*`, in every namespace. Two updates are let
through, both leaving the type, the data, the labels and the immutability unchanged: one that
also leaves the owner references unchanged, such as a `kubectl replace` of every Secret to
rotate the encryption at rest, and the garbage collector removing an owner reference. Deletions
are not checked, so deleting a resource still deletes its Secrets. The policy fails closed: when
the API server cannot evaluate it, the write is refused. Since the policy sees every Secret
write in the cluster, a failure to evaluate it would refuse all of them, Helm releases
included: its match conditions limit it to the writes of the syncer and to the reserved names,
and every other Secret write skips it.

A backup tool that recreates Secrets on restore, such as Velero, is refused these names. It is
harmless, since the syncer writes them again, but the refusals show in the restore log, and
Velero marks the restore `PartiallyFailed`, which fires any alert on that status: exclude these
Secrets from the backup to avoid them.

## When a credential is no longer needed

Under `OnFailure`, an entry that no live pod uses any more stays for a grace period
([`secretSyncer.onFailureGrace`](../configuration.md), 15 minutes by default) before it is
removed: the pods of a rollout or a scale-up during an outage come back and find it. The Secret
itself is never deleted by the syncer. With no entry left, it holds `{"auths":{}}`. A namespace
that leaves the scope of an `Always` resource gets the same empty Secret; one that leaves the
scope of an `OnFailure` resource gets it once that grace period has passed, its pods still
running.

After a restart, the syncer rebuilds the desired credentials from the resources, namespaces and
live pods. It empties existing Secrets that are no longer needed, including when pods disappeared,
the namespace left the resource's scope, or injection was disabled while it was stopped. Grace
periods are held in memory: entries kept only by a grace period are dropped on restart. Unused
pairs without an existing Secret stay without one.

Deleting the resource deletes those Secrets in every case.

The 3.0.0 alphas could write the Secret of an `OnFailure` resource in a namespace its
`namespaceSelector` does not select. After an upgrade, the syncer empties these Secrets while
their resource and namespace still exist.

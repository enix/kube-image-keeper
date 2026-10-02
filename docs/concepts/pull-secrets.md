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
| `OnFailure` | in a namespace once a pod there has been rewritten by the resource | the entries that served the rewrites of its live pods |

Under `Always`, `podSelector` does not narrow where the Secret lands: an absent or empty
`namespaceSelector` puts it in every namespace of the cluster. Set a `namespaceSelector` to
bound it. A resource none of whose entries injects writes no Secret at all.

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

The syncer writes with a server-side apply and never reads the Secrets it wrote: it cannot read
any Secret outside the cluster resource namespace. It writes again when:

- a `secretRef` Secret is created or changes in the cluster resource namespace;
- the resource changes, or a namespace enters or leaves its scope;
- the pods of a namespace change what an `OnFailure` resource needs;
- the informers resync, which restores a Secret someone else edited.

A write that changes nothing is a no-op on the API server.

## When a credential is no longer needed

Under `OnFailure`, an entry that no live pod uses any more stays 15 minutes before it is
removed: the pods of a rollout or a scale-up during an outage come back and find it. The Secret
itself is never deleted by the syncer. With no entry left, it holds `{"auths":{}}`. A namespace
that leaves the scope of an `Always` resource gets the same empty Secret.

The syncer keeps which Secrets it wrote in memory. After a restart, it does not know a Secret
nothing needs any more, so such a Secret keeps its last content:

- under `OnFailure`, when the pods of its namespace all went away while the syncer was stopped,
  until a new rewrite lands in that namespace;
- under `Always`, when its namespace left the scope while the syncer was stopped, until the
  namespace comes back into scope.

Deleting the resource deletes those Secrets in both cases.

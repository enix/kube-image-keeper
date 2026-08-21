# spec v3 open-questions

The following questions are currently open and will be discussed after v3.0 has been released.

## `ImageMirror.spec.driftPolicy` default

Source: [PR #629, review thread on `docs/v3/spec.md`](https://github.com/enix/kube-image-keeper/pull/629#discussion_r3646060022).

The spec currently defaults `driftPolicy` to `Ignore`: an image is copied once to the destination
registry and never refreshed if the upstream tag digest changes.

The question raised: if we want kuik to be as transparent as possible, should the default be `Sync`
instead? With `Ignore`, a pod that falls back to the mirror after the primary registry fails can
silently get an older image than the one the upstream tag points to now. The same concern applies to
a destination populated from an `ImageAlternative` entry, which is asserted equivalent but not
verified byte-identical (see the note on copy semantics in [`spec.md`](./spec.md)).

Counter-proposal from the thread: rather than flipping the default, add an `Auto` value that enables
`Sync` behaviour only when the container's `imagePullPolicy` is `Always`, so the mirror follows the
tag exactly when the pod asked to. The caveat is that `imagePullPolicy: Always` is known to interact
in complicated ways with kuik, so this is parked for review **after v3.0**.

Status: open; `Ignore` stays the default in the spec for now, `Auto` deferred post v3.0.

## `ImageAlternative` status condition naming (`NoActiveFallback` / `NoAlternatives`)

Sources: [PR #629, thread on `NoActiveFallback`](https://github.com/enix/kube-image-keeper/pull/629#discussion_r3650519411)
and [thread on `NoAlternatives`](https://github.com/enix/kube-image-keeper/pull/629#discussion_r3650528268),
both on [`status.md`](./status.md).

Two related concerns about the conditions currently declared on `ImageAlternative.status`:

1. `NoActiveFallback` reads as a double negation in practice (`NoActiveFallback=False` means a fallback
   *is* in use). Naming it positively, for instance `ActiveFallback` or `FallbackInUse`, would read
   better. The usefulness of the condition itself was also questioned: is there a real scenario where
   an operator would `kubectl wait` on it? The plausible one is "wait until all pods are using their
   intended images", which does make sense, but ideally without the negation.
2. `NoAlternatives` has the same problem in reverse. The likely use case is "wait until every image in
   every pod has at least one available alternative", which suggests inverting the condition, but the
   candidate name (`AtLeastOneAlternativeAvailable`) is clumsy and needs more work.

The polarity choice is not free: Kubernetes API conventions push toward positive polarity condition
types, while `Normal-True` conditions (a condition that is `True` in the healthy case) are what keeps
`kubectl get` output quiet, and that is exactly what the `NoXXX` naming was buying here. Any rename
has to pick which of the two it optimises for, and stay consistent across all CRs.

Status: open, deferred. The v3.0 CRDs ship as **alpha**, so conditions can be renamed or reshaped
later without a breaking change.

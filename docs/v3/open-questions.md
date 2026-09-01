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

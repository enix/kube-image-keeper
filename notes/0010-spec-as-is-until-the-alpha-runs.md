# 0010 — The spec is implemented as written until an alpha runs

**Date:** 2026-09-28 · **Status:** decided

**Decision:** the 3.0 alphas implement `docs/v3/` as it is written, defaults included. The
spec is not reworked before an alpha runs end to end. A point that needs an arbitration or
a spec change is recorded in the spec amendments list and deferred; it never blocks a
milestone. `auth.provider` (the `aws`, `gcp` and `azure` cloud identities) is left aside for
the first phases of 3.0: the type stays in the CRD, a provider resolves to a pending
credential and the resolution moves on. It comes back at the end of 3.0 if feasible, else
in 3.1.

**Why:**

- A running binary shows where the spec is wrong faster than a debate on the text does.
- Cloud identities did not exist in v2, were the fuzziest part of the spec (modelled on
  Flux) and need real cloud accounts to test; they were slowing the start of M2 and M5.
- Paul syncs with Alex and Aurélien once a week: a spec question raised mid-week waits.

**Rejected:**

- Amending the spec as questions come up: each amendment costs a team sync and stalls
  the milestone that raised it.

**Constraints:** an ambiguity is implemented in its most literal reading, tested, and
recorded in the amendments list; `docs/v3/` is never edited to settle it.

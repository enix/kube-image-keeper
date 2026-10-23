# Bugs and correctness

Priorities and evidence classes are defined in [the audit summary](./README.md). This report contains 14 findings: five P1, seven P2 and two P3. References identify the audited revision. The credential authorization and HTTP disclosure findings, R01 and R07, are in [the security report](./02-security-deployment-and-ci.md).

Controller reproductions used isolated envtest and local registry fixtures through temporary Go overlays. They did not modify repository tests or use a production cluster. The intentionally failing assertions below are separate from the passing baseline suite; see [tests and coverage](./03-tests-and-coverage.md).

## C01 — P1 — Restart can retain an unused injected credential indefinitely

**Evidence: reproduced with envtest.** **References:** `internal/controller/secretsyncer/syncer.go:266-273`; `docs/v3/walkthroughs/03-secret-syncer-reconciliation.md:135,158`.

**Trigger:** an OnFailure resource has provisioned a pull Secret. The syncer stops, its last rewritten Pod disappears, and a fresh syncer starts. The same empty desired-state branch applies when all entries were in grace at restart, or an Always namespace leaves scope while the process is stopped.

**Expected / observed:** startup should recompute empty content and remove stale credentials. Walkthrough 03, lines 135 and 158, says startup applies every pair and losing grace shortens retention. Instead, `known[pair]` is empty after restart, so `len(needed) == 0 && last == nil` returns without applying an empty Secret. No grace expiry or source watch is established for the pair. Subsequent reconciles repeat the return.

**Impact:** credentials can remain indefinitely in a namespace where they are no longer needed, including after an access-scope reduction. Process memory has become necessary for credential removal, contrary to the stated recovery model.

**Proof:** provision one synthetic `ghcr.io/acme` credential, stop the syncer, delete the sole Pod and start a fresh process against the same API. The four-second eventual empty-auth assertion failed: the Secret's decoded credential-to-username map remained `{"ghcr.io/acme":"robot"}`. The existing restart test passed because it keeps another active entry; its nonempty desired set avoids this branch.

**Remediation / regression:** reconcile empty content for potentially existing managed pairs at startup while preserving the no-read security model. If creating empty Secrets for unused pairs is undesirable, use a durable inventory; an empty process-local map cannot prove that no Secret exists. Test loss of the last entry, all entries in grace, and Always scope reduction across restart, followed by source rotation/deletion. Assert stale credentials disappear without requiring read access to destination Secrets.

## C02 — P1 — Tag-only repair updates an Ignore/Warn snapshot from upstream

**Evidence: reproduced with envtest and local registries.** **References:** `internal/controller/kuik/imagemirror_destination.go:78-85`; `internal/controller/kuik/imagemirror_copy.go:378-421,445-446`; `docs/v3/walkthroughs/02-imagemirror-reconciliation.md:140,150`.

**Trigger:** a mirror copied tag digest D. Upstream moves to D2 under Ignore or Warn. Only the destination's cluster tag is deleted; D's manifest remains present by digest.

**Expected / observed:** restore the tag onto D without consulting upstream. Walkthrough 02, line 140, requires only a tag PUT when its manifest remains, and line 150 requires Warn to leave the copy unchanged. Self-check correctly records `writeBack`, but the worker still probes and copies the current source. That flag only suppresses events/metrics after the copy.

**Impact:** the restored tag points to D2, violating snapshot policy. If upstream is unavailable, repair fails despite the destination holding all required content. Event suppression also conceals the replacement.

**Proof:** under Ignore, copy D, move upstream to D2, delete only the destination tag, then advance the destination pass and copy window. The expected-D assertion received D2; registry logs included new blob uploads. Warn uses the same repair branch by inspection. The existing unchanged-upstream writeback test passed alongside this counterexample.

**Remediation / regression:** implement destination-only retagging from the remembered destination digest using destination credentials. Keep full recopy for genuinely missing manifests. Test Ignore and Warn with changed/unreachable upstream and a private destination. Assert D is preserved, upstream receives no request, and only the intended tag is written.

## C03 — P1 — Mirror copying prefers an Always alternative over a healthy origin

**Evidence: reproduced with envtest and local registries.** **References:** `internal/controller/kuik/imagemirror_copy.go:279-291`, particularly line 288; `docs/v3/walkthroughs/02-imagemirror-reconciliation.md:95,101`.

**Trigger:** an ImageAlternative with `rewritePolicy: Always` covers a healthy origin. Origin and alternative mutable tags currently resolve to different digests, D and D2. A mirror selects the image.

**Expected / observed:** copy origin D and use alternatives only when it fails, except when the origin is explicitly marked unavailable. Walkthrough 02, line 95, states that order. `copySources` instead consumes admission candidates, whose Always policy deliberately puts alternatives first.

**Impact:** D2 is copied under the origin-derived destination name despite the healthy origin. Under Sync, the drift ring can observe origin D while every recopy chooses alternative D2, preventing convergence. Alternative equivalence does not guarantee identical mutable-tag digests; walkthrough 02, line 101, explicitly allows differing digests.

**Proof:** a healthy origin and alternative served different manifests for corresponding tags. With an Always alternative and a selecting mirror, the destination received the alternative digest instead of the origin digest.

**Remediation / regression:** separate copy-source ordering from admission rewrite policy. Preserve scope, declared alternative order, unavailable handling and per-candidate auth/transport. Test healthy and failing origins across rewrite policies, explicit unavailable origins, and Sync convergence when alternative content differs.

## C05 — P1 — Two mirrors sharing a destination delete each other's live tags

**Evidence: reproduced with envtest and a local registry.** **References:** `internal/mirrorpath/plan/sweep.go:75-94`; `internal/controller/kuik/imagemirror_destination.go:137-145,164-165`.

**Trigger:** two ImageMirrors in one cluster share a destination path and select separate namespaces. Each copies a different tag in the same origin repository. The API accepts this configuration; destination-path immutability does not establish unique ownership.

**Expected / observed:** cleanup should preserve another active mirror's live image, or reject unsupported overlap explicitly. Each sweep instead treats every tag with this cluster's suffix as its own, while its expected set contains only its own selected live images. The other mirror's tag becomes an orphan and is deleted after retention.

**Impact:** live destination tags disappear. The mirrors can repeatedly delete and recreate each other's copies, causing pull failures and unnecessary source traffic. Cluster suffixes isolate clusters, not resources in the same cluster.

**Proof:** mirror A selected `acme/app:v1`; mirror B selected `acme/app:v2` in a separate namespace, with the same destination and zero retention. Both copies succeeded. A's first sweep marked B's tag pending; a later sweep after the deletion buffer removed it. B's destination HEAD returned 404 while its Pod remained live.

**Remediation / regression:** define and enforce destination ownership. Reject unsafe effective overlaps or coordinate expected tags across mirrors before cleanup/finalization. Test shared and nested paths, disjoint selectors with tags in one repository, deletion of one resource while another still needs a tag, and preservation of other clusters' tags.

## C06 — P1 — A sweep can retire inventory while its first upload is still running

**Evidence: reproduced with a gated manifest PUT, envtest and restart.** **References:** `internal/mirrorpath/plan/sweep.go:82-85`; `internal/controller/kuik/imagemirror_controller.go:285-289`; `internal/controller/kuik/imagemirror_copy.go:409-421,638-644`; `docs/v3/walkthroughs/02-imagemirror-reconciliation.md:89,229`.

**Trigger:** a copy records a destination repository before uploading. A destination pass lists it before the first tag commits, sees no owned tags and retires the inventory entry. The upload then succeeds. The controller restarts and the Pod departs before the next controller observes it.

**Expected / observed:** pre-push inventory must survive concurrent sweeps. Walkthrough 02, lines 89 and 229-230, identifies it as the non-recomputable record needed for cleanup. Sweep retires the empty listing without accounting for an in-flight write or expected live reference. The successful upload does not restore inventory; process-local `copied` can also suppress immediate re-recording.

**Impact:** after restart with no live Pod, no loop can discover that repository. Cleanup and finalization can leave the committed tag and associated storage permanently behind.

**Proof:** hold the first destination manifest PUT; verify one repository exists in status; run a due destination pass and observe the inventory become empty. Release PUT and confirm the tag exists. Stop the controller, remove the Pod, restart, and run two complete passes beyond retention. `repositories` and `pendingDeletion` stayed empty while the orphan tag remained readable. The expected-deletion assertion failed.

**Remediation / regression:** coordinate retirement with write reservations/in-flight copies and expected references. Guard the list-to-status interval so no copy commits into retired inventory. Re-recording after upload alone leaves a crash window. Test the blocked first PUT, concurrent sweep, restart on either side of commit, Pod departure, failed upload and finalizer interaction. Every committed tag must remain discoverable until deletion.

## C07 — P2 — An old check can contaminate a recreated monitor with the same name

**Evidence: reproduced through deterministic stale-callback injection into envtest-backed controller state.** **References:** `internal/controller/kuik/imagemonitor_checks.go:433-461`; `internal/registry/pacing/scheduler.go:380-393,545-560`.

**Trigger:** a monitor starts a check, is deleted, and is recreated with the same name and a new UID before the old check completes.

**Expected / observed:** discard old-incarnation results. `internal/controller/kuik/imagemonitor_controller.go:251-256` already clears memory on UID change for this reason. However, `monitorChecker` retains only a name; its completion fetches and modifies the current memory by that name. Removing the ring does not cancel or validate the in-flight completion.

**Impact:** the replacement resource can report stale authorization/unavailability or drift results, emit misleading metrics/events and influence hints until a later check corrects them. Recreating a resource with different credentials or scope exposes the boundary.

**Proof:** retain the original checker, delete/recreate the CR, verify different UIDs and an empty replacement status, then deliver the old Unauthorized completion and reconcile. The new CR acquired the old `unavailableImages` entry. This controls the same completion method called by the scheduler; a delayed real HTTP exchange was not separately exercised.

**Remediation / regression:** bind asynchronous work and completion to resource UID or an incarnation token. Reject stale completion even if cancellation races; apply equivalent checks to cache writes where needed. Test deletion/recreation with changed auth/scope, deletion without recreation and ring replacement. Review the related name-only mirror drift callback without treating it as a separately reproduced defect.

## C08 — P2 — Restart reduces mirror anomaly counts to the capped sample

**Evidence: reproduced for missing sources; the drift analogue is code inspection.** **References:** `internal/controller/kuik/imagemirror_copy.go:495-512`; `internal/controller/kuik/imagemirror_controller.go:335-367`; `internal/mirrorpath/plan/counts.go:71-75`.

**Trigger:** a mirror has more failures than `maxListSize`, then restarts before every image is retried.

**Expected / observed:** a capped list is a sample, and omitted anomalies should remain visible in aggregate/truncation reporting until re-observed. `docs/v3/status.md:44-68` defines that distinction. `seed` restores only capped list entries, then the next report treats those maps as the complete population. Its comment promises to preserve counts, but the previous full count is overwritten.

**Impact:** `missingSource`, `drifted` and truncation/pressure reporting can improve solely because the controller restarted, then grow back over paced retries. This is a reporting defect; copy work is not lost. The waveform can conceal an ongoing incident.

**Proof:** capacity two, three missing-source images: before restart, MissingSource=3, list length=2 and truncated count=1. Restart with the same capacity and reconcile before source windows. MissingSource became 2, failing the expected-3 assertion. Loss of truncation/pressure follows the same sample-only rebuild by inspection.

**Remediation / regression:** retain last-known counts/truncation until unknown entries are re-observed or leave scope, or expose an explicit rebuilding state. ImageMonitor already has persisted-count holding in `internal/controller/kuik/imagemonitor_checks.go:106-165`. Test failures and drift above capacity across restart/reload, resolution during rebuilding, scope departure and matching metrics.

## R02 — P2 — Injected credentials do not preserve the probe's per-image lookup

**Evidence: reproduced production credential resolution and Secret construction.** **References:** `internal/auth/pullsecret/pullsecret.go:239-251,285-290`; `internal/registry/keychain/keychain.go:35-62`; `internal/webhook/core/v1/probe.go:144`.

**Trigger:** a routing entry is group `private.test/acme`, while its valid Docker config credential covers only child repository `private.test/acme/app`. A related case has multiple matching credentials, with the first stale and a later one usable.

**Expected / observed:** a private candidate proven pullable using its full reference should remain pullable using the generated Secret. The webhook resolves the image reference; `Build` resolves the group prefix and keeps only `creds[0]`. A child-only credential cannot match the broader prefix, so the emitted auth map is empty.

**Impact:** admission routes the Pod to a private image that its generated credential cannot pull. Retries do not repair the projection mismatch. Prefix projection is documented in `docs/concepts/pull-secrets.md:34-42`; this is a cross-component contract defect, not an assertion that `Build` violates that documented projection.

**Proof:** `keychain.Authenticators("private.test/acme/app:v1", source)` returned one authenticator without error. `Build` for the group emitted `SecretMalformed: secret "source" holds no credential for private.test/acme` and `{"auths":{}}`. The first-stale/later-valid case is supported by inspection, not a separate executed reproduction. Declared `secretRef` rejection of legacy dockercfg format is intentional and excluded.

**Remediation / regression:** preserve applicable narrower keys and retry semantics without widening credential scope, or reject unsupported projections before selecting the private candidate. Test real candidate probing followed by a pull using only the generated Secret: child-only key, expired-specific/valid-broader keys, and a broad group credential control.

## R03 — P2 — Injection can choose a different entry's credential from the routing winner

**Evidence: reproduced candidate-to-syncer mismatch; pull failure depends on differing credential permissions.** **References:** `internal/routing/routing.go:415-418`; `internal/auth/pullsecret/pullsecret.go:194-211`; `internal/routing/podrecord/podrecord.go:26-36`.

**Trigger:** one OnFailure ImageAlternative has nested target groups with different auth: `private.test/acme` uses Secret `source`, while `private.test/acme/team` uses Secret `nested`. Origin `upstream.test/acme/team/app:v1` produces `private.test/acme/team/app:v1` from the broad target.

**Expected / observed:** provisioning should retain the credential requirement of the candidate actually selected. The syncer infers the entry from output prefix alone and chooses the most specific target, `nested`. The record lacks the chosen entry identity needed to distinguish the configurations.

**Impact:** a successful admission probe can lead to persistent pull failure or provisioning of a different credential than intended. This is an ambiguity between routing and projection contracts. The existing `internal/auth/pullsecret/desired_test.go:181-185` validates the most-specific-output inference without obtaining that output from actual candidate generation.

**Proof:** production candidate generation returned reference `private.test/acme/team/app:v1` with auth `source`; passing its rewrite record to the syncer yielded `[{private.test/acme/team nested}]`.

**Remediation / regression:** preserve authenticated identity of the selected entry/credential requirement. Do not infer it from the resulting path alone, and define rotation and CR-edit behavior. Test nested groups where only the broad candidate works, then pull with the emitted Secret; include duplicate output references with distinct configuration. Coordinate provenance with R01 in [the security report](./02-security-deployment-and-ci.md).

## R04 — P2 — Admission cancellation does not stop sequential probing

**Evidence: reproduced with a bounded local HTTP fixture.** **References:** `internal/webhook/core/v1/probe.go:52-64,110-120`; `internal/webhook/core/v1/pod_webhook.go:355-361,379-390`.

**Trigger:** the admission context is canceled before or during a candidate list containing slow/unavailable endpoints, including an API-server webhook timeout.

**Expected / observed:** the canceled waiter should exit and start no further candidate requests. A shared probe may continue for another live admission. Synchronous `singleflight.Do` provides no cancelable waiter; each new probe uses `context.WithoutCancel`, the candidate loop does not check cancellation, and the enclosing wait waits for all lists. Each later candidate receives a fresh timeout.

**Impact:** abandoned admissions retain goroutines and consume registry quota for up to candidate count multiplied by timeout. API-server fail-open behavior does not stop the work. Long lists and admission churn amplify the cost.

**Proof:** cancel before calling `Default`; use three local candidates, each returning 404 after 50 ms. The call returned nil after about 166 ms, recorded three manifest requests after cancellation and generated a no-alternatives annotation.

**Remediation / regression:** use cancelable singleflight waiting and stop between candidates. Bound shared probes with process lifetime and timeout while separating their lifetime from each request waiter. Test cancellation before entry, during a probe and while sharing one with a live waiter; assert prompt exit, no later candidate calls and successful service to the live waiter.

## R05 — P2 — Shared background verdicts cross credential and transport contexts

**Evidence: reproduced scheduler reuse; controller consequences traced by inspection.** **References:** `internal/registry/pacing/scheduler.go:355-358,388-389`; `internal/controller/kuik/imagemonitor_checks.go:342-421`; `internal/controller/kuik/imagemirror_drift.go:82-91`.

**Trigger:** two rings track the same reference with different Pod credentials, or a monitor alternative and mirror origin have different entry auth/transport.

**Expected / observed:** verdict reuse should require equivalent authentication and transport semantics. Each checker resolves its own context, but the scheduler caches by reference only and can deliver another ring's result without calling the receiving checker. Reference-only sharing follows `docs/v3/spec.md:640-645`; the defect is the conflict with context-dependent checks and needs a contract decision.

**Impact:** Unauthorized or Available can be reported for a context in which the opposite is true. This produces false anomaly status or hints and can temporarily suppress drift observations. The cache alone does not transfer credential bytes across namespaces.

**Proof:** checker A returned Unauthorized; checker B's direct-check control returned a descriptor successfully. B's first scheduler callback received Unauthorized with `Areads=1 Breads=0`. Later windows did execute B's check: this is false verdict reuse, not demonstrated permanent starvation.

**Remediation / regression:** include effective auth/transport identity in verdict reuse, or deliberately establish one consistent global credential universe and availability meaning. Keep pacing per host. Test monitors with disjoint namespaces and only one valid pull credential, mirror drift versus alternative checks, and rotation that changes identity.

## R06 — P3 — CA-bundle rejection depends on malformed block order

**Evidence: reproduced.** **References:** `internal/registry/tls_verify.go:17-28`; `internal/registry/tls_test.go:293-304`; `docs/configuration.md:206-211`.

**Trigger:** an invalid-base64 certificate PEM block precedes a valid certificate in a bundle.

**Expected / observed:** the documented whole-bundle rejection of malformed certificate blocks should not depend on ordering. `pem.Decode` skips the malformed prefix to find the valid block; the validator only looks for malformed markers after Decode returns nil.

**Impact / proof:** invalid-before-valid returned one certificate and no error; valid-before-invalid returned zero certificates and the truncated-block error. Operator input is silently partially accepted. This does not trust invalid certificate bytes and is not a verification bypass.

**Remediation / regression:** validate every PEM marker while allowing documented comments, or explicitly narrow the acceptance contract. Test malformed prefix, middle and suffix blocks, valid chains and comments.

## B01 — P2 — Acceptance cleanup waits for mirrors before removing the Pods that hold them

**Evidence: code inspection, independently cross-checked.** **References:** `test/acceptance/guard.go:380-418,436-455,486-500`; `internal/controller/kuik/imagemirror_finalizer.go:77-82,103-115`.

**Trigger:** an acceptance scenario leaves a live Pod using an ImageMirror destination and then runs cleanup.

**Expected / observed:** cleanup must first stop destination users, retain management credentials until tag cleanup completes, and then remove remaining resources. `owned` lists CRs, installation-namespace Secrets and test namespaces, but no Pods. `runCleanup` deletes and waits for CRs before deleting the namespaces containing their Pods. The mirror finalizer deliberately waits for every live Pod under the destination path, independently of selectors.

**Impact / proof:** the dependency order cannot progress while the Pod remains: namespace deletion waits on CR deletion, and CR deletion waits on the Pod. The configured 180-second deadline then returns an error before deleting the remaining objects. Resources remain and subsequent acceptance runs reject leftovers. This is a bounded timeout failure, not an indefinitely running cleanup command. No shared cluster was used to reproduce it.

**Remediation / regression:** remove owned workload users first and wait for them to stop, then delete/finalize mirrors with management Secrets still present, and finally delete Secrets/namespaces. Test a live mirrored Pod, a retained tag requiring management auth, failed finalization and ownership boundaries. Ensure cleanup cannot remove unrelated workloads to break the wait.

## B02 — P3 — Deleting a shadowed source page removes the active overlay in development

**Evidence: reproduced using a copied, unmodified sync module in a temporary fixture.** **Reference:** `website/scripts/sync-docs.mjs:275-294`.

**Trigger:** `docs/demo.md` and the website overlay both define `demo.md`; the development watcher receives an unlink event for the lower-priority source.

**Expected / observed:** the overlay should keep winning, as it does on add/change. `applySourceChange` removes the generated destination unconditionally for unlink/unlinkDir before reaching the overlay-existence guard. Fallback reconstruction applies only when the deleted source was the overlay itself.

**Impact / proof:** in the fixture, a source change preserved the generated overlay content; deleting that source removed the generated target even though the overlay file still existed. Development preview loses the page until another relevant update or full sync. A full production build restores it, so this does not establish a persistent publication failure. Directory unlink has the same unconditional removal path by inspection.

**Remediation / regression:** resolve effective source precedence for deletions as well as writes; preserve/rebuild overlay-covered files and subtrees. Test source and overlay add/change/unlink, directory removal with partial overlay coverage, and fallback when the overlay itself disappears.

## Excluded hypothesis and evidence limits

C04 is deliberately excluded. The suspected deletion-event filter defect assumed deletion would preserve an ImageMirror's generation. Envtest observed generation 2→3, the watch enqueued the update, and the corrected finalization test passed. The initial failed premise is not a product failure.

R02, R03 and R05 expose contradictions between interacting contracts; their fixes require a design decision rather than silently changing the specification. Deferred cloud-provider work is not reported as a new defect. Live kubelet pulls, provider exchange, production registry dialects and large-cluster load were outside the executed reproductions. Further structural recommendations are in [architecture and maintainability](./04-architecture-and-maintainability.md).

# kube-image-keeper (kuik) v2 — Research Report

**Date:** 2026-02-24
**Repository:** `github.com/enix/kube-image-keeper`
**Stack:** Go 1.25, Kubebuilder v4, controller-runtime v0.20.4

---

## 1. Purpose & High-Level Overview

kuik is a Kubernetes operator that transparently reroutes container image pulls through configurable mirror registries. When a Pod is created or updated, a mutating admission webhook intercepts the request, inspects each container's image reference, consults the user-defined CRDs to build an ordered list of mirror alternatives, performs live availability checks (HTTP HEAD) against those mirrors, then rewrites the image to the first available alternative.

The controllers back-fill the mirror registries by proactively copying images from source registries, and clean up stale mirror entries when images are no longer in use.

**Domain:** `kuik.enix.io`
**API version:** `v1alpha1`

---

## 2. Custom Resource Definitions

Four CRDs in two conceptual families:

### 2.1 ImageSetMirror / ClusterImageSetMirror (ISM / CISM)

Define *where* to mirror a set of images.

```
ClusterImageSetMirror  →  cluster-scoped  (shortName: cism)
ImageSetMirror         →  namespaced      (shortName: ism)
```

**Spec fields:**

| Field | Type | Description |
|---|---|---|
| `priority` | `int` | CR-level ordering. Negative = before original image, positive = after. Default 0. |
| `imageFilter` | `ImageFilterDefinition` | Include/exclude regex patterns to select images. |
| `cleanup` | `Cleanup` | Whether to delete mirrored images after a retention duration. |
| `mirrors` | `[]Mirror` | Destinations to mirror matching images to. |

**Mirror fields:**

| Field | Type | Description |
|---|---|---|
| `registry` | `string` | Registry hostname. |
| `path` | `string` | Path prefix within the registry. |
| `priority` | `uint` | Intra-CR ordering (unsigned, lower = higher priority). |
| `credentialSecret` | `*CredentialSecret` | Pull secret for authenticating to this destination. |
| `cleanup` | `*Cleanup` | Per-mirror retention override (not yet fully wired). |

**Status** (`ImageSetMirrorStatus`) holds a `matchingImages` list: for each image matched by the filter, there is a `MatchingImage` entry with:
- The normalized image reference (`image`)
- A list of `MirrorStatus` per destination (`image`, `mirroredAt`, `lastError`)
- An `unusedSince` timestamp (set when the image stops being used by any pod; used to drive cleanup)

`Mirrors.GetCredentialSecretForImage(image)` returns the credential secret whose `registry/path` prefix is the longest match for `image`.

### 2.2 ReplicatedImageSet / ClusterReplicatedImageSet (RIS / CRIS)

Define *equivalence sets* of upstream registries: an image from any upstream is transparently available from all other upstreams.

```
ClusterReplicatedImageSet  →  cluster-scoped  (shortName: cris)
ReplicatedImageSet         →  namespaced      (shortName: ris)
```

**Spec fields:**

| Field | Type | Description |
|---|---|---|
| `priority` | `int` | Same semantics as ISM/CISM. |
| `upstreams` | `[]ReplicatedUpstream` | List of equivalent registries/paths. |

**ReplicatedUpstream fields:**

| Field | Type | Description |
|---|---|---|
| `registry` | `string` (via `ImageReference`) | Registry hostname. |
| `path` | `string` (via `ImageReference`) | Path prefix. |
| `priority` | `uint` | Intra-CR ordering. |
| `imageFilter` | `ImageFilterDefinition` | Selects which images from *this specific upstream* trigger replication. |
| `credentialSecret` | `*CredentialSecret` | Pull credentials for this upstream. |

**Status** is currently empty (placeholder).

### 2.3 ImageReference

A simple inline struct `{Registry, Path}` with a `Reference()` method returning `registry/path` (or just `path` if registry is empty).

### 2.4 CredentialSecret

```go
type CredentialSecret struct {
    Name      string
    Namespace string  // ignored for namespaced resources; parent namespace is used
}
```

---

## 3. The Webhook: Image Rewriting

**File:** `internal/webhook/core/v1/pod_webhook.go`
**Registered for:** Pod CREATE and UPDATE
**Failure policy:** `Ignore` (pod creation proceeds even if webhook fails)

### 3.1 Entry Point

`PodCustomDefaulter.Default()` → `defaultPod()`

The defaulter is registered via `SetupPodWebhookWithManager()`, which also initialises two in-memory TTL caches (both TTL = 1 second):
- `checkCache` (`otter.Cache[string, bool]`, capacity 1000): caches image availability results.
- `alternativeCache` (`otter.Cache[string, *AlternativeImage]`, capacity 100): caches the selected best alternative per image.

A `singleflight.Group` deduplicates concurrent requests for the same image.

### 3.2 Container Collection and Idempotency

The webhook reads the `kuik.enix.io/original-images` annotation (a JSON `map[string]string` of container name → original image). For any container whose name is already in this map, the container has already been processed; it is skipped. This is how the webhook avoids double-processing on re-admission.

Init container names are prefixed with `"init:"` to avoid collisions.

After collecting the containers to process, the annotation is updated with the current originals. Containers using digest-based images (`@sha256:...`) or unparseable references are silently skipped.

### 3.3 Listing CRDs

Four lists are retrieved from the API server:
- `ClusterImageSetMirrorList` (cluster-wide)
- `ImageSetMirrorList` (pod's namespace)
- `ClusterReplicatedImageSetList` (cluster-wide)
- `ReplicatedImageSetList` (pod's namespace)

CISMs are normalised into `ImageSetMirror` objects (with empty namespace) so the same code processes both. Namespaced ISMs and RISs have their `credentialSecret.Namespace` filled from the parent resource namespace (CISMs already carry the namespace in the spec; ISMs/RISs infer it).

### 3.4 Building the Alternatives List

For each container, `buildAlternativesList()` constructs an ordered list of `AlternativeImage`:

**Step 1 — start with the original image:**
```
alternatives = [{reference: normalizedOriginal, typeOrder: Original}]
```

**Step 2 — scan ImageSetMirrors:**

For each ISM/CISM, apply its `ImageFilter` to the normalized image. On match, for each mirror in `spec.mirrors`, append:
```
{
  reference:        path.Join(mirror.Registry, mirror.Path, imgPath),
  credentialSecret: mirror.CredentialSecret,
  secretOwner:      ism,
  crPriority:       ism.Spec.Priority,
  intraPriority:    mirror.Priority,
  typeOrder:        CISM or ISM,
  declarationOrder: index in mirrors array,
}
```
`imgPath` is the image reference *without* its registry component.

**Step 3 — scan ReplicatedImageSets:**

For each RIS/CRIS, find the upstream whose `imageFilter.MustBuildWithRegistry(upstream.Registry)` matches the image. On match:
- Compute `suffix = normalizedImage - prefix` (where prefix = `upstream.Registry/upstream.Path`)
- For every upstream (not just the matching one), append:
```
{
  reference:    path.Join(upstream.Registry, upstream.Path) + suffix,
  ...
}
```
This generates all equivalent locations for the image.

**Step 4 — sort stably:**

Sort key: `(crPriority ASC, typeOrder ASC, intraPriority ASC, declarationOrder ASC)`

Type ordering constants:
```
Original=0, CISM=1, ISM=2, CRIS=3, RIS=4
```

So with equal priorities: original comes first, then cluster-scoped variants, then namespaced variants.

**Step 5 — deduplicate and load secrets:**

The `Container.addAlternative()` method uses a `map[string]struct{}` to deduplicate by reference. Then `loadAlternativesSecrets()` fetches the K8s Secret for each alternative that references one.

### 3.5 Selecting the Best Alternative

`findBestAlternative()` calls `parallel.FirstSuccessful()` on all alternatives. Each goroutine calls `checkImageAvailabilityCached()`:

1. Check `checkCache` (TTL=1s). On hit, return cached result.
2. Use `singleflight` to collapse concurrent requests for the same image.
3. `checkImageAvailability()`: HTTP HEAD via `registry.Client` with the pod pull secrets + the alternative's own secret. Interprets the response:
   - Success → `ImageAvailable`
   - HTTP 404 → `ImageNotFound` (triggers stale mirror cleanup, see below)
   - HTTP 401/403 → `ImageInvalidAuth`
   - `ratelimit-remaining: 0;*` header → `ImageQuotaExceeded`
   - Other error → `ImageUnreachable`

`FirstSuccessful` returns the first available result *in declaration order*, even though all goroutines run concurrently. If the original image is first in the list and available, it is returned unchanged (the webhook then skips the rewrite since `original == alternative`).

### 3.6 Pod Mutation

If an alternative is found and differs from the original:
1. `container.Image` is rewritten.
2. If the alternative has an `ImagePullSecret` and the pod's namespace differs from the secret's namespace, `ensureSecret()` creates/updates a copy of the secret in the pod's namespace with a deterministic name: `kuik-<secretName>-<xxhash64(sourceNamespace/ownerUID)>` (truncated to fit the 253-char DNS label limit).
3. The secret reference is injected into `pod.Spec.ImagePullSecrets` if not already present.

Secret copies are labelled with owner metadata (`kuik.enix.io/owner-uid`, `owner-version`, `owner-group`, `owner-kind`, `owner-name`) so `SecretOwnerReconciler` can track and clean them up.

### 3.7 Stale Mirror Cleanup (from the webhook)

When `checkImageAvailability` returns `ImageNotFound` and the alternative has a `SecretOwner`:
- A background goroutine calls `clearStaleMirrorStatus()`.
- It only handles ISM/CISM owners (not RIS/CRIS, which have no mirroring status).
- It finds the `matchingImages[].mirrors[]` entry where `mirror.image == alternative.Reference` and `mirror.mirroredAt != nil`.
- It uses **Server-Side Apply (SSA)** with field owner `"kuik-webhook"` to first take ownership of `mirroredAt`, then remove it.
- Removing `mirroredAt` signals the controller to re-mirror the image on its next reconciliation.

---

## 4. Controllers

### 4.1 ImageSetMirrorBaseReconciler (shared logic)

**File:** `internal/controller/kuik/commonimagesetmirror.go`

This base struct is embedded by both `ImageSetMirrorReconciler` and `ClusterImageSetMirrorReconciler`.

**`mirrorImage()`:** Copies an image from its source registry to a mirror destination.
1. Fetches pod pull secrets for the source image.
2. Fetches the destination secret via `mirrors.GetCredentialSecretForImage()`.
3. Uses `registry.Client.GetDescriptor()` to retrieve the source image metadata.
4. Uses `registry.Client.CopyImage()` to copy the image (architecture filter: `["amd64"]` — only amd64 is mirrored).
5. Updates `mirror.MirroredAt` to `time.Now()`.

**`cleanupMirror()`:** Deletes an image from a mirror registry using `registry.Client.DeleteImage()`.

**`mergePreviousAndCurrentMatchingImages()`:** Core logic for updating ISM status.
1. Calls `podsByNormalizedMatchingImages()` to find which pods in scope use images matching the ISM filter, while excluding images whose references start with any known mirror prefix (to prevent mirror-loop tracking).
2. Builds a new `matchingImagesMap` from current pod images.
3. Calls `updateUnusedSince()` to compare with the previous status:
   - Image no longer matching filter → `unusedSince = time.Time{} + 1h` (sentinel for instant expiry)
   - Image still matches but no pod uses it → `unusedSince = now` (start retention clock)
   - Image is back in use → `unusedSince = nil` (reset)
4. Merges existing mirror statuses with newly computed ones (`mergeMirrors()`).

**`normalizedImageNamesFromPod()`:** Collects all image references from a pod (both current and original via annotation), normalizes them, and deduplicates. Digest-based images are skipped.

**`getAllMirrorPrefixes()`:** Returns a `map[string][]string` of namespace → mirror prefixes, collecting from all CISMs (keyed by `""`) and all ISMs (keyed by their namespace). Used to filter out mirror-origin images when computing `matchingImages`. When `ignoreNamespaces=true` (for CISM reconciler), all ISM prefixes are bucketed under `""`.

**Rate limiter:** `newMirroringRateLimiter()` combines exponential backoff (1s → 1000s) with a token-bucket limiter (10 req/s, burst 100). Both ISM and CISM controllers use this.

### 4.2 ImageSetMirrorReconciler (namespaced)

**File:** `internal/controller/kuik/imagesetmirror_controller.go`

Reconciles `ImageSetMirror` objects.

**Reconciliation loop:**

1. Fetch the ISM; ignore if not found.
2. List pods *in the ISM's namespace*.
3. If `deletionTimestamp` is set and finalizer present: delete all mirrored images (skip if `mirroredAt` is nil), remove finalizer.
4. Ensure `kuik.enix.io/mirror-cleanup` finalizer is present.
5. Get all mirror prefixes (namespace-aware: `getAllMirrorPrefixes(ctx, false)`).
6. Merge current pod images with previous status → patch status.
7. **Cleanup phase:** Iterate `matchingImages` with `unusedSince` set. For each mirror:
   - If cleanup disabled: keep.
   - If retention not expired: keep, schedule requeue for `deleteAfter` duration.
   - If retention expired: call `cleanupMirror()`, remove from status.
8. Patch status after cleanup.
9. **Mirroring phase:** For each `matchingImage` not marked unused, for each mirror without `mirroredAt`: call `mirrorImage()`, record error or clear `lastError`.
10. Patch status after mirroring.

**Pod watch:** Maps pod changes to ISMs in the same namespace whose filter matches any of the pod's images. Uses `GenerationChangedPredicate` on the ISM itself (only re-reconcile when spec changes, not status).

### 4.3 ClusterImageSetMirrorReconciler (cluster-scoped)

**File:** `internal/controller/kuik/clusterimagesetmirror_controller.go`

Near-identical to ISM reconciler with two differences:
- Lists pods from **all namespaces** (`cism.Namespace` is empty).
- Uses `getAllMirrorPrefixes(ctx, true)` — all ISM prefixes are treated as cluster-wide.
- Finalizer operations use `retry.RetryOnConflict` (the ISM reconciler does not — this appears to be an asymmetry worth noting).
- Pod watch maps pod changes to all CISMs cluster-wide.

### 4.4 SecretOwnerReconciler[T] (generic)

**File:** `internal/controller/kuik/secretowner_controller.go`

Generic controller parameterized on `T client.Object`. Currently instantiated for `ClusterImageSetMirror` and `ClusterReplicatedImageSet`.

Manages the `kuik.enix.io/secret-cleanup` finalizer. On deletion:
- Lists all Secrets with `kuik.enix.io/owner-uid = <owner.UID>` across all namespaces.
- Deletes them all (ignores NotFound).
- Removes the finalizer.

---

## 5. Registry Package

**Files:** `internal/registry/registry.go`, `transport.go`, `keychain.go`, `credentialprovider/`

### 5.1 Client

Fluent builder pattern:
```go
registry.NewClient(insecureRegistries, rootCAs).
    WithTimeout(d).
    WithPullSecrets(secrets).
    ReadDescriptor(http.MethodHead, imageName)
```

`Execute()` is the central dispatcher:
1. Build keychains from pull secrets (`GetKeychains()`).
2. Parse image reference.
3. For each keychain, call the action. Stop at first success.
4. If all fail, join all errors.
5. Falls back to `authn.DefaultKeychain` if no keychains are provided.

**`ReadDescriptor(httpMethod, imageName)`:** Returns `(*v1.Descriptor, http.Header, error)`. The headers are captured via `HeaderCapture` and returned to allow rate-limit detection by the caller.

**`GetDescriptor(imageName)`:** Full `remote.Get()` returning `*remote.Descriptor` (includes manifest data).

**`CopyImage(src, dest, architectures)`:**
- For image indexes (multi-arch manifests): uses `mutate.RemoveManifests()` to filter to the requested architectures, then `remote.WriteIndex()`.
- For single-image manifests: `remote.Write()`.

**`DeleteImage(imageName)`:** `remote.Head()` to get digest → `remote.Delete()` by digest. Silently ignores 404.

### 5.2 HeaderCapture (transport.go)

A `http.RoundTripper` wrapper that stores the last response headers. Thread-unsafe by design (single-use per request, consistent with how `Execute()` uses it).

Rate limit detection in the webhook:
```go
func isRateLimited(headers http.Header) bool {
    return strings.HasPrefix(headers.Get("ratelimit-remaining"), "0;")
}
```

### 5.3 Keychain (keychain.go)

`GetKeychains(imageName, secrets)` parses each K8s secret's docker config JSON and returns a list of `authn.Keychain` implementations. Each keychain is an `authConfigKeychain` wrapping a `BasicDockerKeyring` (adapted from k8s.io/kubelet).

`BasicDockerKeyring` performs longest-prefix matching on registry hostnames. Handles Docker Hub's multiple aliases and the legacy `.dockercfg` format.

---

## 6. Filter Package

**Files:** `internal/filter/filter.go`, `include_exclude.go`, `prefix_include_exclude.go`

### IncludeExcludeFilter

Compiles include/exclude patterns as Go regexps. Matching uses `FindString(s) == s` (full-string match, not substring). Logic:

```
match = (some include matches) AND NOT (any exclude matches)
```

If no include patterns are provided but exclude patterns exist, a `.*` catch-all include is added automatically.

### PrefixIncludeExcludeFilter

Extends `IncludeExcludeFilter` with a prefix check. Used by `ReplicatedImageSet`:
```go
upstream.ImageFilter.MustBuildWithRegistry(upstream.Registry)
```
It first strips the prefix from the candidate string, then applies the embedded filter to the suffix. This allows `imageFilter` patterns to be written relative to the upstream path (without needing to repeat the registry).

---

## 7. Parallel Package

**File:** `internal/parallel/parallel.go`

`FirstSuccessful[P, R](params []P, f func(*P) (*R, bool)) *R`

Spawns one goroutine per element. All goroutines write to a buffered channel. A `pending` slice indexed by original position serves as a reorder buffer: the function scans from `nextToReturn` and returns the first successful result in declaration order, even if a later result arrives first. If no result is successful, returns nil.

This is key to the priority system: the webhook tries all alternatives concurrently but returns the best one by priority, not by which check completes fastest.

---

## 8. Config Package

**File:** `internal/config/config.go`

Loads `config.yaml` using koanf with defaults:

```yaml
routing:
  activeCheck:
    timeout: 1s
```

Supports `time.Duration` string parsing via mapstructure hook. Currently only one configurable parameter: the HTTP HEAD timeout used by the webhook's availability checks.

---

## 9. Supporting Infrastructure

### 9.1 Labels and Annotations (field_names.go)

```
kuik.enix.io/original-images    # annotation: JSON map of container name → original image
kuik.enix.io/secret-cleanup     # finalizer on CISM/CRIS owners
kuik.enix.io/mirror-cleanup     # finalizer on ISM/CISM
kuik.enix.io/owner-uid          # label on managed secrets
kuik.enix.io/owner-version      # label on managed secrets
kuik.enix.io/owner-group        # label on managed secrets
kuik.enix.io/owner-kind         # label on managed secrets
kuik.enix.io/owner-name         # label on managed secrets
```

### 9.2 Metrics (internal/controller/collector.go, internal/info/info.go)

- `kube_image_keeper_build_info{version, revision, ...}` gauge (always 1)
- `kube_image_keeper_is_leader` gauge (1 when this instance holds the leader lease)
- `kube_image_keeper_up` gauge (1 when healthy)

Several metric collectors (image monitoring counters, gauge, histogram) are commented out, suggesting future monitoring features.

### 9.3 Test Infrastructure (internal/testsetup/setup.go)

Uses envtest (in-process k8s API server + etcd). Test suites follow the Ginkgo BDD pattern. A custom Gomega formatter provides better regexp diffs.

---

## 10. Key Design Decisions and Specificities

### 10.1 Two-Level Priority System

The priority system is intentionally asymmetric:
- **CR-level priority** (`spec.priority`) is `int` (signed) — allows "before" (negative) or "after" (positive) the original image.
- **Intra-CR priority** (`mirror.priority` / `upstream.priority`) is `uint` (unsigned) — only controls relative ordering *within* the same CR and priority level.

The sort key `(crPriority, typeOrder, intraPriority, declarationOrder)` means YAML order is the tiebreaker of last resort, giving deterministic behavior even with zero priorities.

### 10.2 Cluster-vs-Namespace Scoping

The webhook processes ISMs from the pod's namespace + all CISMs. Both are normalised to `ImageSetMirror` with empty namespace indicating cluster-scope. The CISM/ISM distinction is preserved only through the `typeOrder` field (`crTypeOrderCISM < crTypeOrderISM`), giving CISMs slightly higher default priority than ISMs at equal CR-level priorities.

### 10.3 Mirror Loop Prevention

When computing `matchingImages`, any pod image whose reference starts with a known mirror prefix is excluded. Mirror prefixes are gathered from *all* ISMs/CISMs so a pod running a mirrored image never causes that image to also be treated as a source — preventing infinite mirroring chains.

### 10.4 Annotation-Based Original Image Tracking

The `kuik.enix.io/original-images` annotation is the source of truth for the original images before any rewriting. The controllers read this annotation (via `normalizedImageNamesFromPod`) to correctly identify which source images are actually in use even after the webhook has rewritten them. Without this, a pod running `mirror.example.com/library/nginx:latest` would never be recognized as consuming `docker.io/library/nginx:latest`.

The annotation also drives idempotency: if the webhook is called again (e.g., Pod update), already-processed containers are skipped.

### 10.5 Architecture Limitation

Image mirroring is hardcoded to `["amd64"]` only. This is a significant constraint for users who need multi-arch images or ARM workloads. The architecture list is passed to `CopyImage()` but not configurable through any CRD or config.

### 10.6 SSA for Stale Mirror Cleanup

The webhook uses Server-Side Apply (SSA) with a two-step process to clear stale `mirroredAt` timestamps:
1. First patch *takes ownership* of the field by writing its current value (so SSA field management is assigned to `kuik-webhook`).
2. Second patch *removes* the field by omitting it.

This two-step approach is necessary because SSA only removes a field if the field manager that owns it explicitly sends a patch without it.

### 10.7 Secret Name Uniqueness

Cross-namespace secret copies are named `kuik-<secretName>-<xxhash64(sourceNamespace/ownerUID)>`. The hash input includes both the source namespace and owner UID to avoid collisions when multiple CISMs/CRISs have secrets with the same name in different namespaces.

### 10.8 Caching Strategy

The webhook uses very short TTLs (1 second) deliberately. This prevents thundering-herd issues during rapid Pod scheduling while ensuring stale cache entries expire quickly. The `singleflight.Group` further collapses concurrent checks for the same image within a single request burst.

### 10.9 ISM vs CISM Reconciler Asymmetry

The CISM reconciler uses `retry.RetryOnConflict` for all finalizer operations; the ISM reconciler does not. This asymmetry appears unintentional — cluster-scoped resources may be more likely to experience conflicts from the SecretOwnerReconciler writing the same resource, but the ISM reconciler should be equally robust.

### 10.10 ReplicatedImageSet Has No Mirroring Controller

Unlike ISM/CISM, the `ReplicatedImageSet` / `ClusterReplicatedImageSet` have no dedicated controller performing actual image copies. The `SecretOwnerReconciler` manages their owned secrets, but there is no equivalent of `mirrorImage()` for RIS/CRIS. The assumption is that the referenced upstreams already have the images and kuik only reroutes (redirects via webhook); no active copying is performed for RIS/CRIS.

---

## 11. Data Flow Diagrams

### Pod Admission (Webhook)

```
Pod CREATE/UPDATE
│
├── Read kuik.enix.io/original-images annotation
├── Collect unprocessed containers (skip already-annotated, skip digest-based)
├── Update annotation with current original images
│
├── List CISM (cluster) + ISM (namespace) + CRIS (cluster) + RIS (namespace)
├── Normalise CISMs → ISMs (empty namespace), CRISs → RISs
│
└── For each container:
    │
    ├── findBestAlternativeCached (alternativeCache + singleflight)
    │   │
    │   ├── buildAlternativesList
    │   │   ├── Start with original (typeOrder=0)
    │   │   ├── Scan ISMs/CISMs: add mirror alternatives
    │   │   ├── Scan RIS/CRIS: find matching upstream, add all upstreams as alternatives
    │   │   ├── Sort by (crPriority, typeOrder, intraPriority, declarationOrder)
    │   │   └── Load secrets for each alternative
    │   │
    │   └── findBestAlternative
    │       └── parallel.FirstSuccessful → checkImageAvailabilityCached
    │           ├── checkCache hit → return cached
    │           └── singleflight → checkImageAvailability (HTTP HEAD)
    │               ├── Available → cache true, return
    │               └── NotFound → cache false, clearStaleMirrorStatus (goroutine)
    │
    ├── If alternative == original → skip (no rewrite)
    └── Rewrite container.Image
        ├── ensureSecret (copy to pod namespace if needed)
        └── Inject secret into pod.Spec.ImagePullSecrets
```

### Controller Reconciliation (ISM/CISM)

```
ISM/CISM changed OR Pod in scope changed
│
├── Fetch ISM/CISM
├── List Pods (in namespace or cluster-wide)
│
├── [Deletion path] → cleanupMirror all mirrored images → remove finalizer
│
├── Ensure mirror-cleanup finalizer
│
├── getAllMirrorPrefixes → collect all mirror destinations (for loop detection)
│
├── mergePreviousAndCurrentMatchingImages
│   ├── podsByNormalizedMatchingImages (filter out mirror-prefixed images)
│   ├── Build new matchingImagesMap from current pods
│   └── updateUnusedSince (compare with previous status)
│
├── Patch status (new matching images list)
│
├── [Cleanup phase] For each matchingImage with unusedSince:
│   ├── cleanup disabled → keep
│   ├── retention not expired → keep, schedule requeueAfter
│   └── retention expired → cleanupMirror, remove from status
│
├── Patch status (after cleanup)
│
└── [Mirror phase] For each active matchingImage without mirroredAt:
    ├── mirrorImage (GetDescriptor + CopyImage, amd64 only)
    ├── Set mirroredAt on success
    └── Set lastError on failure
    └── Patch status per image
```

---

## 12. Known Issues and TODOs (from source comments)

| Location | Issue |
|---|---|
| `pod_webhook.go:407` | `FIXME`: when building ISM alternatives, if the image doesn't match the filter, it should also check if the image matches a mirrored reference (for images already rewritten). |
| `imagesetmirror_types.go:85` | `TODO`: `credentialSecret.Namespace` should be required for CISM and prohibited for ISM (currently ignored for namespaced resources). |
| `commonimagesetmirror.go:252` | `FIXME`: `mergeMirrors` does not remove mirrors present in current but absent from expected (stale mirror entries accumulate). |
| `imagesetmirror_controller.go:125` | `TODO`: merge per-mirror retention options (currently only spec-level cleanup is honoured). |
| `imagesetmirror_types.go:54` | `TODO`: add a validating webhook to ensure include/exclude regexps are valid at admission time (currently invalid regexps panic in `MustBuild()`). |
| `pod_webhook.go:530-545` | Commented-out code: planned `RegistryMonitor` CRD integration for per-registry health check configuration. |
| `collector.go` | Commented-out metric definitions for image monitoring, monitor age histograms. |
| `main.go:75` | `--unused-image-ttl` flag is parsed but not wired to any actual configuration (unused). |

---

## 13. Entry Point and Deployment Configuration

**`cmd/main.go`** flags:

| Flag | Default | Description |
|---|---|---|
| `--metrics-bind-address` | `0` (disabled) | Metrics endpoint address. |
| `--health-probe-bind-address` | `:8081` | Readiness/liveness probe address. |
| `--leader-elect` | `false` | Enable leader election (ID: `e69ca865.enix.io`). |
| `--metrics-secure` | `true` | Serve metrics over HTTPS. |
| `--webhook-cert-path` | `""` | Directory for webhook TLS certificate. |
| `--enable-http2` | `false` | HTTP/2 disabled by default (CVE mitigation). |
| `--unused-image-ttl` | `24` | Parsed but currently unused. |
| `--config` | `/etc/kube-image-keeper/config.yaml` | Config file path. |
| `--zap-*` | various | Standard controller-runtime zap logging flags. |

**ENABLE_WEBHOOKS** env var: set to `"false"` to disable the Pod webhook (useful for controller-only deployments or local development).

HTTP/2 is disabled by default to prevent CVE vulnerabilities GHSA-qppj-fm5r-hxr3 and GHSA-4374-p667-p6c8 (HTTP/2 Stream Cancellation and Rapid Reset attacks).

The webhook server and metrics server each support independent TLS certificate watchers for hot-reload of certificates.

---

## 14. Dependencies of Note

| Package | Purpose |
|---|---|
| `github.com/google/go-containerregistry` | All registry interactions (read, copy, delete, HEAD) |
| `github.com/maypok86/otter` | High-performance in-memory TTL cache (Ristretto-inspired) |
| `go4.org/syncutil/singleflight` | Deduplication of concurrent requests for same key |
| `github.com/distribution/reference` | OCI image reference parsing and normalization |
| `github.com/knadh/koanf` | Config file loading with layered overrides |
| `github.com/cespare/xxhash` | Fast non-cryptographic hash for secret name generation |
| `sigs.k8s.io/controller-runtime` | Kubebuilder framework (manager, reconciler, webhook, cache) |
| `golang.org/x/time/rate` | Token bucket for the mirroring rate limiter |

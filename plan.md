# Implementation Plan: ClusterImageSetAvailability

## Overview

Add a new `ClusterImageSetAvailability` CRD (cluster-scoped) that:
1. Discovers images from running Pods cluster-wide by applying an `imageFilter`
2. Tracks each image's availability by periodically sending HEAD (or GET) requests to the registry
3. Rate-limits checks per registry via a drip-feed mechanism (`maxPerInterval` checks per `interval`)
4. Expires and removes images from tracking after `unusedImageExpiry` once no Pod uses them

The feature touches five areas: **config**, **API types**, **registry package refactor**, **controller**, and **main.go registration**.

---

## Phase 1 — Config Extension

**File:** `internal/config/config.go`

Add a `RegistriesMonitoring` struct that separates the baseline `Default` config from per-registry overrides stored under `Items`. The existing `kuikv1alpha1.CredentialSecret` type (defined in `api/kuik/v1alpha1/imagesetmirror_types.go`) is reused directly — no new struct needed. The `config` package will import `api/kuik/v1alpha1`, which is safe since `api/kuik/v1alpha1` only imports `internal/filter` and there is no circular dependency.

```go
import kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"

type Config struct {
    Routing              Routing              `koanf:"routing"`
    RegistriesMonitoring RegistriesMonitoring `koanf:"registriesMonitoring"`
}

// RegistriesMonitoring separates the baseline config from per-registry overrides.
type RegistriesMonitoring struct {
    // Default is the baseline configuration applied to all registries.
    // Registry-specific entries in Items override individual fields.
    Default RegistryMonitoring `koanf:"default"`

    // Items maps registry hostnames to their per-registry overrides.
    // Only the fields that differ from Default need to be specified.
    Items map[string]RegistryMonitoring `koanf:"items"`
}

type RegistryMonitoring struct {
    // Method is the HTTP method used for availability checks: "HEAD" or "GET".
    Method string `koanf:"method"`

    // Interval is the period of one drip-feed cycle for this registry.
    Interval time.Duration `koanf:"interval"`

    // MaxPerInterval is the number of images checked per Interval.
    // Together they define the tick rate: one check every Interval/MaxPerInterval.
    MaxPerInterval int `koanf:"maxPerInterval"`

    // Timeout is the per-request deadline for availability checks.
    Timeout time.Duration `koanf:"timeout"`

    // FallbackCredentialSecret is used to authenticate against the registry
    // when no running Pod references the image (e.g. it has become unused).
    // Optional. If absent, checks are attempted anonymously.
    FallbackCredentialSecret *kuikv1alpha1.CredentialSecret `koanf:"fallbackCredentialSecret"`
}
```

### Default and per-registry configuration

`Default` provides the baseline that applies to every registry. Entries in `Items` only need to specify the fields that differ from the default — unset fields (zero values) inherit the default.

```yaml
# /etc/kuik/config.yaml
registriesMonitoring:
  default:
    method: HEAD
    interval: 5m
    maxPerInterval: 1
    timeout: 5s
  items:
    docker.io:
      # Inherits method=HEAD and timeout=5s from default.
      interval: 1h
      maxPerInterval: 3
      fallbackCredentialSecret:
        name: registry-secret
        namespace: kuik-system
```

`RegistryMonitoring` has built-in defaults so that `registryConfig` always returns a usable config — no `bool` return needed. The built-in defaults are:

| Field | Default | Rationale |
|---|---|---|
| `Method` | `"HEAD"` | Lightest request for availability checks |
| `Interval` | `1h` | Conservative rate for unconfigured registries |
| `MaxPerInterval` | `1` | Single check per interval |
| `Timeout` | `0` | No timeout — `Client.newContextOption` bypasses when 0 |
| `FallbackCredentialSecret` | `nil` | Anonymous access by default |

These defaults are set through koanf's `structs.Provider(defaultConfig, "koanf")` in `config.Load`, same as the existing `Routing.ActiveCheck.Timeout`:

```go
var defaultConfig = Config{
    Routing: Routing{
        ActiveCheck: ActiveCheck{
            Timeout: time.Second,
        },
    },
    RegistriesMonitoring: RegistriesMonitoring{
        Default: RegistryMonitoring{
            Method:         http.MethodHead,
            Interval:       time.Hour,
            MaxPerInterval: 1,
        },
    },
}
```

A helper on the reconciler (see Phase 4) resolves the effective merged config for a given registry. It starts from `Default` and applies only the non-zero fields from the matching `Items` entry, so a per-registry override only needs to declare what it changes:

```go
func (r *ClusterImageSetAvailabilityReconciler) registryConfig(registry string) config.RegistryMonitoring {
    mon := r.Config.RegistriesMonitoring

    // Start from the baseline default (always has usable values).
    merged := mon.Default

    // Apply per-registry overrides: only non-zero fields replace the default.
    if override, ok := mon.Items[registry]; ok {
        if override.Method != "" {
            merged.Method = override.Method
        }
        if override.Interval != 0 {
            merged.Interval = override.Interval
        }
        if override.MaxPerInterval != 0 {
            merged.MaxPerInterval = override.MaxPerInterval
        }
        if override.Timeout != 0 {
            merged.Timeout = override.Timeout
        }
        if override.FallbackCredentialSecret != nil {
            merged.FallbackCredentialSecret = override.FallbackCredentialSecret
        }
    }

    return merged
}
```

**Note on durations:** Go's `time.Duration` does not support the `d` suffix. Users must write `720h` instead of `30d`. This should be documented on the CRD field.

---

## Phase 2 — New CRD: `ClusterImageSetAvailability`

**New file:** `api/kuik/v1alpha1/clusterimagesetavailability_types.go`

### 2.1 Status enum

The existing `ImageAvailability` int iota in `pod_webhook.go` is retired (see Phase 3 refactor). A single string-typed enum is used everywhere:

```go
// ImageAvailabilityStatus represents the result of an image availability check.
// +kubebuilder:validation:Enum=Scheduled;Available;NotFound;Unreachable;InvalidAuth;UnavailableSecret;QuotaExceeded
type ImageAvailabilityStatus string

const (
    // ImageAvailabilityScheduled means the image has not been checked yet.
    ImageAvailabilityScheduled ImageAvailabilityStatus = "Scheduled"
    // ImageAvailabilityAvailable means the registry confirmed the image exists.
    ImageAvailabilityAvailable ImageAvailabilityStatus = "Available"
    // ImageAvailabilityNotFound means the registry returned HTTP 404.
    ImageAvailabilityNotFound ImageAvailabilityStatus = "NotFound"
    // ImageAvailabilityUnreachable means the registry could not be contacted.
    ImageAvailabilityUnreachable ImageAvailabilityStatus = "Unreachable"
    // ImageAvailabilityInvalidAuth means the registry rejected the credentials.
    ImageAvailabilityInvalidAuth ImageAvailabilityStatus = "InvalidAuth"
    // ImageAvailabilityUnavailableSecret means the referenced credential secret does not exist.
    ImageAvailabilityUnavailableSecret ImageAvailabilityStatus = "UnavailableSecret"
    // ImageAvailabilityQuotaExceeded means the registry rate limit has been reached.
    ImageAvailabilityQuotaExceeded ImageAvailabilityStatus = "QuotaExceeded"
)
```

### 2.2 Spec and Status types

`MonitoredImage.Path` stores the **full normalized image reference** (e.g. `docker.io/library/nginx:1.27`, `quay.io/enix/x509-certificate-exporter:v1.0.0`). Storing the full reference — not the registry-relative path — ensures uniqueness even when a single `ClusterImageSetAvailability` monitors images from multiple registries, and avoids the ambiguity between `nginx:1.25` and `library/nginx:1.25` that would arise with Docker Hub's image normalisation.

```go
// ClusterImageSetAvailabilitySpec defines the desired monitoring configuration.
type ClusterImageSetAvailabilitySpec struct {
    // UnusedImageExpiry is how long to keep tracking an image after no Pod uses it.
    // Once elapsed the image is removed from status. Example: "720h" (equivalent to 30 days).
    // Zero means unused images are never removed.
    // +optional
    UnusedImageExpiry metav1.Duration `json:"unusedImageExpiry,omitempty"`

    // ImageFilter selects which images to monitor.
    // Patterns are matched against the registry-relative path component of each image
    // (everything after "registry/"), so patterns do not need to include the registry hostname.
    // Example include pattern: "library/nginx:.+"
    // +optional
    ImageFilter ImageFilterDefinition `json:"imageFilter,omitempty"`
}

// MonitoredImage holds the current availability state for a single image.
type MonitoredImage struct {
    // Path is the full normalised image reference, e.g. "docker.io/library/nginx:1.27".
    Path string `json:"path"`

    // Status is the result of the last availability check.
    Status ImageAvailabilityStatus `json:"status"`

    // UnusedSince is the timestamp when the last Pod referencing this image disappeared.
    // Nil means at least one Pod currently uses this image.
    // +optional
    UnusedSince *metav1.Time `json:"unusedSince,omitempty"`

    // LastError contains the error message from the last failed check, if any.
    // +optional
    LastError string `json:"lastError,omitempty"`

    // LastMonitor is the timestamp of the last availability check.
    // Nil means the image has not been checked yet (Status is Scheduled).
    // +optional
    LastMonitor *metav1.Time `json:"lastMonitor,omitempty"`
}

// ClusterImageSetAvailabilityStatus defines the observed state.
type ClusterImageSetAvailabilityStatus struct {
    // ImageCount is the total number of images currently being tracked.
    ImageCount int `json:"imageCount,omitempty"`

    // +listType=map
    // +listMapKey=path
    Images []MonitoredImage `json:"images,omitempty"`
}
```

### 2.3 CRD root types

`JSONPath` on array fields cannot count elements; `ImageCount` is a plain integer field maintained by the controller and used for the print column.

```go
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=cisa
// +kubebuilder:printcolumn:name="Images",type=integer,JSONPath=".status.imageCount",description="Total number of monitored images"

// ClusterImageSetAvailability monitors the availability of images across configured registries.
type ClusterImageSetAvailability struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`

    Spec   ClusterImageSetAvailabilitySpec   `json:"spec,omitempty"`
    Status ClusterImageSetAvailabilityStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterImageSetAvailabilityList contains a list of ClusterImageSetAvailability.
type ClusterImageSetAvailabilityList struct {
    metav1.TypeMeta `json:",inline"`
    metav1.ListMeta `json:"metadata,omitempty"`
    Items           []ClusterImageSetAvailability `json:"items"`
}

func init() {
    SchemeBuilder.Register(&ClusterImageSetAvailability{}, &ClusterImageSetAvailabilityList{})
}
```

**After adding the types**, run:

```bash
make generate    # regenerates zz_generated.deepcopy.go
make manifests   # regenerates CRD YAML in config/crd/bases/
```

---

## Phase 3 — Registry Package Refactor

Before writing the controller, two pieces of logic currently in the webhook package are moved into `internal/registry/` so they can be shared with the new controller.

### 3.1 Move `isRateLimited` → `internal/registry/ratelimit.go`

```go
// internal/registry/ratelimit.go

// IsRateLimited returns true if the registry response headers indicate the
// rate limit has been reached ("ratelimit-remaining: 0;...").
func IsRateLimited(headers http.Header) bool {
    return strings.HasPrefix(headers.Get("ratelimit-remaining"), "0;")
}
```

Update `pod_webhook.go` to call `registry.IsRateLimited(headers)` and remove the local `isRateLimited`.

### 3.2 Move `checkImageAvailability` → `internal/registry/availability.go`

The `ImageAvailability` int iota in `pod_webhook.go` is retired and replaced by `kuikv1alpha1.ImageAvailabilityStatus` everywhere. The function moves to the registry package as a standalone exported function.

```go
// internal/registry/availability.go

// CheckImageAvailability performs an HTTP HEAD or GET request against the registry
// and returns the availability status of the image along with any error encountered.
// The error is non-nil for all non-Available statuses and contains the underlying
// cause (e.g. HTTP status text, transport error). Callers that need a human-readable
// explanation (such as the controller's LastError field) can use err.Error().
func CheckImageAvailability(
    ctx context.Context,
    reference string,
    method string,
    timeout time.Duration,
    pullSecrets []corev1.Secret,
) (kuikv1alpha1.ImageAvailabilityStatus, error) {
    _, headers, err := NewClient(nil, nil).
        WithTimeout(timeout).
        WithPullSecrets(pullSecrets).
        ReadDescriptor(method, reference)

    if IsRateLimited(headers) {
        return kuikv1alpha1.ImageAvailabilityQuotaExceeded, fmt.Errorf("rate limit exceeded")
    }

    if err != nil {
        switch TransportStatusCode(err) {
        case http.StatusNotFound:
            return kuikv1alpha1.ImageAvailabilityNotFound, fmt.Errorf("image not found: %w", err)
        case http.StatusUnauthorized, http.StatusForbidden:
            return kuikv1alpha1.ImageAvailabilityInvalidAuth, fmt.Errorf("authentication failed: %w", err)
        default:
            return kuikv1alpha1.ImageAvailabilityUnreachable, fmt.Errorf("registry unreachable: %w", err)
        }
    }

    return kuikv1alpha1.ImageAvailabilityAvailable, nil
}
```

Update `pod_webhook.go`:
- Remove the `ImageAvailability` iota and its constants.
- Remove `checkImageAvailability` and `isRateLimited`.
- Rewrite `checkImageAvailabilityCached` to call `registry.CheckImageAvailability` and map `ImageAvailabilityAvailable` → `true` for the bool cache.

```go
// in pod_webhook.go — updated checkImageAvailabilityCached
func (d *PodCustomDefaulter) checkImageAvailabilityCached(...) bool {
    // ... singleflight + cache logic unchanged ...
    result, _ := registry.CheckImageAvailability(ctx, image.Reference,
        http.MethodHead, d.Config.Routing.ActiveCheck.Timeout, pullSecrets)
    available := result == kuikv1alpha1.ImageAvailabilityAvailable
    d.checkCache.Set(image.Reference, available)

    if result == kuikv1alpha1.ImageAvailabilityNotFound && image.SecretOwner != nil {
        go d.clearStaleMirrorStatus(image)
    }
    return available
}
```

---

## Phase 4 — Controller

**New file:** `internal/controller/kuik/clusterimagesetavailability_controller.go`

### 4.1 Struct

```go
// +kubebuilder:rbac:groups=kuik.enix.io,resources=clusterimagesetavailabilities,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kuik.enix.io,resources=clusterimagesetavailabilities/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// ClusterImageSetAvailabilityReconciler reconciles a ClusterImageSetAvailability object.
type ClusterImageSetAvailabilityReconciler struct {
    client.Client
    Scheme *runtime.Scheme
    Config *config.Config
}
```

### 4.2 SetupWithManager

```go
func (r *ClusterImageSetAvailabilityReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&kuikv1alpha1.ClusterImageSetAvailability{}).
        Named("kuik-clusterimagesetavailability").
        WatchesRawSource(source.TypedKind(mgr.GetCache(), &corev1.Pod{},
            handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, pod *corev1.Pod) []reconcile.Request {
                log := logf.FromContext(ctx).WithName("pod-mapper").WithValues("pod", klog.KObj(pod))

                var cisaList kuikv1alpha1.ClusterImageSetAvailabilityList
                if err := r.List(ctx, &cisaList); err != nil {
                    log.Error(err, "failed to list ClusterImageSetAvailability")
                    return nil
                }

                imageNames := normalizedImageNamesFromPod(logf.IntoContext(ctx, log), pod)

                reqs := []reconcile.Request{}
                for _, cisa := range cisaList.Items {
                    for imageName := range imageNames {
                        reg, _, err := internal.RegistryAndPathFromReference(imageName)
                        if err != nil {
                            continue
                        }
                        imageFilter := cisa.Spec.ImageFilter.MustBuildWithRegistry(reg + "/")
                        if imageFilter.Match(imageName) {
                            reqs = append(reqs, reconcile.Request{
                                NamespacedName: client.ObjectKeyFromObject(&cisa),
                            })
                            break
                        }
                    }
                }
                return reqs
            })),
        ).
        Complete(r)
}
```

### 4.3 Main Reconcile loop

The reconciler has two responsibilities per cycle:

1. **Sync image list** — reflect current Pod usage in `status.images`
2. **Drip-feed monitoring** — per unique registry present in status, check the next due image

The per-registry check logic is extracted into `checkNextForRegistry` to keep cyclomatic complexity low.

```go
func (r *ClusterImageSetAvailabilityReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    var cisa kuikv1alpha1.ClusterImageSetAvailability
    if err := r.Get(ctx, req.NamespacedName, &cisa); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    var pods corev1.PodList
    if err := r.List(ctx, &pods); err != nil {
        return ctrl.Result{}, err
    }

    // Step 1: sync the image list from current pods.
    original := cisa.DeepCopy()
    r.syncImageList(ctx, &cisa, pods.Items)
    if err := r.Status().Patch(ctx, &cisa, client.MergeFrom(original)); err != nil {
        return ctrl.Result{}, err
    }

    // Step 2: drip-feed monitoring — one check per registry per tick.
    uniqueRegistries := uniqueRegistriesFromStatus(&cisa)
    minRequeueAfter := time.Duration(math.MaxInt64)

    for _, registry := range uniqueRegistries {
        monCfg := r.registryConfig(registry)

        requeueAfter, err := r.checkNextForRegistry(ctx, &cisa, registry, monCfg, pods.Items)
        if err != nil {
            return ctrl.Result{}, err
        }
        if requeueAfter > 0 {
            minRequeueAfter = min(minRequeueAfter, requeueAfter)
        }
    }

    if minRequeueAfter == time.Duration(math.MaxInt64) {
        return ctrl.Result{}, nil // nothing to requeue for
    }
    return ctrl.Result{RequeueAfter: minRequeueAfter}, nil
}

// uniqueRegistriesFromStatus returns the deduplicated list of registry hostnames
// present in cisa.Status.Images.
func uniqueRegistriesFromStatus(cisa *kuikv1alpha1.ClusterImageSetAvailability) []string {
    seen := map[string]struct{}{}
    for _, img := range cisa.Status.Images {
        reg, _, err := internal.RegistryAndPathFromReference(img.Path)
        if err == nil {
            seen[reg] = struct{}{}
        }
    }
    return slices.Collect(maps.Keys(seen))
}
```

### 4.4 checkNextForRegistry

Performs the drip-feed logic for a single registry. `findNextImageToCheck` returns both the oldest image (next candidate) and the latest-checked image. The tick spacing (`interval / maxPerInterval`) is enforced by comparing the latest check's timestamp against `tickDuration` — no counting needed.

```go
func (r *ClusterImageSetAvailabilityReconciler) checkNextForRegistry(
    ctx context.Context,
    cisa *kuikv1alpha1.ClusterImageSetAvailability,
    registry string,
    monCfg config.RegistryMonitoring,
    pods []corev1.Pod,
) (requeueAfter time.Duration, err error) {
    log := logf.FromContext(ctx)
    tickDuration := monCfg.Interval / time.Duration(monCfg.MaxPerInterval)

    nextImage, latestChecked := findNextImageToCheck(cisa, registry)
    if nextImage == nil {
        return 0, nil // no images for this registry yet
    }

    // Respect tick spacing: if the most recent check is too recent, wait.
    if latestChecked != nil {
        timeUntilDue := tickDuration - time.Since(latestChecked.LastMonitor.Time)
        if timeUntilDue > 0 {
            return timeUntilDue, nil
        }
    }

    log.V(1).Info("checking image availability", "registry", registry, "path", nextImage.Path)
    original := cisa.DeepCopy()
    r.performCheck(ctx, nextImage, monCfg, pods)
    if err := r.Status().Patch(ctx, cisa, client.MergeFrom(original)); err != nil {
        return 0, err
    }

    return tickDuration, nil // requeue after the tick for the next image in this registry
}
```

### 4.5 syncImageList

Mirrors the `mergePreviousAndCurrentMatchingImages` pattern from ISM/CISM.

The instant-expiry marker is a non-zero timestamp far in the past (`time.Time{} + 1h`) used to mark images that no longer match the filter at all. On the next reconciliation, the expiry check removes them immediately. The non-zero value is required because `nil` and the zero `time.Time` are both treated as "no expiry" in JSON marshalling.

```go
func (r *ClusterImageSetAvailabilityReconciler) syncImageList(
    ctx context.Context,
    cisa *kuikv1alpha1.ClusterImageSetAvailability,
    pods []corev1.Pod,
) {
    log := logf.FromContext(ctx)
    now := metav1.NewTime(time.Now())
    // instantExpiryMarker triggers immediate removal on the next reconciliation.
    // It uses 0001-01-01 01:00:00 UTC (1 hour after the Go zero time) to be
    // distinguishable from nil while guaranteeing time.Since() >= any expiry duration.
    instantExpiryMarker := metav1.NewTime(time.Time{}.Add(time.Hour))

    // Build the set of currently-used full references per registry.
    currentImages := map[string]struct{}{} // full normalised references
    for i := range pods {
        for imageName := range normalizedImageNamesFromPod(ctx, &pods[i]) {
            reg, _, err := internal.RegistryAndPathFromReference(imageName)
            if err != nil {
                continue
            }
            imageFilter := cisa.Spec.ImageFilter.MustBuildWithRegistry(reg + "/")
            if imageFilter.Match(imageName) {
                currentImages[imageName] = struct{}{}
            }
        }
    }

    // Update existing status entries: track usage changes and filter scope changes.
    for i := range cisa.Status.Images {
        img := &cisa.Status.Images[i]

        reg, _, err := internal.RegistryAndPathFromReference(img.Path)
        if err != nil {
            continue
        }

        imageFilter := cisa.Spec.ImageFilter.MustBuildWithRegistry(reg + "/")
        inScope := imageFilter.Match(img.Path)

        if !inScope {
            // The image no longer matches the filter or its registry became unconfigured.
            // Use the instant-expiry marker so it is removed on the next reconciliation.
            if img.UnusedSince == nil || !img.UnusedSince.Equal(&instantExpiryMarker) {
                img.UnusedSince = &instantExpiryMarker
                log.Info("image no longer in scope, marking for removal", "path", img.Path)
            }
            continue
        }

        if _, inUse := currentImages[img.Path]; inUse {
            img.UnusedSince = nil // back in use or still in use
        } else if img.UnusedSince == nil {
            img.UnusedSince = &now // just became unused
            log.Info("image is no longer used by any pod", "path", img.Path)
        }
    }

    // Remove entries that have exceeded unusedImageExpiry.
    expiry := cisa.Spec.UnusedImageExpiry.Duration
    if expiry > 0 {
        cisa.Status.Images = slices.DeleteFunc(cisa.Status.Images, func(img kuikv1alpha1.MonitoredImage) bool {
            return img.UnusedSince != nil && time.Since(img.UnusedSince.Time) >= expiry
        })
    }

    // Add newly discovered images not yet in status.
    existingPaths := map[string]struct{}{}
    for _, img := range cisa.Status.Images {
        existingPaths[img.Path] = struct{}{}
    }
    for imageName := range currentImages {
        if _, exists := existingPaths[imageName]; !exists {
            cisa.Status.Images = append(cisa.Status.Images, kuikv1alpha1.MonitoredImage{
                Path:   imageName,
                Status: kuikv1alpha1.ImageAvailabilityScheduled,
            })
            log.Info("discovered new image to monitor", "path", imageName)
        }
    }

    cisa.Status.ImageCount = len(cisa.Status.Images)
}
```

### 4.6 findNextImageToCheck

Returns two pointers for the given registry:
- `oldest` — the image with the oldest (or nil) `LastMonitor`, i.e. the next candidate to check.
- `latest` — the image with the most recent `LastMonitor`, used by `checkNextForRegistry` for tick gating.

Both are `nil` when there are no images for this registry.

```go
func findNextImageToCheck(
    cisa *kuikv1alpha1.ClusterImageSetAvailability,
    registry string,
) (oldest, latest *kuikv1alpha1.MonitoredImage) {
    for i := range cisa.Status.Images {
        img := &cisa.Status.Images[i]

        imgRegistry, _, err := internal.RegistryAndPathFromReference(img.Path)
        if err != nil || imgRegistry != registry {
            continue
        }

        // Track oldest (nil LastMonitor is always oldest).
        if oldest == nil || img.LastMonitor == nil {
            oldest = img
        } else if oldest.LastMonitor != nil && img.LastMonitor.Before(oldest.LastMonitor) {
            oldest = img
        }

        // Track latest.
        if img.LastMonitor != nil {
            if latest == nil || img.LastMonitor.After(latest.LastMonitor.Time) {
                latest = img
            }
        }
    }
    return oldest, latest
}
```

### 4.7 performCheck

Calls `registry.CheckImageAvailability` (moved from the webhook in Phase 3) and updates the `MonitoredImage` entry in place.

```go
func (r *ClusterImageSetAvailabilityReconciler) performCheck(
    ctx context.Context,
    img *kuikv1alpha1.MonitoredImage,
    monCfg config.RegistryMonitoring,
    pods []corev1.Pod,
) {
    now := metav1.NewTime(time.Now())

    pullSecrets, err := r.resolveCredentials(ctx, img.Path, monCfg, pods)
    if err != nil {
        img.Status = kuikv1alpha1.ImageAvailabilityUnavailableSecret
        img.LastError = err.Error()
        img.LastMonitor = &now
        return
    }

    result, checkErr := registry.CheckImageAvailability(ctx, img.Path, monCfg.Method, monCfg.Timeout, pullSecrets)

    img.Status = result
    img.LastMonitor = &now
    if checkErr != nil {
        img.LastError = checkErr.Error()
    } else {
        img.LastError = ""
    }
}
```

### 4.8 resolveCredentials

```go
func (r *ClusterImageSetAvailabilityReconciler) resolveCredentials(
    ctx context.Context,
    fullRef string,
    monCfg config.RegistryMonitoring,
    pods []corev1.Pod,
) ([]corev1.Secret, error) {
    // Prefer pull secrets from a running pod that references this image.
    for i := range pods {
        pod := &pods[i]
        for imageName := range normalizedImageNamesFromPod(ctx, pod) {
            if imageName != fullRef || len(pod.Spec.ImagePullSecrets) == 0 {
                continue
            }
            secrets, err := internal.GetPullSecretsFromPod(ctx, r.Client, pod)
            if err == nil {
                return secrets, nil
            }
            // Log and try the next pod; fall through to fallback if none succeed.
            logf.FromContext(ctx).V(1).Info("could not read pod pull secrets",
                "pod", klog.KObj(pod), "error", err)
        }
    }

    // Fall back to the registry-level credential secret.
    if monCfg.FallbackCredentialSecret == nil {
        return nil, nil // attempt anonymous access
    }

    secret := &corev1.Secret{}
    key := client.ObjectKey{
        Namespace: monCfg.FallbackCredentialSecret.Namespace,
        Name:      monCfg.FallbackCredentialSecret.Name,
    }
    if err := r.Get(ctx, key, secret); err != nil {
        return nil, fmt.Errorf("fallback credential secret %s not found: %w", key, err)
    }
    return []corev1.Secret{*secret}, nil
}
```

---

## Phase 5 — Register in main.go

### 5.1 Scaffold with kubebuilder CLI first

Before editing `cmd/main.go`, run the kubebuilder CLI to generate the resource scaffolding and update the `PROJECT` file automatically:

```bash
kubebuilder create api \
  --kind ClusterImageSetAvailability \
  --version v1alpha1 \
  --group kuik \
  --resource \
  --controller=true \
  --namespaced=false
```

This updates `PROJECT` (including the controller entry), generates the types stub, and generates the controller stub along with its registration in `cmd/main.go`. Both stubs are replaced by our implementation.

### 5.2 Register the controller

kubebuilder generates the controller registration in `cmd/main.go`. The only manual edit needed afterward is adding the `Config` field, which kubebuilder cannot know about:

```go
// Generated by kubebuilder — add Config: configuration manually.
if err = (&kuikcontroller.ClusterImageSetAvailabilityReconciler{
    Client: mgr.GetClient(),
    Scheme: mgr.GetScheme(),
    Config: configuration, // add this line
}).SetupWithManager(mgr); err != nil {
    setupLog.Error(err, "unable to create controller", "controller", "ClusterImageSetAvailability")
    os.Exit(1)
}
```

---

## Phase 6 — Generate & Manifests

```bash
make generate    # regenerates zz_generated.deepcopy.go for new types
make manifests   # regenerates CRD YAML and RBAC
make lint-fix    # fix any linting issues
make test        # run unit tests
```

CRD YAML output: `config/crd/bases/kuik.enix.io_clusterimagesetavailabilities.yaml`

---

## Key Design Decisions

### Drip-feed rate computation

`tickDuration = interval / maxPerInterval`

With `interval=5m, maxPerInterval=1`: one check every 5 minutes.
With `interval=10m, maxPerInterval=2`: one check every 5 minutes (two checks spread over 10 minutes).

The reconciler computes `tickDuration` per registry and compares `time.Since(lastMonitor)` against it. If due, it checks and returns `tickDuration` as the next requeue duration. If not due, it returns the remaining time. The total cycle time to check all N images for a registry is `N × tickDuration`.

### Why no separate monitoring goroutine

The `ctrl.Result{RequeueAfter: tickDuration}` pattern is sufficient and avoids goroutine lifecycle complexity, particularly across leader-election transitions. The reconciler is triggered by Pod changes and by timer; the tick-gate ensures the monitoring rate is respected regardless of how often the reconciler runs.

### Full normalised reference as the primary key

`MonitoredImage.Path` stores the full normalised reference (e.g. `docker.io/library/nginx:1.27`) rather than the registry-relative path. This:
- Avoids Docker Hub deduplication issues (`nginx:1.25` vs `library/nginx:1.25` both normalise to `docker.io/library/nginx:1.25`)
- Works correctly when a single `ClusterImageSetAvailability` monitors images from multiple registries
- Is consistent with how `normalizedImageNamesFromPod` produces references

The registry hostname is extracted with `internal.RegistryAndPathFromReference(img.Path)` whenever needed.

### `imageFilter` patterns are registry-relative

`imageFilter` patterns are matched against the registry-relative path (everything after `registry/`) using `MustBuildWithRegistry(registry + "/")`. This means patterns like `library/nginx:.+` work naturally without embedding the registry hostname. The same filter is tested per configured (or default) registry.

### Default and per-registry config merging

`RegistriesMonitoring.Default` provides the baseline for all registries. `RegistriesMonitoring.Items` holds per-registry overrides; only non-zero fields in an override replace the corresponding default field.

The `registryConfig(registry)` helper applies this merge and always returns a valid config. When neither `Default` nor `Items[registry]` provides explicit values, the built-in defaults apply (Method=HEAD, Interval=1h, MaxPerInterval=1, Timeout=0, FallbackCredentialSecret=nil). This means every registry is always monitorable — there is no "unmonitored" state. The monitoring loop iterates over unique registries found in `status.images` (via `uniqueRegistriesFromStatus`) and resolves config through `registryConfig`.

### Unified `ImageAvailabilityStatus` type

The old `ImageAvailability` int iota in `pod_webhook.go` is retired. Both the webhook and the new controller use `kuikv1alpha1.ImageAvailabilityStatus` (the string type) via `registry.CheckImageAvailability`. The webhook's bool availability cache is populated by comparing the result against `ImageAvailabilityAvailable`.

### `unusedImageExpiry` with zero value

If `unusedImageExpiry` is not set (zero value), unused images are never removed from `status.images`. Users must explicitly opt into expiry, mirroring ISM's `cleanup.enabled` requirement.

---

## File Summary

| File | Action |
|---|---|
| `internal/config/config.go` | Add `RegistriesMonitoring` struct (with `Default` + `Items`), `RegistryMonitoring`; import `kuikv1alpha1` |
| `api/kuik/v1alpha1/clusterimagesetavailability_types.go` | **New**: CRD types, `ImageAvailabilityStatus` enum |
| `api/kuik/v1alpha1/zz_generated.deepcopy.go` | Auto-generated by `make generate` |
| `config/crd/bases/kuik.enix.io_clusterimagesetavailabilities.yaml` | Auto-generated by `make manifests` |
| `internal/registry/ratelimit.go` | **New**: exported `IsRateLimited` (moved from webhook) |
| `internal/registry/availability.go` | **New**: exported `CheckImageAvailability` (moved from webhook) |
| `internal/webhook/core/v1/pod_webhook.go` | Remove `ImageAvailability` enum, `isRateLimited`, `checkImageAvailability`; call `registry.CheckImageAvailability` |
| `internal/controller/kuik/clusterimagesetavailability_controller.go` | **New**: controller |
| `cmd/main.go` | Register new controller |
| `PROJECT` | Updated by `kubebuilder create api` CLI command |

---

## Implementation Checklist

### Phase 1 — Config Extension

- [x] **1.1** Add `RegistriesMonitoring` and `RegistryMonitoring` structs to `internal/config/config.go`
  - Add `RegistriesMonitoring` field to `Config` struct
  - Add `RegistriesMonitoring` struct with `Default` and `Items` fields
  - Add `RegistryMonitoring` struct with `Method`, `Interval`, `MaxPerInterval`, `Timeout`, `FallbackCredentialSecret` fields
  - Import `kuikv1alpha1` for `CredentialSecret` type reuse
- [x] **1.2** Add built-in defaults to `defaultConfig` in `config.go`
  - Set `RegistriesMonitoring.Default.Method` to `http.MethodHead`
  - Set `RegistriesMonitoring.Default.Interval` to `time.Hour`
  - Set `RegistriesMonitoring.Default.MaxPerInterval` to `1`
- [x] **1.3** Verify config loads correctly with koanf (`structs.Provider(defaultConfig, "koanf")`)

### Phase 2 — New CRD: ClusterImageSetAvailability

- [x] **2.1** Run `kubebuilder create api` to scaffold the resource and controller
  - `kubebuilder create api --kind ClusterImageSetAvailability --version v1alpha1 --group kuik --resource --controller=true --namespaced=false`
  - Verify `PROJECT` file is updated
- [x] **2.2** Define `ImageAvailabilityStatus` string enum in `api/kuik/v1alpha1/clusterimagesetavailability_types.go`
  - `Scheduled`, `Available`, `NotFound`, `Unreachable`, `InvalidAuth`, `UnavailableSecret`, `QuotaExceeded`
  - Add kubebuilder validation enum marker
- [x] **2.3** Define `ClusterImageSetAvailabilitySpec` struct
  - `UnusedImageExpiry metav1.Duration`
  - `ImageFilter ImageFilterDefinition`
- [x] **2.4** Define `MonitoredImage` struct
  - `Path` (full normalised reference)
  - `Status ImageAvailabilityStatus`
  - `UnusedSince *metav1.Time`
  - `LastError string`
  - `LastMonitor *metav1.Time`
- [x] **2.5** Define `ClusterImageSetAvailabilityStatus` struct
  - `ImageCount int`
  - `Images []MonitoredImage` with `+listType=map` and `+listMapKey=path`
- [x] **2.6** Define CRD root types with kubebuilder markers
  - `+kubebuilder:resource:scope=Cluster,shortName=cisa`
  - `+kubebuilder:subresource:status`
  - `+kubebuilder:printcolumn` for `ImageCount`
  - Register types in `init()`
- [x] **2.7** Run `make generate` (deepcopy) and `make manifests` (CRD YAML, RBAC)

### Phase 3 — Registry Package Refactor

- [x] **3.1** Create `internal/registry/ratelimit.go`
  - Move `isRateLimited` from `pod_webhook.go` as exported `IsRateLimited`
- [x] **3.2** Create `internal/registry/availability.go`
  - Move `checkImageAvailability` from `pod_webhook.go` as exported `CheckImageAvailability`
  - Return `(ImageAvailabilityStatus, error)` — error carries the underlying cause
  - Use `kuikv1alpha1.ImageAvailabilityStatus` enum instead of int iota
  - Import `kuikv1alpha1` and `fmt`
- [x] **3.3** Update `pod_webhook.go`
  - Remove `ImageAvailability` int iota and its constants
  - Remove local `checkImageAvailability` function
  - Remove local `isRateLimited` function
  - Update `checkImageAvailabilityCached` to call `registry.CheckImageAvailability`
  - Discard error return (`result, _ :=`), map `ImageAvailabilityAvailable` → `true` for bool cache
- [x] **3.4** Verify webhook tests still pass (`go test ./internal/webhook/...`)

### Phase 4 — Controller

- [ ] **4.1** Create `internal/controller/kuik/clusterimagesetavailability_controller.go`
  - Define `ClusterImageSetAvailabilityReconciler` struct with `client.Client`, `Scheme`, `Config`
  - Add RBAC markers for CISA, pods, secrets
- [ ] **4.2** Implement `SetupWithManager`
  - Watch `ClusterImageSetAvailability` resources
  - Watch Pods via `WatchesRawSource` with `TypedKind` mapper
  - Pod mapper: list all CISAs, check if any pod image matches a CISA's filter, enqueue matching CISAs
  - Use `normalizedImageNamesFromPod` and `MustBuildWithRegistry` for filter matching
- [ ] **4.3** Implement `normalizedImageNamesFromPod` helper
  - Iterate pod init + regular containers
  - Normalise each image name via `internal.RegistryAndPathFromReference`
  - Return `iter.Seq[string]` (or `map[string]struct{}`)
- [ ] **4.4** Implement main `Reconcile` method
  - Fetch CISA resource (ignore NotFound)
  - List all Pods cluster-wide
  - Call `syncImageList` and patch status
  - Iterate `uniqueRegistriesFromStatus`, call `checkNextForRegistry` per registry
  - Track `minRequeueAfter` with `math.MaxInt64` sentinel
  - Return `ctrl.Result{RequeueAfter: minRequeueAfter}`
- [ ] **4.5** Implement `uniqueRegistriesFromStatus` helper
  - Deduplicate registry hostnames from `status.images` paths
- [ ] **4.6** Implement `registryConfig` helper on reconciler
  - Start from `Config.RegistriesMonitoring.Default`
  - Merge non-zero fields from `Config.RegistriesMonitoring.Items[registry]`
  - Always return a valid `config.RegistryMonitoring` (no bool)
- [ ] **4.7** Implement `syncImageList`
  - Build `currentImages` set from pods (normalise, filter with `MustBuildWithRegistry`)
  - Update existing entries: clear `UnusedSince` if back in use, set `UnusedSince` if just became unused, set instant-expiry marker if out of filter scope
  - Remove entries that exceeded `unusedImageExpiry`
  - Add newly discovered images with `Scheduled` status
  - Update `ImageCount`
- [ ] **4.8** Implement `checkNextForRegistry`
  - Compute `tickDuration = interval / maxPerInterval`
  - Call `findNextImageToCheck` for `(oldest, latest)` pointers
  - Gate on tick spacing from `latest.LastMonitor`
  - Call `performCheck` and patch status
  - Return `tickDuration` as requeue
- [ ] **4.9** Implement `findNextImageToCheck`
  - Single pass returning `(oldest, latest *MonitoredImage)`
  - `oldest`: nil `LastMonitor` wins, then earliest timestamp
  - `latest`: most recent non-nil `LastMonitor`
- [ ] **4.10** Implement `performCheck`
  - Call `resolveCredentials` for pull secrets
  - Call `registry.CheckImageAvailability` → `(status, error)`
  - Set `img.Status`, `img.LastMonitor`, `img.LastError` (from error or cleared)
  - Handle `UnavailableSecret` separately when credential resolution fails
- [ ] **4.11** Implement `resolveCredentials`
  - Try pull secrets from running pods that reference the image
  - Fall back to `monCfg.FallbackCredentialSecret` if configured
  - Return `nil, nil` for anonymous access when no secret available

### Phase 5 — Register in main.go

- [ ] **5.1** Verify kubebuilder generated the controller registration in `cmd/main.go`
- [ ] **5.2** Add `Config: configuration` field to the generated controller setup block

### Phase 6 — Generate & Validate

- [ ] **6.1** Run `make generate` — regenerate `zz_generated.deepcopy.go`
- [ ] **6.2** Run `make manifests` — regenerate CRD YAML and RBAC
- [ ] **6.3** Run `make lint-fix` — fix any linting issues
- [ ] **6.4** Run `make test` — verify all unit tests pass
- [ ] **6.5** Verify CRD YAML at `config/crd/bases/kuik.enix.io_clusterimagesetavailabilities.yaml`

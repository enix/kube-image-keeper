# Implementation Plan: ClusterImageSetAvailability

## Overview

Add a new `ClusterImageSetAvailability` CRD (cluster-scoped) that:
1. Discovers images from running Pods cluster-wide by applying an `imageFilter`
2. Tracks each image's availability by periodically sending HEAD (or GET) requests to the registry
3. Rate-limits checks per registry via a drip-feed mechanism (`maxPerInterval` checks per `interval`)
4. Expires and removes images from tracking after `unusedImageExpiry` once no Pod uses them

The feature touches four areas: **config**, **API types**, **controller**, and **main.go registration**.

---

## Phase 1 — Config Extension

**File:** `internal/config/config.go`

Add a `RegistriesMonitoring` map keyed by registry hostname. Each entry configures the monitoring behaviour for that registry.

@NOTE: `CredentialSecret` is already defined in `api/kuik/v1alpha1/imagesetmirror_types.go`.

```go
type Config struct {
    Routing              Routing                        `koanf:"routing"`
    RegistriesMonitoring map[string]RegistryMonitoring  `koanf:"registriesMonitoring"`
}

type RegistryMonitoring struct {
    // Method is the HTTP method used for availability checks. "HEAD" or "GET".
    // Default: "HEAD"
    Method string `koanf:"method"`

    // Interval is the period between check cycles for this registry.
    Interval time.Duration `koanf:"interval"`

    // MaxPerInterval is the maximum number of images checked per Interval.
    // Together they define the drip-feed rate: one check every Interval/MaxPerInterval.
    MaxPerInterval int `koanf:"maxPerInterval"`

    // Timeout is the per-request timeout for availability checks.
    Timeout time.Duration `koanf:"timeout"`

    // FallbackCredentialSecret is used for images no longer referenced by any Pod.
    FallbackCredentialSecret *CredentialSecretRef `koanf:"fallbackCredentialSecret"`
}

// CredentialSecretRef is a namespace/name reference to a Kubernetes Secret.
// It is defined here (not in the API package) to keep the config package self-contained.
type CredentialSecretRef struct {
    Name      string `koanf:"name"`
    Namespace string `koanf:"namespace"`
}
```

**Default config snippet** (for documentation):

```yaml
# /etc/kuik/config.yaml
registriesMonitoring:
  docker.io:
    method: HEAD
    interval: 5m
    maxPerInterval: 1
    timeout: 5s
    fallbackCredentialSecret:
      name: registry-secret
      namespace: kuik-system
```

**Note on durations:** `metav1.Duration` / `time.Duration` in Go does not support the `d` suffix. Users must write `720h` instead of `30d`. Document this in the CRD or consider a custom webhook validation.

---

## Phase 2 — New CRD: `ClusterImageSetAvailability`

**New file:** `api/kuik/v1alpha1/clusterimagesetavailability_types.go`

### 2.1 Status enum

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

@NOTE: since one `ClusterImageSetAvailability` can monitor multiple registries, `status.path` should be the full path (eg. `quay.io/nginx/nginx` or `nginx:latest` for an image from the docker hub).

```go
// ClusterImageSetAvailabilitySpec defines the desired monitoring configuration.
type ClusterImageSetAvailabilitySpec struct {
    // UnusedImageExpiry is how long to keep tracking an image after no Pod uses it.
    // Once elapsed, the image is removed from status. Example: "720h" (30 days).
    // +optional
    UnusedImageExpiry metav1.Duration `json:"unusedImageExpiry,omitempty"`

    // ImageFilter selects which images (by registry-relative path) to monitor.
    // Patterns are matched against the path component of the image reference,
    // i.e. everything after "registry/". Example include pattern: "library/nginx:.+"
    // +optional
    ImageFilter ImageFilterDefinition `json:"imageFilter,omitempty"`
}

// MonitoredImage holds the current availability state for a single image.
type MonitoredImage struct {
    // Path is the registry-relative image reference, e.g. "library/nginx:1.27".
    // The registry is determined by the registriesMonitoring config key.
    Path string `json:"path"`

    // Status is the result of the last availability check.
    Status ImageAvailabilityStatus `json:"status"`

    // UnusedSince is the timestamp when the last Pod referencing this image disappeared.
    // Nil means the image is currently in use.
    // +optional
    UnusedSince *metav1.Time `json:"unusedSince,omitempty"`

    // LastError contains the error message from the last failed check, if any.
    // +optional
    LastError string `json:"lastError,omitempty"`

    // LastMonitor is the timestamp of the last availability check.
    // Nil means the image has not been checked yet (status is Scheduled).
    // +optional
    LastMonitor *metav1.Time `json:"lastMonitor,omitempty"`
}

// ClusterImageSetAvailabilityStatus defines the observed state.
type ClusterImageSetAvailabilityStatus struct {
    // +listType=map
    // +listMapKey=path
    Images []MonitoredImage `json:"images,omitempty"`
}
```

### 2.3 CRD root types

@NOTE: make sure that `JSONPath=".status.images[*]"` really count images and is accepted in this context

```go
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=cisa
// +kubebuilder:printcolumn:name="Images",type=integer,JSONPath=".status.images[*]",description="Number of monitored images"

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

## Phase 3 — Controller

**New file:** `internal/controller/kuik/clusterimagesetavailability_controller.go`

### 3.1 Struct

```go
// ClusterImageSetAvailabilityReconciler reconciles a ClusterImageSetAvailability object.
type ClusterImageSetAvailabilityReconciler struct {
    client.Client
    Scheme *runtime.Scheme
    Config *config.Config
}
```

### 3.2 SetupWithManager

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
                    for registry := range r.Config.RegistriesMonitoring {
                        f := cisa.Spec.ImageFilter.MustBuildWithRegistry(registry + "/")
                        for imageName := range imageNames {
                            if f.Match(imageName) {
                                reqs = append(reqs, reconcile.Request{
                                    NamespacedName: client.ObjectKeyFromObject(&cisa),
                                })
                                goto nextCisa
                            }
                        }
                    }
                nextCisa:
                }
                return reqs
            })),
        ).
        Complete(r)
}
```

### 3.3 Main Reconcile loop

The reconciler has two responsibilities on each cycle:

1. **Sync image list** — reflect current Pod usage into `status.images`
2. **Check next due image** — per registry, check 1 image if the drip-feed tick has elapsed; requeue for the next tick

@NOTE: extract per registry check into its own function so the cyclomatic complexity stays low

```go
func (r *ClusterImageSetAvailabilityReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := logf.FromContext(ctx)

    var cisa kuikv1alpha1.ClusterImageSetAvailability
    if err := r.Get(ctx, req.NamespacedName, &cisa); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    var pods corev1.PodList
    if err := r.List(ctx, &pods); err != nil {
        return ctrl.Result{}, err
    }

    // --- Step 1: sync image list ---
    original := cisa.DeepCopy()
    r.syncImageList(ctx, &cisa, pods.Items)
    if err := r.Status().Patch(ctx, &cisa, client.MergeFrom(original)); err != nil {
        return ctrl.Result{}, err
    }

    // --- Step 2: drip-feed monitoring checks ---
    minRequeueAfter := time.Duration(0)

    for registry, registryMonitoringConfig := range r.Config.RegistriesMonitoring {
        tickDuration := registryMonitoringConfig.Interval / time.Duration(registryMonitoringConfig.MaxPerInterval)

        // Find the image with the oldest lastMonitor for this registry (nil = never checked).
        nextImage := r.findNextImageToCheck(&cisa, registry)
        if nextImage == nil {
            continue // no images for this registry
        }

        // How long until this image is due for a check?
        var timeUntilDue time.Duration
        if nextImage.LastMonitor != nil {
            timeUntilDue = tickDuration - time.Since(nextImage.LastMonitor.Time)
        }
        // timeUntilDue <= 0 means the check is due now.

        if timeUntilDue > 0 {
            if minRequeueAfter == 0 || timeUntilDue < minRequeueAfter {
                minRequeueAfter = timeUntilDue
            }
            continue
        }

        // Perform the check.
        log.V(1).Info("checking image availability", "registry", registry, "path", nextImage.Path)
        original = cisa.DeepCopy()
        r.performCheck(ctx, nextImage, registry, registryMonitoringConfig, pods.Items)
        if err := r.Status().Patch(ctx, &cisa, client.MergeFrom(original)); err != nil {
            return ctrl.Result{}, err
        }

        // The next image for this registry is due after tickDuration.
        if minRequeueAfter == 0 || tickDuration < minRequeueAfter {
            minRequeueAfter = tickDuration
        }
    }

    if minRequeueAfter > 0 {
        return ctrl.Result{RequeueAfter: minRequeueAfter}, nil
    }
    return ctrl.Result{}, nil
}
```

### 3.4 syncImageList

Mirrors the `mergePreviousAndCurrentMatchingImages` logic from ISM/CISM.

@NOTE: what is Sentinel ?
@NOTE: don't use single letter names for variables (except for i): f => filter, p => path

```go
func (r *ClusterImageSetAvailabilityReconciler) syncImageList(
    ctx context.Context,
    cisa *kuikv1alpha1.ClusterImageSetAvailability,
    pods []corev1.Pod,
) {
    log := logf.FromContext(ctx)
    now := metav1.NewTime(time.Now())
    // Sentinel used to trigger instant expiry for images that no longer match the filter.
    // (Same pattern as ISM/CISM: 0001-01-01 + 1h avoids the zero-value == nil ambiguity.)
    instantExpiry := metav1.NewTime(time.Time{}.Add(time.Hour))

    // Build a set of currently-used (registry-relative) paths per registry.
    currentPaths := map[string]map[string]struct{}{} // registry -> set of paths
    for registry := range r.Config.RegistriesMonitoring {
        currentPaths[registry] = map[string]struct{}{}
        f := cisa.Spec.ImageFilter.MustBuildWithRegistry(registry + "/")

        for i := range pods {
            for imageName := range normalizedImageNamesFromPod(ctx, &pods[i]) {
                if !f.Match(imageName) {
                    continue
                }
                reg, imgPath, err := internal.RegistryAndPathFromReference(imageName)
                if err != nil || reg != registry {
                    continue
                }
                currentPaths[registry][imgPath] = struct{}{}
            }
        }
    }

    // Build a flat set of all currently-active paths (across all registries).
    allCurrentPaths := map[string]struct{}{}
    for _, paths := range currentPaths {
        for p := range paths {
            allCurrentPaths[p] = struct{}{}
        }
    }

    // Update existing status entries.
    for i := range cisa.Status.Images {
        img := &cisa.Status.Images[i]

        // Check if this image is still within the scope of a configured registry.
        inScope := false
        for registry, paths := range currentPaths {
            f := cisa.Spec.ImageFilter.MustBuildWithRegistry(registry + "/")
            fullRef := registry + "/" + img.Path
            if f.Match(fullRef) {
                inScope = true
                if _, inUse := paths[img.Path]; inUse {
                    img.UnusedSince = nil // back in use
                } else if img.UnusedSince == nil {
                    img.UnusedSince = &now // just became unused
                    log.Info("image is no longer used by any pod", "path", img.Path)
                }
                break
            }
        }

        if !inScope {
            // Image no longer matches the filter at all: mark for instant expiry.
            if img.UnusedSince == nil || !img.UnusedSince.Equal(&instantExpiry) {
                img.UnusedSince = &instantExpiry
                log.Info("image no longer matches filter, queuing for removal", "path", img.Path)
            }
        }
    }

    // Remove entries that have exceeded unusedImageExpiry.
    expiry := cisa.Spec.UnusedImageExpiry.Duration
    if expiry > 0 {
        cisa.Status.Images = slices.DeleteFunc(cisa.Status.Images, func(img kuikv1alpha1.MonitoredImage) bool {
            if img.UnusedSince == nil {
                return false
            }
            return time.Since(img.UnusedSince.Time) >= expiry
        })
    }

    // Add newly discovered images not yet in status.
    existingPaths := map[string]struct{}{}
    for _, img := range cisa.Status.Images {
        existingPaths[img.Path] = struct{}{}
    }

    for _, paths := range currentPaths {
        for p := range paths {
            if _, exists := existingPaths[p]; !exists {
                cisa.Status.Images = append(cisa.Status.Images, kuikv1alpha1.MonitoredImage{
                    Path:   p,
                    Status: kuikv1alpha1.ImageAvailabilityScheduled,
                })
                log.Info("discovered new image to monitor", "path", p)
            }
        }
    }
}
```

### 3.5 findNextImageToCheck

Returns the entry from `status.images` for the given registry that should be checked next (i.e. has the oldest `lastMonitor`, with `nil` treated as the oldest possible).

```go
func (r *ClusterImageSetAvailabilityReconciler) findNextImageToCheck(
    cisa *kuikv1alpha1.ClusterImageSetAvailability,
    registry string,
) *kuikv1alpha1.MonitoredImage {
    f := cisa.Spec.ImageFilter.MustBuildWithRegistry(registry + "/")

    var oldest *kuikv1alpha1.MonitoredImage
    for i := range cisa.Status.Images {
        img := &cisa.Status.Images[i]
        if !f.Match(registry + "/" + img.Path) {
            continue // image belongs to a different registry
        }
        if oldest == nil {
            oldest = img
            continue
        }
        // nil LastMonitor (never checked) sorts before any timestamp.
        if img.LastMonitor == nil {
            oldest = img
            continue
        }
        if oldest.LastMonitor != nil && img.LastMonitor.Before(oldest.LastMonitor) {
            oldest = img
        }
    }
    return oldest
}
```

### 3.6 performCheck

Executes the availability check and updates the `MonitoredImage` entry in place.

@NOTE: this code should re-use what have been done for `pod_webhook`. This function could call `PodCustomDefaulter.checkImageAvailability`. `checkImageAvailability` should be moved  in `internal/registry` first.

```go
func (r *ClusterImageSetAvailabilityReconciler) performCheck(
    ctx context.Context,
    img *kuikv1alpha1.MonitoredImage,
    registry string,
    registryMonitoringConfig config.RegistryMonitoring,
    pods []corev1.Pod,
) {
    log := logf.FromContext(ctx)
    fullRef := registry + "/" + img.Path
    now := metav1.NewTime(time.Now())

    // Resolve credentials: prefer pod secrets, fall back to configured secret.
    pullSecrets, err := r.resolveCredentials(ctx, fullRef, img, registryMonitoringConfig, pods)
    if err != nil {
        // Secret referenced in fallbackCredentialSecret does not exist.
        img.Status = kuikv1alpha1.ImageAvailabilityUnavailableSecret
        img.LastError = err.Error()
        img.LastMonitor = &now
        return
    }

    _, headers, err := registry.NewClient(nil, nil).
        WithTimeout(registryMonitoringConfig.Timeout).
        WithPullSecrets(pullSecrets).
        ReadDescriptor(registryMonitoringConfig.Method, fullRef)

    img.LastMonitor = &now
    img.LastError = ""

    if isRateLimited(headers) {
        img.Status = kuikv1alpha1.ImageAvailabilityQuotaExceeded
        log.V(1).Info("quota exceeded", "image", fullRef)
        return
    }

    if err != nil {
        switch registry.TransportStatusCode(err) {
        case http.StatusNotFound:
            img.Status = kuikv1alpha1.ImageAvailabilityNotFound
        case http.StatusUnauthorized, http.StatusForbidden:
            img.Status = kuikv1alpha1.ImageAvailabilityInvalidAuth
        default:
            img.Status = kuikv1alpha1.ImageAvailabilityUnreachable
        }
        img.LastError = err.Error()
        log.V(1).Info("image unavailable", "image", fullRef, "status", img.Status)
        return
    }

    img.Status = kuikv1alpha1.ImageAvailabilityAvailable
    log.V(1).Info("image available", "image", fullRef)
}
```

**Note:** `isRateLimited` is currently defined in `internal/webhook/core/v1/pod_webhook.go`. It should be moved to `internal/registry/` (e.g. `registry/ratelimit.go`) and exported so both the webhook and this controller can use it.

### 3.7 resolveCredentials

```go
func (r *ClusterImageSetAvailabilityReconciler) resolveCredentials(
    ctx context.Context,
    fullRef string,
    img *kuikv1alpha1.MonitoredImage,
    registryMonitoringConfig config.RegistryMonitoring,
    pods []corev1.Pod,
) ([]corev1.Secret, error) {
    // Try to find a pod that uses this image and has pull secrets.
    for i := range pods {
        pod := &pods[i]
        for imageName := range normalizedImageNamesFromPod(ctx, pod) {
            if imageName != fullRef {
                continue
            }
            if len(pod.Spec.ImagePullSecrets) == 0 {
                break
            }
            secrets, err := internal.GetPullSecretsFromPod(ctx, r.Client, pod)
            if err == nil {
                return secrets, nil
            }
            // Log but don't fail — try another pod or fall through to fallback.
            logf.FromContext(ctx).V(1).Info("could not read pod pull secrets", "pod", klog.KObj(pod), "error", err)
        }
    }

    // Fall back to the configured fallback secret.
    if registryMonitoringConfig.FallbackCredentialSecret == nil {
        return nil, nil // anonymous access
    }

    secret := &corev1.Secret{}
    key := client.ObjectKey{
        Namespace: registryMonitoringConfig.FallbackCredentialSecret.Namespace,
        Name:      registryMonitoringConfig.FallbackCredentialSecret.Name,
    }
    if err := r.Get(ctx, key, secret); err != nil {
        return nil, fmt.Errorf("fallback credential secret %s not found: %w", key, err)
    }
    return []corev1.Secret{*secret}, nil
}
```

### 3.8 RBAC markers

Add at the top of the controller file:

```go
// +kubebuilder:rbac:groups=kuik.enix.io,resources=clusterimagesetavailabilities,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kuik.enix.io,resources=clusterimagesetavailabilities/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
```

---

## Phase 4 — Register in main.go

@NOTE: the file `PROJECT` should be updated too, actually you can use `kubebuilder` CLI to generate required files and scaffolding before editing (eg. `kubebuilder create api --kind ClusterReplicatedImageSet --version v1alpha1 --group kuik --resource --controller=false --namespaced=false`).

In `cmd/main.go`, instantiate and register the new controller after the existing ones:

```go
if err = (&kuikcontroller.ClusterImageSetAvailabilityReconciler{
    Client: mgr.GetClient(),
    Scheme: mgr.GetScheme(),
    Config: configuration,
}).SetupWithManager(mgr); err != nil {
    setupLog.Error(err, "unable to create controller", "controller", "ClusterImageSetAvailability")
    os.Exit(1)
}
```

---

## Phase 5 — Generate & Manifests

After all code changes:

```bash
make generate    # regenerates DeepCopy methods for new types
make manifests   # regenerates CRD YAML and RBAC
make lint-fix    # fix any linting issues
make test        # run unit tests
```

The CRD YAML will be generated at:
`config/crd/bases/kuik.enix.io_clusterimagesetavailabilities.yaml`

---

## Key Design Decisions

### Drip-feed rate computation

`tickDuration = interval / maxPerInterval`

With `interval=5m, maxPerInterval=1`: one check every 5 minutes.
With `interval=10m, maxPerInterval=2`: one check every 5 minutes (two checks spread over 10 minutes).

The reconciler computes `tickDuration` and compares `time.Since(lastMonitor)` against it. If due, it checks and requeues after `tickDuration`. If not due, it requeues after the remaining time.

This means the total cycle time to check all N images for a registry is `N × tickDuration`.

### Why no separate monitoring goroutine

The `ctrl.Result{RequeueAfter: tickDuration}` pattern is sufficient and avoids the complexity of goroutine lifecycle management, especially across leader-election transitions. The reconciler is triggered by pod changes *and* by time, and the tick-gate ensures the monitoring rate is respected regardless of how often the reconciler runs.

### Image path representation

Status stores the **registry-relative path** (`library/nginx:1.25`), not the full normalized reference. The registry is implicit from the `registriesMonitoring` config. This matches the spec and makes the status more readable when all images are from the same registry.

@NOTE: be careful to not store twice the same image (eg. `nginx:1.25` and `library/nginx:1.25`). it is maybe easier to store full normalized reference.

### `imageFilter` matching is registry-scoped

`imageFilter` patterns are matched against the registry-relative path using `MustBuildWithRegistry(registry + "/")`. This means:
- Pattern `library/nginx:.+` matches `docker.io/library/nginx:1.25` for registry `docker.io`
- The same pattern is tested per configured registry

An image is monitored even if its registry doesn't appears in `registriesMonitoring`. Images from unconfigured registries are using default values.

Example:

```yaml
# /etc/kuik/config.yaml
registriesMonitoring:
  default:
    method: HEAD
    interval: 5m
    maxPerInterval: 1
    timeout: 5s
  docker.io:
    method: HEAD
    interval: 1h
    maxPerInterval: 3
    timeout: 5s
    fallbackCredentialSecret:
      name: registry-secret
      namespace: kuik-system
```

Default includes all fields from per registry configuration except `fallbackCredentialSecret`.

### `isRateLimited` refactor

The `isRateLimited` function currently lives in the webhook package. It should be moved to `internal/registry/ratelimit.go` as an exported function to avoid duplication:

```go
// internal/registry/ratelimit.go

// IsRateLimited returns true if the registry response headers indicate that
// the rate limit has been reached (ratelimit-remaining: 0;...).
func IsRateLimited(headers http.Header) bool {
    return strings.HasPrefix(headers.Get("ratelimit-remaining"), "0;")
}
```

The webhook's `isRateLimited` call is updated to `registry.IsRateLimited(headers)`.

### `UnavailableSecret` status

This is a new status value that doesn't exist in the webhook's `ImageAvailability` enum. It covers the case where the `fallbackCredentialSecret` is configured but the referenced Secret does not exist in the cluster. The check fails before even contacting the registry.

@NOTE: `ImageAvailability` enum should be probably removed and replaced by the `ImageAvailabilityStatus` enum.

### `unusedImageExpiry` with zero value

If `unusedImageExpiry` is not set (zero value), unused images are **never** removed from `status.images`. This is intentional: the user must opt into cleanup by setting the expiry. This mirrors ISM's `cleanup.enabled` requirement.

---

## File Summary

| File | Action |
|---|---|
| `internal/config/config.go` | Add `RegistriesMonitoring`, `RegistryMonitoring`, `CredentialSecretRef` |
| `api/kuik/v1alpha1/clusterimagesetavailability_types.go` | New file: CRD types |
| `api/kuik/v1alpha1/zz_generated.deepcopy.go` | Auto-generated by `make generate` |
| `config/crd/bases/kuik.enix.io_clusterimagesetavailabilities.yaml` | Auto-generated by `make manifests` |
| `internal/registry/ratelimit.go` | New file: move + export `isRateLimited` |
| `internal/webhook/core/v1/pod_webhook.go` | Update call to `registry.IsRateLimited` |
| `internal/controller/kuik/clusterimagesetavailability_controller.go` | New file: controller |
| `cmd/main.go` | Register new controller |

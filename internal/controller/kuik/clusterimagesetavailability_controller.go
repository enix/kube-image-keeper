package kuik

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal"
	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/enix/kube-image-keeper/internal/registry"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

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

type monitoringCandidate struct {
	image               *kuikv1alpha1.MonitoredImage
	cisa                *kuikv1alpha1.ClusterImageSetAvailability
	registryLastMonitor time.Time
}

func (r *ClusterImageSetAvailabilityReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kuikv1alpha1.ClusterImageSetAvailability{}).
		Named("kuik-clusterimagesetavailability").
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		WatchesRawSource(source.TypedKind(mgr.GetCache(), &corev1.Pod{},
			handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, pod *corev1.Pod) []reconcile.Request {
				log := logf.FromContext(ctx).WithName("pod-mapper").WithValues("pod", klog.KObj(pod))

				var cisaList kuikv1alpha1.ClusterImageSetAvailabilityList
				if err := r.List(ctx, &cisaList); err != nil {
					log.Error(err, "failed to list ClusterImageSetAvailability")
					return nil
				}

				imageNames := normalizedImageNamesFromPod(logf.IntoContext(ctx, log), pod)

				var reqs []reconcile.Request
				for _, cisa := range cisaList.Items {
					imageFilter := cisa.Spec.ImageFilter.MustBuild()
					for imageName := range imageNames {
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

func (r *ClusterImageSetAvailabilityReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cisa kuikv1alpha1.ClusterImageSetAvailability
	if err := r.Get(ctx, req.NamespacedName, &cisa); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var pods corev1.PodList
	if err := r.List(ctx, &pods); err != nil {
		return ctrl.Result{}, err
	}

	original := cisa.DeepCopy()
	r.syncImageList(ctx, &cisa, pods.Items)
	if err := r.Status().Patch(ctx, &cisa, client.MergeFrom(original)); err != nil {
		return ctrl.Result{}, err
	}

	candidates, err := r.getRegistriesCandidates(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	minRequeueAfter := time.Duration(math.MaxInt64)
	for registry, candidate := range candidates {
		registryConfig := r.registryConfig(registry)

		requeueAfter, err := r.checkNextForRegistry(ctx, candidate, registry, registryConfig, pods.Items)
		if err != nil {
			return ctrl.Result{}, err
		}
		if requeueAfter > 0 {
			minRequeueAfter = min(minRequeueAfter, requeueAfter)
		}
	}

	if minRequeueAfter == time.Duration(math.MaxInt64) {
		return ctrl.Result{}, nil
	}

	return ctrl.Result{RequeueAfter: minRequeueAfter}, nil
}

func (r *ClusterImageSetAvailabilityReconciler) getRegistriesCandidates(ctx context.Context) (map[string]*monitoringCandidate, error) {
	var cisas kuikv1alpha1.ClusterImageSetAvailabilityList
	if err := r.List(ctx, &cisas); err != nil {
		return nil, err
	}

	candidates := map[string]*monitoringCandidate{}
	for i := range cisas.Items {
		cisa := &cisas.Items[i]

		for j := range cisa.Status.Images {
			image := &cisa.Status.Images[j]

			registry, _, err := internal.RegistryAndPathFromReference(image.Path)
			if err != nil {
				continue
			}

			candidate, hasCandidate := candidates[registry]
			if !hasCandidate {
				candidate = &monitoringCandidate{}
				candidates[registry] = candidate
			}

			if image.LastMonitor == nil || !hasCandidate || image.LastMonitor.Before(candidate.image.LastMonitor) {
				candidate.image = image
				candidate.cisa = cisa
			}

			if image.LastMonitor != nil && image.LastMonitor.After(candidate.registryLastMonitor) {
				candidate.registryLastMonitor = image.LastMonitor.Time
			}
		}
	}

	return candidates, nil
}

func (r *ClusterImageSetAvailabilityReconciler) registryConfig(registry string) config.RegistryMonitoring {
	mon := r.Config.RegistriesMonitoring

	merged := mon.Default

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

func (r *ClusterImageSetAvailabilityReconciler) checkNextForRegistry(ctx context.Context, candidate *monitoringCandidate, registry string, registryConfig config.RegistryMonitoring, pods []corev1.Pod) (time.Duration, error) {
	log := logf.FromContext(ctx)
	tickDuration := registryConfig.Interval / time.Duration(registryConfig.MaxPerInterval)
	timeUntilDue := tickDuration - time.Since(candidate.registryLastMonitor)

	if timeUntilDue > 0 {
		return timeUntilDue, nil
	}

	original := candidate.cisa.DeepCopy()

	log.V(1).Info("checking image availability", "registry", registry, "path", candidate.image.Path)
	r.performCheck(ctx, candidate.image, registryConfig, pods)
	log.V(1).Info("image monitoring done", "status", candidate.image.Status)

	if err := r.Status().Patch(ctx, candidate.cisa, client.MergeFrom(original)); err != nil {
		return 0, err
	}

	return tickDuration, nil
}

func (r *ClusterImageSetAvailabilityReconciler) syncImageList(ctx context.Context, cisa *kuikv1alpha1.ClusterImageSetAvailability, pods []corev1.Pod) {
	log := logf.FromContext(ctx)
	now := metav1.NewTime(time.Now())
	instantExpiryMarker := metav1.NewTime(time.Time{}.Add(time.Hour))
	imageFilter := cisa.Spec.ImageFilter.MustBuild()

	currentImages := map[string]struct{}{}
	for i := range pods {
		for imageName := range normalizedImageNamesFromPod(ctx, &pods[i]) {
			if imageFilter.Match(imageName) {
				currentImages[imageName] = struct{}{}
			}
		}
	}

	for i := range cisa.Status.Images {
		image := &cisa.Status.Images[i]

		if !imageFilter.Match(image.Path) {
			if image.UnusedSince == nil || !image.UnusedSince.Equal(&instantExpiryMarker) {
				image.UnusedSince = &instantExpiryMarker
				log.Info("image no longer in scope, marking for removal", "path", image.Path)
			}
			continue
		}

		if _, inUse := currentImages[image.Path]; inUse {
			image.UnusedSince = nil
		} else if image.Status == kuikv1alpha1.ImageAvailabilityScheduled {
			image.UnusedSince = &instantExpiryMarker
			log.Info("image is no longer used by any pod and has never been monitored, marking it for removal", "path", image.Path)
		} else if image.UnusedSince == nil {
			image.UnusedSince = &now
		}
	}

	expiry := cisa.Spec.UnusedImageExpiry.Duration
	if expiry > 0 {
		cisa.Status.Images = slices.DeleteFunc(cisa.Status.Images, func(image kuikv1alpha1.MonitoredImage) bool {
			if image.UnusedSince != nil && time.Since(image.UnusedSince.Time) >= expiry {
				if image.UnusedSince.Compare(instantExpiryMarker.Time) != 0 {
					log.Info("image is unused for more than the retention duration, removing from monitoring", "path", image.Path)
				}
				return true
			}
			return false
		})
	}

	existingPaths := map[string]struct{}{}
	for _, image := range cisa.Status.Images {
		existingPaths[image.Path] = struct{}{}
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

func (r *ClusterImageSetAvailabilityReconciler) performCheck(ctx context.Context, image *kuikv1alpha1.MonitoredImage, registryConfig config.RegistryMonitoring, pods []corev1.Pod) {
	now := metav1.NewTime(time.Now())

	pullSecrets, err := r.resolveCredentials(ctx, image.Path, registryConfig, pods)
	if err != nil {
		image.Status = kuikv1alpha1.ImageAvailabilityUnavailableSecret
		image.LastError = err.Error()
	}

	result, checkErr := registry.CheckImageAvailability(ctx, image.Path, registryConfig.Method, registryConfig.Timeout, pullSecrets)
	image.LastMonitor = &now

	if image.Status == kuikv1alpha1.ImageAvailabilityUnavailableSecret && result == kuikv1alpha1.ImageAvailabilityInvalidAuth {
		return // In case of InvalidAuth with UnavailableSecret, UnavailableSecret takes precedence over InvalidAuth
	}

	image.Status = result
	if checkErr != nil {
		image.LastError = checkErr.Error()
	} else {
		image.LastError = ""
	}
}

func (r *ClusterImageSetAvailabilityReconciler) resolveCredentials(ctx context.Context, fullRef string, registryConfig config.RegistryMonitoring, pods []corev1.Pod) ([]corev1.Secret, error) {
	var errs []error

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
			logf.FromContext(ctx).V(1).Info("could not read pod pull secrets", "pod", klog.KObj(pod), "error", err)
			errs = append(errs, err)
		}
	}

	if registryConfig.FallbackCredentialSecret == nil {
		if len(errs) > 0 {
			return nil, fmt.Errorf("unavailable secret: %w", errors.Join(errs...))
		}
		return nil, nil
	}

	secret := &corev1.Secret{}
	key := client.ObjectKey{
		Namespace: registryConfig.FallbackCredentialSecret.Namespace,
		Name:      registryConfig.FallbackCredentialSecret.Name,
	}
	if err := r.Get(ctx, key, secret); err != nil {
		return nil, fmt.Errorf("fallback credential secret %s not found: %w", key, err)
	}

	return []corev1.Secret{*secret}, nil
}

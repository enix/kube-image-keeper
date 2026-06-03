package kuik

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"maps"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/distribution/reference"
	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal"
	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/enix/kube-image-keeper/internal/filter"
	"github.com/enix/kube-image-keeper/internal/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// MirrorObject is the common interface implemented by *ImageSetMirror and
// *ClusterImageSetMirror. It lets ImageSetMirrorBaseReconciler reconcile both
// kinds through one code path; the concrete reconcilers only supply the object
// type and the listing of their own kind (see Reconcile / SetupWithManager in
// imagesetmirror_controller.go and clusterimagesetmirror_controller.go).
//
// Scope differences are derived, not branched: the pod list scope, the mirror
// operation namespace, and the getAllMirrorPrefixes bucketing all key off
// obj.GetNamespace() (empty for cluster-scoped objects). Namespace filtering is
// folded into PodMatcher by the cluster-scoped accessor.
type MirrorObject interface {
	client.Object
	MirrorSpec() *kuikv1alpha1.ImageSetMirrorSpec
	MirrorStatus() *kuikv1alpha1.ImageSetMirrorStatus
	PodMatcher() (func(pod *corev1.Pod) bool, error)
	ImageFilter() (filter.Filter, error)
}

// ImageSetMirrorBaseReconciler provides a base for building ImageSetMirror and ClusterImageSetMirror reconciliers
type ImageSetMirrorBaseReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Config   *config.Config
	Recorder events.EventRecorder

	platforms       []v1.Platform
	globalPodFilter filter.PodFilter
}

// reconcile is the shared reconciliation loop for both mirror kinds. The
// concrete reconcilers call it with a freshly-allocated, empty object of their
// own kind; it is populated by the Get below.
//
// FIXME: split this into smaller steps to drop the gocyclo exemption.
//
//nolint:gocyclo
func (r *ImageSetMirrorBaseReconciler) reconcile(ctx context.Context, req ctrl.Request, obj MirrorObject) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	log.V(1).Info("reconciliation started")

	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Empty namespace (cluster-scoped objects) lists pods cluster-wide.
	namespace := obj.GetNamespace()

	var pods corev1.PodList
	if err := r.List(ctx, &pods, &client.ListOptions{Namespace: namespace}); err != nil {
		return ctrl.Result{}, err
	}

	podMatcher, err := obj.PodMatcher()
	if err != nil {
		log.Error(err, "invalid filter; skipping reconcile until spec is fixed")
		r.Recorder.Eventf(obj, nil, corev1.EventTypeWarning, "InvalidFilter", "ReconcileSkipped", "filter is invalid: %v", err)
		return ctrl.Result{}, nil
	}
	imageFilter, err := obj.ImageFilter()
	if err != nil {
		log.Error(err, "invalid filter; skipping reconcile until spec is fixed")
		r.Recorder.Eventf(obj, nil, corev1.EventTypeWarning, "InvalidFilter", "ReconcileSkipped", "filter is invalid: %v", err)
		return ctrl.Result{}, nil
	}
	pods.Items = slices.DeleteFunc(pods.Items, func(p corev1.Pod) bool {
		return !podMatcher(&p) || !r.globalPodFilter.Match(&p)
	})

	spec, status := obj.MirrorSpec(), obj.MirrorStatus()

	if !obj.GetDeletionTimestamp().IsZero() {
		if controllerutil.ContainsFinalizer(obj, imageSetMirrorFinalizer) {
			log.Info("deleting images from cache")

			for _, matchingImages := range status.MatchingImages {
				for _, mirror := range matchingImages.Mirrors {
					cleanupLog := log.WithValues("image", mirror.Image)
					if mirror.MirroredAt.IsZero() {
						cleanupLog.V(1).Info("image not mirrored yet, skipping deletion")
						continue
					}
					cleanupLog.V(1).Info("deleting image")
					if !r.cleanupMirror(logf.IntoContext(ctx, cleanupLog), mirror.Image, namespace, spec.Mirrors) {
						return ctrl.Result{}, errors.New("could not cleanup mirrors")
					}
				}
			}

			log.Info("removing finalizer")
			err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				if err := r.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
					return client.IgnoreNotFound(err)
				}
				controllerutil.RemoveFinalizer(obj, imageSetMirrorFinalizer)
				return r.Update(ctx, obj)
			})
			if err != nil {
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(obj, imageSetMirrorFinalizer) {
		log.Info("adding finalizer")
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := r.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
				return err
			}
			controllerutil.AddFinalizer(obj, imageSetMirrorFinalizer)
			return r.Update(ctx, obj)
		})
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	mirrorPrefixes, err := r.getAllMirrorPrefixes(ctx, namespace == "")
	if err != nil {
		return ctrl.Result{}, err
	}

	original := obj.DeepCopyObject().(client.Object)
	podsByMatchingImages, err := mergePreviousAndCurrentMatchingImages(logf.IntoContext(ctx, log), pods.Items, obj, mirrorPrefixes, imageFilter)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.Status().Patch(ctx, obj, client.MergeFrom(original)); err != nil {
		return ctrl.Result{}, err
	}

	someDeletionFailed := false
	requeueAfter := time.Duration(0)
	matchingImagesAfterCleanup := []kuikv1alpha1.MatchingImage{}
	for i := range status.MatchingImages {
		matchingImage := &status.MatchingImages[i]

		if matchingImage.UnusedSince == nil {
			matchingImagesAfterCleanup = append(matchingImagesAfterCleanup, *matchingImage)
			continue
		}

		mirrorsAfterCleanup := []kuikv1alpha1.MirrorStatus{}
		for j := range matchingImage.Mirrors {
			mirror := &matchingImage.Mirrors[j]

			cleanupEnabled := spec.Cleanup.Enabled
			retentionDuration := spec.Cleanup.Retention.Duration // TODO: merge retention options
			deleteAfter := retentionDuration - time.Since(matchingImage.UnusedSince.Time)
			if !cleanupEnabled {
				mirrorsAfterCleanup = append(mirrorsAfterCleanup, *mirror)
				continue
			} else if deleteAfter > 0 {
				if requeueAfter == 0 || deleteAfter < requeueAfter {
					requeueAfter = deleteAfter
				}
				mirrorsAfterCleanup = append(mirrorsAfterCleanup, *mirror)
				continue
			}

			cleanupLog := log.WithValues("image", mirror.Image)
			cleanupLog.Info("image is unused for more than the retention duration, deleting it", "retentionDuration", retentionDuration)
			if mirror.MirroredAt != nil {
				if !r.cleanupMirror(logf.IntoContext(ctx, cleanupLog), mirror.Image, namespace, spec.Mirrors) {
					mirrorsAfterCleanup = append(mirrorsAfterCleanup, *mirror)
					someDeletionFailed = true
				}
			}
		}

		if len(mirrorsAfterCleanup) > 0 {
			matchingImage.Mirrors = mirrorsAfterCleanup
			matchingImagesAfterCleanup = append(matchingImagesAfterCleanup, *matchingImage)
		}
	}

	original = obj.DeepCopyObject().(client.Object)
	status.MatchingImages = matchingImagesAfterCleanup
	if err := r.Status().Patch(ctx, obj, client.MergeFrom(original)); err != nil {
		return ctrl.Result{}, err
	}

	someMirrorFailed := false
	for i := range status.MatchingImages {
		matchingImage := &status.MatchingImages[i]
		original = obj.DeepCopyObject().(client.Object)

		if matchingImage.UnusedSince != nil {
			continue
		}

		for j := range matchingImage.Mirrors {
			mirror := &matchingImage.Mirrors[j]

			if mirror.MirroredAt == nil {
				mirrorLog := log.WithValues("from", matchingImage.Image, "to", mirror.Image)
				mirrorLog.Info("mirroring image")

				err := r.mirrorImage(ctx, namespace, spec.Mirrors, podsByMatchingImages, matchingImage.Image, mirror)
				if err != nil {
					mirrorLog.Error(err, "could not mirror image")
					someMirrorFailed = true
					mirror.LastError = err.Error()
				} else {
					mirrorLog.Info("successfully mirrored image")
					mirror.LastError = ""
				}
			}
		}

		if err := r.Status().Patch(ctx, obj, client.MergeFrom(original)); err != nil {
			return ctrl.Result{}, err
		}
	}

	if someDeletionFailed {
		return ctrl.Result{}, errors.New("one or more image(s) could not be deleted")
	}

	if someMirrorFailed {
		return ctrl.Result{}, errors.New("one or more image(s) could not be mirrored")
	}

	if requeueAfter > 0 {
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	return ctrl.Result{}, nil
}

// setupController wires the shared controller plumbing (rate limiter, generation
// predicate, pod watch). The concrete reconciler supplies its kind name, an empty
// object for the For() type, the pod mapper, and itself as the Reconciler.
func (r *ImageSetMirrorBaseReconciler) setupController(mgr ctrl.Manager, name string, obj client.Object, mapPod handler.TypedMapFunc[*corev1.Pod, reconcile.Request], rec reconcile.Reconciler) error {
	r.setupPlatforms()
	if err := r.setupGlobalPodFilter(); err != nil {
		return err
	}
	if r.Recorder == nil {
		r.Recorder = mgr.GetEventRecorder(name)
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(obj).
		Named(name).
		WithOptions(controller.Options{
			RateLimiter: newMirroringRateLimiter(),
		}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		WatchesRawSource(source.TypedKind(mgr.GetCache(), &corev1.Pod{}, handler.TypedEnqueueRequestsFromMapFunc(mapPod))).
		Complete(rec)
}

// enqueueForPod is the shared pod-mapper body. The concrete reconciler supplies a
// listObjects closure that lists its own kind (scoped to the pod's namespace for
// the namespaced kind, cluster-wide for the cluster kind).
func (r *ImageSetMirrorBaseReconciler) enqueueForPod(ctx context.Context, pod *corev1.Pod, listObjects func(ctx context.Context) ([]MirrorObject, error)) []reconcile.Request {
	log := logf.FromContext(ctx).
		WithName("pod-mapper").
		WithValues("pod", klog.KObj(pod))

	if !r.globalPodFilter.Match(pod) {
		return nil
	}

	objs, err := listObjects(ctx)
	if err != nil {
		log.Error(err, "failed to list mirror resources")
		return nil
	}

	imageNames := normalizedImageNamesFromPod(pod)

	reqs := []reconcile.Request{}
	for _, obj := range objs {
		podMatcher, err := obj.PodMatcher()
		if err != nil {
			log.Error(err, "skipping mirror resource with invalid filter", "namespace", obj.GetNamespace(), "name", obj.GetName())
			continue
		}
		if !podMatcher(pod) {
			continue
		}
		imageFilter, err := obj.ImageFilter()
		if err != nil {
			log.Error(err, "skipping mirror resource with invalid image filter", "namespace", obj.GetNamespace(), "name", obj.GetName())
			continue
		}
		for imageName := range imageNames {
			if strings.Contains(imageName, "@") {
				continue // ignore digest-based images
			}

			if imageFilter.Match(imageName) {
				reqs = append(reqs, reconcile.Request{
					NamespacedName: client.ObjectKeyFromObject(obj),
				})
				break
			}
		}
	}

	return reqs
}

// compileGlobalPodFilter compiles a pod filter from cfg.SkipLabels and cfg.SkipAnnotations.
// It returns the compiled filter, or an error if compilation fails.
func compileGlobalPodFilter(cfg *config.Config) (filter.PodFilter, error) {
	f, err := filter.CompilePodFilter(nil, cfg.SkipLabels, nil, cfg.SkipAnnotations)
	if err != nil {
		return filter.PodFilter{}, err
	}
	return *f, nil
}

func (r *ImageSetMirrorBaseReconciler) setupGlobalPodFilter() error {
	f, err := compileGlobalPodFilter(r.Config)
	if err != nil {
		return err
	}
	r.globalPodFilter = f
	return nil
}

func (r *ImageSetMirrorBaseReconciler) setupPlatforms() {
	r.platforms = make([]v1.Platform, len(r.Config.Mirroring.Platforms))
	for i, p := range r.Config.Mirroring.Platforms {
		r.platforms[i] = v1.Platform{
			OS:           p.OS,
			Architecture: p.Architecture,
			Variant:      p.Variant,
		}
	}
}

func (r *ImageSetMirrorBaseReconciler) getPullSecret(ctx context.Context, namespace, name string, secret *corev1.Secret) error {
	secretReference := client.ObjectKey{Namespace: namespace, Name: name}
	if err := r.Get(ctx, secretReference, secret); err != nil {
		return err
	}
	return nil
}

func (r *ImageSetMirrorBaseReconciler) getPullSecretsFromPods(ctx context.Context, podsByMatchingImages map[string]*corev1.Pod, image string) ([]corev1.Secret, error) {
	var secrets []corev1.Secret

	if pod, ok := podsByMatchingImages[image]; ok {
		secrets = make([]corev1.Secret, len(pod.Spec.ImagePullSecrets))

		for i, imagePullSecret := range pod.Spec.ImagePullSecrets {
			if err := r.getPullSecret(ctx, pod.Namespace, imagePullSecret.Name, &secrets[i]); err != nil {
				return nil, err
			}
		}
	}

	return secrets, nil
}

func (r *ImageSetMirrorBaseReconciler) getImageSecretFromMirrors(ctx context.Context, image, namespace string, mirrors kuikv1alpha1.Mirrors) (*corev1.Secret, error) {
	destCredentialSecret := mirrors.GetCredentialSecretForImage(image)

	if destCredentialSecret == nil {
		return nil, nil
	}

	// This allows to use the same code for both ClusterImageSetMirror and ImageSetMirror
	if namespace == "" {
		namespace = destCredentialSecret.Namespace
	}

	secret := &corev1.Secret{}
	if err := r.getPullSecret(ctx, namespace, destCredentialSecret.Name, secret); err != nil {
		return nil, err
	}

	return secret, nil
}

func (r *ImageSetMirrorBaseReconciler) mirrorImage(ctx context.Context, namespace string, mirrors kuikv1alpha1.Mirrors, podsByMatchingImages map[string]*corev1.Pod, from string, to *kuikv1alpha1.MirrorStatus) (err error) {
	srcSecrets, err := r.getPullSecretsFromPods(ctx, podsByMatchingImages, from)
	if err != nil {
		return err
	}

	destSecrets := make([]corev1.Secret, 1)
	if secret, err := r.getImageSecretFromMirrors(ctx, to.Image, namespace, mirrors); err != nil {
		return err
	} else if secret != nil {
		destSecrets[0] = *secret
	}

	defer func() {
		if err != nil {
			client := registry.NewClient(nil, nil).WithPullSecrets(destSecrets)
			_, destErr := client.GetDescriptor(ctx, to.Image)
			if destErr == nil {
				logf.FromContext(ctx).V(1).Info("could not mirror image, but the image seems to be already mirrored")
				err = nil
				now := metav1.NewTime(time.Now())
				to.MirroredAt = &now
			}
		}
	}()

	client := registry.NewClient(nil, nil).WithPullSecrets(srcSecrets)
	srcDesc, err := client.GetDescriptor(ctx, from)
	if err != nil {
		return err
	}

	// FIXME: if a platform is added or removed, already mirrored images are not updated consequently
	if err := client.WithTimeout(0).WithPullSecrets(destSecrets).CopyImage(ctx, srcDesc, to.Image, r.platforms); err != nil {
		return err
	}

	now := metav1.NewTime(time.Now())
	to.MirroredAt = &now

	return nil
}

func (r *ImageSetMirrorBaseReconciler) cleanupMirror(ctx context.Context, image, namespace string, mirrors kuikv1alpha1.Mirrors) (success bool) {
	log := logf.FromContext(ctx)

	secret, err := r.getImageSecretFromMirrors(ctx, image, namespace, mirrors)
	if err != nil {
		log.Error(err, "could not read secret for image deletion")
		return false
	} else if secret == nil {
		log.V(1).Info("no secret is configured for deleting image, ignoring")
		return true
	}

	if err := registry.NewClient(nil, nil).WithPullSecrets([]corev1.Secret{*secret}).DeleteImage(ctx, image); err != nil {
		log.Error(err, "could not delete image")
		return false
	}

	return true
}

func mergePreviousAndCurrentMatchingImages(ctx context.Context, pods []corev1.Pod, obj MirrorObject, mirrorPrefixes map[string][]string, imageFilter filter.Filter) (map[string]*corev1.Pod, error) {
	spec, status := obj.MirrorSpec(), obj.MirrorStatus()
	podsByMatchingImages := podsByNormalizedMatchingImages(ctx, imageFilter, mirrorPrefixes, pods)

	matchingImagesMap := map[string]kuikv1alpha1.MatchingImage{}
	for matchingImage := range podsByMatchingImages {
		mirrors := []kuikv1alpha1.MirrorStatus{}
		for _, mirror := range spec.Mirrors {
			matchingImageWithoutRegistry := strings.SplitN(matchingImage, "/", 2)[1]
			mirrors = append(mirrors, kuikv1alpha1.MirrorStatus{
				Image: path.Join(mirror.Registry, mirror.Path, matchingImageWithoutRegistry),
			})
		}
		matchingImagesMap[matchingImage] = kuikv1alpha1.MatchingImage{
			Image:   matchingImage,
			Mirrors: mirrors,
		}
	}

	inUseImages := podsInUseImages(ctx, pods)
	if err := updateUnusedSince(ctx, matchingImagesMap, inUseImages, status, imageFilter); err != nil {
		return nil, err
	}

	status.MatchingImages = make([]kuikv1alpha1.MatchingImage, 0, len(matchingImagesMap))
	for _, img := range matchingImagesMap {
		status.MatchingImages = append(status.MatchingImages, img)
	}

	return podsByMatchingImages, nil
}

func podsByNormalizedMatchingImages(ctx context.Context, filter filter.Filter, mirrorPrefixes map[string][]string, pods []corev1.Pod) map[string]*corev1.Pod {
	log := logf.FromContext(ctx)

	filteredOutImagesMap := map[string]struct{}{}
	matchingImagesMap := map[string]*corev1.Pod{}
	for i := range pods {
		pod := &pods[i]
		for image := range normalizedImageNamesFromPod(pod) {
			relevantMirrorPrefixes := append(mirrorPrefixes[""], mirrorPrefixes[pod.Namespace]...)
			if slices.ContainsFunc(relevantMirrorPrefixes, func(mirrorPrefix string) bool {
				return strings.HasPrefix(image, mirrorPrefix)
			}) {
				filteredOutImagesMap[image] = struct{}{}
				continue
			}

			if filter.Match(image) {
				matchingImagesMap[image] = pod
			}
		}
	}

	if len(filteredOutImagesMap) > 0 {
		filteredOutImages := slices.Collect(maps.Keys(filteredOutImagesMap))
		log.V(1).Info("filtering out images to prevent mirror loop", "images", filteredOutImages, "count", len(filteredOutImagesMap))
	}

	return matchingImagesMap
}

func normalizedImageNamesMapFromAnnotatedPod(ctx context.Context, pod *corev1.Pod) map[string]bool {
	log := logf.FromContext(ctx)

	originalImages := map[string]string{}
	if originalImagesStr, ok := pod.Annotations[OriginalImagesAnnotation]; ok {
		if err := json.Unmarshal([]byte(originalImagesStr), &originalImages); err != nil {
			log.Error(err, "could not unmarshal "+OriginalImagesAnnotation+" annotation")
		}
	}

	imageNames := map[string]bool{}
	for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
		if strings.Contains(container.Image, "@") {
			continue // ignore digest-based images
		}
		if named, err := reference.ParseNormalizedNamed(container.Image); err == nil {
			imageNames[named.String()] = false
		}
	}
	for _, image := range originalImages {
		if strings.Contains(image, "@") {
			continue // ignore digest-based images
		}
		if named, err := reference.ParseNormalizedNamed(image); err == nil {
			imageNames[named.String()] = true
		}
	}

	return imageNames
}

func normalizedImageNamesFromAnnotatedPod(ctx context.Context, pod *corev1.Pod) iter.Seq[string] {
	return maps.Keys(normalizedImageNamesMapFromAnnotatedPod(ctx, pod))
}

func normalizedImageNamesFromPod(pod *corev1.Pod) iter.Seq[string] {
	imageNames := map[string]struct{}{}
	for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
		if strings.Contains(container.Image, "@") {
			continue // ignore digest-based images
		}
		if named, err := reference.ParseNormalizedNamed(container.Image); err == nil {
			imageNames[named.String()] = struct{}{}
		}
	}

	return maps.Keys(imageNames)
}

// podsInUseImages returns the set of normalized image names that are still
// referenced by the cluster, considering BOTH the current container image and
// the original image stashed in the kuik.enix.io/original-images annotation.
// This is the signal used to decide whether a previously-mirrored image is
// still in use: a pod that the webhook has rewritten to pull from the mirror
// still logically requires its original reference.
func podsInUseImages(ctx context.Context, pods []corev1.Pod) map[string]struct{} {
	inUse := map[string]struct{}{}
	for i := range pods {
		for image := range normalizedImageNamesFromAnnotatedPod(ctx, &pods[i]) {
			inUse[image] = struct{}{}
		}
	}
	return inUse
}

func updateUnusedSince(ctx context.Context, matchingImagesMap map[string]kuikv1alpha1.MatchingImage, inUseImages map[string]struct{}, ismStatus *kuikv1alpha1.ImageSetMirrorStatus, imageFilter filter.Filter) error {
	log := logf.FromContext(ctx)
	unusedSinceNotMatching := metav1.Time{Time: (time.Time{}).Add(time.Hour)}

	for i := range ismStatus.MatchingImages {
		img := &ismStatus.MatchingImages[i]

		named, match, err := internal.NormalizeAndMatch(imageFilter, img.Image)
		if err != nil {
			return err
		} else if !match {
			// The image isn't matching anymore, which is different from matching but stopped to be used in the cluster.
			// Thus, we set UnusedSince to 0001-01-01 01:00:00 +0000 UTC to trigger instant expiry and deletion.
			// We add 1 hour to the zero value to prevent the patch to be ignored (zero value is considered == to nil)
			if !img.UnusedSince.Equal(&unusedSinceNotMatching) {
				img.UnusedSince = &unusedSinceNotMatching
				log.Info("image is not matching anymore, queuing it for deletion", "image", img.Image)
			}
		} else if _, ok := inUseImages[named.String()]; !ok {
			if img.UnusedSince.IsZero() {
				img.UnusedSince = &metav1.Time{Time: time.Now()}
				log.Info("image is not used anymore, marking it as unused", "image", img.Image)
			}
		} else {
			img.UnusedSince = nil
		}

		img.Mirrors = mergeMirrors(img.Mirrors, matchingImagesMap[named.String()].Mirrors)
		matchingImagesMap[named.String()] = *img
	}

	return nil
}

func mergeMirrors(currentMirrors, expectedMirrors []kuikv1alpha1.MirrorStatus) []kuikv1alpha1.MirrorStatus {
	// FIXME: remove and cleanup images that are present in current but not in expected
	currentImages := map[string]struct{}{}
	for _, mirror := range currentMirrors {
		currentImages[mirror.Image] = struct{}{}
	}

	for _, mirror := range expectedMirrors {
		if _, ok := currentImages[mirror.Image]; !ok {
			currentMirrors = append(currentMirrors, mirror)
		}
	}

	return currentMirrors
}

func newMirroringRateLimiter() workqueue.TypedRateLimiter[reconcile.Request] {
	// based on workqueue.DefaultTypedControllerRateLimiter
	return workqueue.NewTypedMaxOfRateLimiter(
		workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Second, 1000*time.Second),
		&workqueue.TypedBucketRateLimiter[reconcile.Request]{Limiter: rate.NewLimiter(rate.Limit(10), 100)},
	)
}

func (r *ImageSetMirrorBaseReconciler) getAllMirrorPrefixes(ctx context.Context, ignoreNamespaces bool) (map[string][]string, error) {
	var cismList kuikv1alpha1.ClusterImageSetMirrorList
	if err := r.List(ctx, &cismList); err != nil {
		return nil, err
	}
	var ismList kuikv1alpha1.ImageSetMirrorList
	if err := r.List(ctx, &ismList); err != nil {
		return nil, err
	}

	clusterwideMirrorPrefixes := make([]string, 0, len(cismList.Items)) // preallocate at least 1 mirror slot per CISM
	for _, cism := range cismList.Items {
		for _, mirror := range cism.Spec.Mirrors {
			clusterwideMirrorPrefixes = append(clusterwideMirrorPrefixes, mirror.Prefix())
		}
	}

	mirrorPrefixes := map[string][]string{
		"": clusterwideMirrorPrefixes,
	}

	for _, ism := range ismList.Items {
		if ignoreNamespaces {
			ism.Namespace = ""
		}
		for _, mirror := range ism.Spec.Mirrors {
			mirrorPrefixes[ism.Namespace] = append(mirrorPrefixes[ism.Namespace], mirror.Prefix())
		}
	}

	return mirrorPrefixes, nil
}

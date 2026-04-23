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
	"github.com/enix/kube-image-keeper/internal/filter"
	"github.com/enix/kube-image-keeper/internal/registry"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ImageSetMirrorBaseReconciler provides a base for building ImageSetMirror and ClusterImageSetMirror reconciliers
type ImageSetMirrorBaseReconciler struct {
	client.Client
	Scheme *runtime.Scheme
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
			_, destErr := client.GetDescriptor(to.Image)
			if destErr == nil {
				logf.FromContext(ctx).V(1).Info("could not mirror image, but the image seems to be already mirrored")
				err = nil
				now := metav1.NewTime(time.Now())
				to.MirroredAt = &now
			}
		}
	}()

	client := registry.NewClient(nil, nil).WithPullSecrets(srcSecrets)
	srcDesc, err := client.GetDescriptor(from)
	if err != nil {
		return err
	}

	if err := client.WithTimeout(0).WithPullSecrets(destSecrets).CopyImage(srcDesc, to.Image, []string{"amd64"}); err != nil {
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

	if err := registry.NewClient(nil, nil).WithPullSecrets([]corev1.Secret{*secret}).DeleteImage(image); err != nil {
		log.Error(err, "could not delete image")
		return false
	}

	return true
}

func mergePreviousAndCurrentMatchingImages(ctx context.Context, pods []corev1.Pod, ismSpec *kuikv1alpha1.ImageSetMirrorSpec, ismStatus *kuikv1alpha1.ImageSetMirrorStatus, mirrorPrefixes map[string][]string) (map[string]*corev1.Pod, error) {
	imageFilter := ismSpec.ImageFilter.MustBuild()
	podsByMatchingImages := podsByNormalizedMatchingImages(ctx, imageFilter, mirrorPrefixes, pods)

	matchingImagesMap := map[string]kuikv1alpha1.MatchingImage{}
	for matchingImage := range podsByMatchingImages {
		mirrors := []kuikv1alpha1.MirrorStatus{}
		for _, mirror := range ismSpec.Mirrors {
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

	if err := updateUnusedSince(ctx, matchingImagesMap, ismStatus, imageFilter); err != nil {
		return nil, err
	}

	ismStatus.MatchingImages = make([]kuikv1alpha1.MatchingImage, 0, len(matchingImagesMap))
	for _, img := range matchingImagesMap {
		ismStatus.MatchingImages = append(ismStatus.MatchingImages, img)
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

func updateUnusedSince(ctx context.Context, matchingImagesMap map[string]kuikv1alpha1.MatchingImage, ismStatus *kuikv1alpha1.ImageSetMirrorStatus, imageFilter filter.Filter) error {
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
		} else if _, ok := matchingImagesMap[named.String()]; !ok {
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

// reconcileImageSetMirror runs the shared reconcile flow for ImageSetMirror and
// ClusterImageSetMirror. It operates on the untyped object for metadata/patch
// operations and on the (aliased) Spec/Status pointers for domain logic.
func (r *ImageSetMirrorBaseReconciler) reconcileImageSetMirror(
	ctx context.Context,
	obj client.Object,
	spec *kuikv1alpha1.ImageSetMirrorSpec,
	status *kuikv1alpha1.ImageSetMirrorStatus,
	ignoreNamespacesForPrefixes bool,
	pods []corev1.Pod,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.V(1).Info("reconciliation started")

	if !obj.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, r.handleDeletion(ctx, obj, spec, status)
	}

	if err := r.ensureFinalizer(ctx, obj); err != nil {
		return ctrl.Result{}, err
	}

	podsByMatchingImages, err := r.mergeMatchingImages(ctx, obj, spec, status, pods, ignoreNamespacesForPrefixes)
	if err != nil {
		return ctrl.Result{}, err
	}

	requeueAfter, someDeletionFailed, err := r.cleanupExpiredMirrors(ctx, obj, spec, status)
	if err != nil {
		return ctrl.Result{}, err
	}

	someMirrorFailed, err := r.mirrorPendingImages(ctx, obj, spec, status, podsByMatchingImages)
	if err != nil {
		return ctrl.Result{}, err
	}

	return buildReconcileResult(requeueAfter, someDeletionFailed, someMirrorFailed)
}

func (r *ImageSetMirrorBaseReconciler) handleDeletion(
	ctx context.Context,
	obj client.Object,
	spec *kuikv1alpha1.ImageSetMirrorSpec,
	status *kuikv1alpha1.ImageSetMirrorStatus,
) error {
	if !controllerutil.ContainsFinalizer(obj, imageSetMirrorFinalizer) {
		return nil
	}

	log := logf.FromContext(ctx)
	log.Info("deleting images from cache")

	for _, matchingImages := range status.MatchingImages {
		for _, mirror := range matchingImages.Mirrors {
			cleanupLog := log.WithValues("image", mirror.Image)
			if mirror.MirroredAt.IsZero() {
				cleanupLog.V(1).Info("image not mirrored yet, skipping deletion")
				continue
			}
			cleanupLog.V(1).Info("deleting image")
			if !r.cleanupMirror(logf.IntoContext(ctx, cleanupLog), mirror.Image, obj.GetNamespace(), spec.Mirrors) {
				return errors.New("could not cleanup mirrors")
			}
		}
	}

	log.Info("removing finalizer")
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := r.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			return client.IgnoreNotFound(err)
		}
		controllerutil.RemoveFinalizer(obj, imageSetMirrorFinalizer)
		return r.Update(ctx, obj)
	})
}

func (r *ImageSetMirrorBaseReconciler) ensureFinalizer(ctx context.Context, obj client.Object) error {
	if controllerutil.ContainsFinalizer(obj, imageSetMirrorFinalizer) {
		return nil
	}
	logf.FromContext(ctx).Info("adding finalizer")
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := r.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			return err
		}
		controllerutil.AddFinalizer(obj, imageSetMirrorFinalizer)
		return r.Update(ctx, obj)
	})
}

func (r *ImageSetMirrorBaseReconciler) mergeMatchingImages(
	ctx context.Context,
	obj client.Object,
	spec *kuikv1alpha1.ImageSetMirrorSpec,
	status *kuikv1alpha1.ImageSetMirrorStatus,
	pods []corev1.Pod,
	ignoreNamespaces bool,
) (map[string]*corev1.Pod, error) {
	mirrorPrefixes, err := r.getAllMirrorPrefixes(ctx, ignoreNamespaces)
	if err != nil {
		return nil, err
	}

	original := obj.DeepCopyObject().(client.Object)
	podsByMatchingImages, err := mergePreviousAndCurrentMatchingImages(ctx, pods, spec, status, mirrorPrefixes)
	if err != nil {
		return nil, err
	}

	if err := r.Status().Patch(ctx, obj, client.MergeFrom(original)); err != nil {
		return nil, err
	}

	return podsByMatchingImages, nil
}

func (r *ImageSetMirrorBaseReconciler) cleanupExpiredMirrors(
	ctx context.Context,
	obj client.Object,
	spec *kuikv1alpha1.ImageSetMirrorSpec,
	status *kuikv1alpha1.ImageSetMirrorStatus,
) (time.Duration, bool, error) {
	someDeletionFailed := false
	requeueAfter := time.Duration(0)
	matchingImagesAfterCleanup := []kuikv1alpha1.MatchingImage{}

	for i := range status.MatchingImages {
		matchingImage := &status.MatchingImages[i]

		if matchingImage.UnusedSince == nil {
			matchingImagesAfterCleanup = append(matchingImagesAfterCleanup, *matchingImage)
			continue
		}

		mirrorsAfterCleanup, deletionFailed, nextRequeue := r.cleanupUnusedMatchingImage(ctx, obj, spec, matchingImage)
		if deletionFailed {
			someDeletionFailed = true
		}
		if nextRequeue > 0 && (requeueAfter == 0 || nextRequeue < requeueAfter) {
			requeueAfter = nextRequeue
		}

		if len(mirrorsAfterCleanup) > 0 {
			matchingImage.Mirrors = mirrorsAfterCleanup
			matchingImagesAfterCleanup = append(matchingImagesAfterCleanup, *matchingImage)
		}
	}

	original := obj.DeepCopyObject().(client.Object)
	status.MatchingImages = matchingImagesAfterCleanup
	if err := r.Status().Patch(ctx, obj, client.MergeFrom(original)); err != nil {
		return 0, false, err
	}

	return requeueAfter, someDeletionFailed, nil
}

func (r *ImageSetMirrorBaseReconciler) cleanupUnusedMatchingImage(
	ctx context.Context,
	obj client.Object,
	spec *kuikv1alpha1.ImageSetMirrorSpec,
	matchingImage *kuikv1alpha1.MatchingImage,
) ([]kuikv1alpha1.MirrorStatus, bool, time.Duration) {
	log := logf.FromContext(ctx)
	mirrorsAfterCleanup := []kuikv1alpha1.MirrorStatus{}
	deletionFailed := false
	requeueAfter := time.Duration(0)

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
			if !r.cleanupMirror(logf.IntoContext(ctx, cleanupLog), mirror.Image, obj.GetNamespace(), spec.Mirrors) {
				mirrorsAfterCleanup = append(mirrorsAfterCleanup, *mirror)
				deletionFailed = true
			}
		}
	}

	return mirrorsAfterCleanup, deletionFailed, requeueAfter
}

func (r *ImageSetMirrorBaseReconciler) mirrorPendingImages(
	ctx context.Context,
	obj client.Object,
	spec *kuikv1alpha1.ImageSetMirrorSpec,
	status *kuikv1alpha1.ImageSetMirrorStatus,
	podsByMatchingImages map[string]*corev1.Pod,
) (bool, error) {
	log := logf.FromContext(ctx)
	someMirrorFailed := false

	for i := range status.MatchingImages {
		matchingImage := &status.MatchingImages[i]
		original := obj.DeepCopyObject().(client.Object)

		if matchingImage.UnusedSince != nil {
			continue
		}

		for j := range matchingImage.Mirrors {
			mirror := &matchingImage.Mirrors[j]

			if mirror.MirroredAt != nil {
				continue
			}

			mirrorLog := log.WithValues("from", matchingImage.Image, "to", mirror.Image)
			mirrorLog.Info("mirroring image")

			if err := r.mirrorImage(ctx, obj.GetNamespace(), spec.Mirrors, podsByMatchingImages, matchingImage.Image, mirror); err != nil {
				mirrorLog.Error(err, "could not mirror image")
				someMirrorFailed = true
				mirror.LastError = err.Error()
			} else {
				mirrorLog.Info("successfully mirrored image")
				mirror.LastError = ""
			}
		}

		if err := r.Status().Patch(ctx, obj, client.MergeFrom(original)); err != nil {
			return false, err
		}
	}

	return someMirrorFailed, nil
}

func buildReconcileResult(requeueAfter time.Duration, someDeletionFailed, someMirrorFailed bool) (ctrl.Result, error) {
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

// podImagesMatchFilter reports whether any of the pod's (non digest-based)
// normalized images is matched by the supplied filter. Used by pod-mapper
// watchers in ISM/CISM SetupWithManager.
func podImagesMatchFilter(imageNames iter.Seq[string], imageFilter filter.Filter) bool {
	for imageName := range imageNames {
		if strings.Contains(imageName, "@") {
			continue // ignore digest-based images
		}
		if imageFilter.Match(imageName) {
			return true
		}
	}
	return false
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

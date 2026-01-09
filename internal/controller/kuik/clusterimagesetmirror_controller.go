package kuik

import (
	"context"
	"errors"
	"strings"
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// ClusterImageSetMirrorReconciler reconciles a ClusterImageSetMirror object
type ClusterImageSetMirrorReconciler struct {
	ImageSetMirrorBaseReconciler
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ClusterImageSetMirror object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.4/pkg/reconcile
func (r *ClusterImageSetMirrorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var cism kuikv1alpha1.ClusterImageSetMirror
	if err := r.Get(ctx, req.NamespacedName, &cism); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var pods corev1.PodList
	if err := r.List(ctx, &pods, &client.ListOptions{Namespace: cism.Namespace}); err != nil {
		return ctrl.Result{}, err
	}

	if !cism.ObjectMeta.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&cism, imageSetMirrorFinalizerName) {
			log.Info("deleting images from cache")

			for _, matchingImages := range cism.Status.MatchingImages {
				for _, mirror := range matchingImages.Mirrors {
					cleanupLog := log.WithValues("image", mirror.Image)
					if mirror.MirroredAt.IsZero() {
						cleanupLog.V(1).Info("image not mirrored yet, skipping deletion")
						continue
					}
					cleanupLog.V(1).Info("deleting image")
					if !r.cleanupMirror(logf.IntoContext(ctx, cleanupLog), mirror.Image, cism.Namespace, cism.Spec.Mirrors) {
						return ctrl.Result{}, errors.New("could not cleanup mirrors")
					}
				}
			}

			log.Info("removing finalizer")
			err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				if err := r.Get(ctx, client.ObjectKeyFromObject(&cism), &cism); err != nil {
					return client.IgnoreNotFound(err)
				}
				controllerutil.RemoveFinalizer(&cism, imageSetMirrorFinalizerName)
				return r.Update(ctx, &cism)
			})
			if err != nil {
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&cism, imageSetMirrorFinalizerName) {
		log.Info("adding finalizer")
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := r.Get(ctx, client.ObjectKeyFromObject(&cism), &cism); err != nil {
				return err
			}

			controllerutil.AddFinalizer(&cism, imageSetMirrorFinalizerName)
			return r.Update(ctx, &cism)
		})
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	mirrorPrefixes, err := getAllOtherMirrorPrefixes(ctx, r.Client, cism.ObjectMeta, true)
	if err != nil {
		return ctrl.Result{}, err
	}

	spec, status := (*kuikv1alpha1.ImageSetMirrorSpec)(&cism.Spec), (*kuikv1alpha1.ImageSetMirrorStatus)(&cism.Status)
	podsByMatchingImages, matchingImagesMap, err := mergePreviousAndCurrentMatchingImages(logf.IntoContext(ctx, log), pods.Items, spec, status, mirrorPrefixes)
	if err != nil {
		return ctrl.Result{}, err
	}

	originalCism := cism.DeepCopy()
	cism.Status.MatchingImages = []kuikv1alpha1.MatchingImage{}
	for _, matchingImage := range matchingImagesMap {
		cism.Status.MatchingImages = append(cism.Status.MatchingImages, matchingImage)
	}

	if err := r.Status().Patch(ctx, &cism, client.MergeFrom(originalCism)); err != nil {
		return ctrl.Result{}, err
	}

	someDeletionFailed := false
	requeueAfter := time.Duration(0)
	matchingImagesAfterCleanup := []kuikv1alpha1.MatchingImage{}
	for i := range cism.Status.MatchingImages {
		matchingImage := &cism.Status.MatchingImages[i]

		if matchingImage.UnusedSince == nil {
			matchingImagesAfterCleanup = append(matchingImagesAfterCleanup, *matchingImage)
			continue
		}

		mirrorsAfterCleanup := []kuikv1alpha1.MirrorStatus{}
		for j := range matchingImage.Mirrors {
			mirror := &matchingImage.Mirrors[j]

			cleanupEnabled := cism.Spec.Cleanup.Enabled
			retentionDuration := cism.Spec.Cleanup.Retention.Duration // TODO: merge retention options
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
				if !r.cleanupMirror(logf.IntoContext(ctx, cleanupLog), mirror.Image, cism.Namespace, cism.Spec.Mirrors) {
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

	originalCism = cism.DeepCopy()
	cism.Status.MatchingImages = matchingImagesAfterCleanup
	if err := r.Status().Patch(ctx, &cism, client.MergeFrom(originalCism)); err != nil {
		return ctrl.Result{}, err
	}

	someMirrorFailed := false
	for i := range cism.Status.MatchingImages {
		matchingImage := &cism.Status.MatchingImages[i]
		originalCism = cism.DeepCopy()

		if matchingImage.UnusedSince != nil {
			continue
		}

		for j := range matchingImage.Mirrors {
			mirror := &matchingImage.Mirrors[j]

			if mirror.MirroredAt == nil {
				mirrorLog := log.WithValues("from", matchingImage.Image, "to", mirror.Image)
				mirrorLog.Info("mirroring image")

				err := r.mirrorImage(ctx, cism.Namespace, cism.Spec.Mirrors, podsByMatchingImages, matchingImage.Image, mirror)
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

		if err := r.Status().Patch(ctx, &cism, client.MergeFrom(originalCism)); err != nil {
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

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterImageSetMirrorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kuikv1alpha1.ClusterImageSetMirror{}).
		Named("kuik-clusterimagesetmirror").
		WithOptions(controller.Options{
			RateLimiter: newMirroringRateLimiter(),
		}).
		WatchesRawSource(source.TypedKind(mgr.GetCache(), &corev1.Pod{},
			handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, pod *corev1.Pod) []reconcile.Request {
				log := logf.FromContext(ctx).
					WithName("pod-mapper").
					WithValues("pod", klog.KObj(pod))

				var cisms kuikv1alpha1.ClusterImageSetMirrorList
				if err := r.List(ctx, &cisms); err != nil {
					log.Error(err, "failed to list ClusterImageSetMirror")
					return nil
				}

				reqs := []reconcile.Request{}
				for _, cism := range cisms.Items {
					imageFilter := cism.Spec.ImageFilter.MustBuild()
					for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
						if strings.Contains(container.Image, "@") {
							continue // ignore digest-based images
						}

						_, match, err := internal.NormalizeAndMatch(imageFilter, container.Image)
						if err != nil {
							log.Error(err, "failed to match an image", "image", container.Image)
							continue
						}

						if match {
							reqs = append(reqs, reconcile.Request{
								NamespacedName: client.ObjectKeyFromObject(&cism),
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

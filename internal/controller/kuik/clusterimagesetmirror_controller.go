package kuik

import (
	"context"
	"errors"
	"path"
	"strings"
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/matchers"
	"github.com/enix/kube-image-keeper/internal/registry"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// ClusterImageSetMirrorReconciler reconciles a ClusterImageSetMirror object
type ClusterImageSetMirrorReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=kuik.enix.io,resources=clusterimagesetmirrors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kuik.enix.io,resources=clusterimagesetmirrors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kuik.enix.io,resources=clusterimagesetmirrors/finalizers,verbs=update

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

	matcher := cism.Spec.ImageMatcher.MustBuild()

	podsByMatchingImages, err := matchers.PodsByNormalizedMatchingImages(matcher, pods.Items)
	if err != nil {
		return ctrl.Result{}, err
	}

	matchedImagesMap := map[string]kuikv1alpha1.MatchedImage{}
	for matchingImage := range podsByMatchingImages {
		mirrors := []kuikv1alpha1.MirrorStatus{}
		for _, mirror := range cism.Spec.Mirrors {
			matchingImageWithoutRegistry := strings.SplitN(matchingImage, "/", 2)[1]
			mirrors = append(mirrors, kuikv1alpha1.MirrorStatus{
				Image: path.Join(mirror.Registry, mirror.Path, matchingImageWithoutRegistry),
			})
		}
		matchedImagesMap[matchingImage] = kuikv1alpha1.MatchedImage{
			Image:   matchingImage,
			Mirrors: mirrors,
		}
	}

	unusedSinceNotMatched := metav1.Time{Time: (time.Time{}).Add(time.Hour)}
	for _, matchedImage := range cism.Status.MatchedImages {
		named, match, err := matchers.NormalizeAndMatch(matcher, matchedImage.Image)
		if err != nil {
			return ctrl.Result{}, err
		} else if !match {
			// The image isn't matched anymore, which is different from matching but stopped to be used in the cluster.
			// This, we set UnusedSince to 0001-01-01 01:00:00 +0000 UTC to trigger instant expiry and deletion.
			// We add 1 hour to the zero value to prevent the patch to be ignored (zero value is considered == to nil)
			if !matchedImage.UnusedSince.Equal(&unusedSinceNotMatched) {
				matchedImage.UnusedSince = &unusedSinceNotMatched
				log.Info("image is not matching anymore, queuing it for deletion", "image", matchedImage.Image)
			}
		} else if _, ok := matchedImagesMap[named.String()]; !ok {
			if matchedImage.UnusedSince.IsZero() {
				matchedImage.UnusedSince = &metav1.Time{Time: time.Now()}
				log.Info("image is not used anymore, marking it as unused", "image", matchedImage.Image)
			}
		} else {
			matchedImage.UnusedSince = nil
		}
		// FIXME: update mirrors recursively (add/remove)
		matchedImagesMap[named.String()] = matchedImage
	}

	originalCism := cism.DeepCopy()
	cism.Status.MatchedImages = []kuikv1alpha1.MatchedImage{}
	for _, matchedImage := range matchedImagesMap {
		cism.Status.MatchedImages = append(cism.Status.MatchedImages, matchedImage)
	}

	if err := r.Status().Patch(ctx, &cism, client.MergeFrom(originalCism)); err != nil {
		return ctrl.Result{}, err
	}

	someDeletionFailed := false
	matchedImagesAfterCleanup := []kuikv1alpha1.MatchedImage{}
	for i := range cism.Status.MatchedImages {
		matchedImage := &cism.Status.MatchedImages[i]

		if matchedImage.UnusedSince == nil {
			matchedImagesAfterCleanup = append(matchedImagesAfterCleanup, *matchedImage)
			continue
		}

		mirrorsAfterCleanup := []kuikv1alpha1.MirrorStatus{}
		for j := range matchedImage.Mirrors {
			mirror := &matchedImage.Mirrors[j]

			cleanupEnabled := cism.Spec.Cleanup.Enabled
			retentionDuration := cism.Spec.Cleanup.Retention.Duration
			if !cleanupEnabled || time.Since(matchedImage.UnusedSince.Time) < retentionDuration { // TODO: merge retention options
				mirrorsAfterCleanup = append(mirrorsAfterCleanup, *mirror)
				continue
			}

			cleanupLog := log.WithValues("image", matchedImage.Image)
			cleanupLog.Info("image is unused for more than the retention duration, deleting it", "retentionDuration", retentionDuration)
			if mirror.MirroredAt != nil {
				secret, err := getImageSecretFromMirrors(ctx, r.Client, mirror.Image, cism.Namespace, cism.Spec.Mirrors)
				if err != nil {
					cleanupLog.Error(err, "could not read secret for image deletion")
				} else if secret == nil {
					cleanupLog.V(1).Info("no secret is configured for deleting image, ignoring")
					continue
				}

				if err := registry.NewClient(nil, nil).WithPullSecrets([]corev1.Secret{*secret}).DeleteImage(mirror.Image); err != nil {
					cleanupLog.Error(err, "could not delete image")
					someDeletionFailed = true
					mirrorsAfterCleanup = append(mirrorsAfterCleanup, *mirror)
				}
			}
		}

		if len(mirrorsAfterCleanup) > 0 {
			matchedImage.Mirrors = mirrorsAfterCleanup
			matchedImagesAfterCleanup = append(matchedImagesAfterCleanup, *matchedImage)
		}
	}

	originalCism = cism.DeepCopy()
	cism.Status.MatchedImages = matchedImagesAfterCleanup
	if err := r.Status().Patch(ctx, &cism, client.MergeFrom(originalCism)); err != nil {
		return ctrl.Result{}, err
	}

	someMirrorFailed := false
	for i := range cism.Status.MatchedImages {
		matchedImage := &cism.Status.MatchedImages[i]
		originalCism = cism.DeepCopy()

		if matchedImage.UnusedSince != nil {
			continue
		}

		for j := range matchedImage.Mirrors {
			mirror := &matchedImage.Mirrors[j]

			if mirror.MirroredAt == nil {
				mirrorLog := log.WithValues("from", matchedImage.Image, "to", mirror.Image)
				mirrorLog.Info("mirroring image")

				err := r.MirrorImage(ctx, &cism, podsByMatchingImages, matchedImage.Image, mirror)
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

	// TODO: requeue after expiration when an image has been marked as UnusedSince

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterImageSetMirrorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kuikv1alpha1.ClusterImageSetMirror{}).
		Named("kuik-clusterimagesetmirror").
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
					for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
						_, match, err := matchers.NormalizeAndMatch(cism.Spec.ImageMatcher.MustBuild(), container.Image)
						if err != nil {
							log.Error(err, "failed to match an image", "image", container.Image)
							continue
						}

						if match {
							reqs = append(reqs, reconcile.Request{
								NamespacedName: types.NamespacedName{Namespace: cism.Namespace, Name: cism.Name},
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

func (r *ClusterImageSetMirrorReconciler) getPullSecretsFromPods(ctx context.Context, podsByMatchingImages map[string]*corev1.Pod, image string) ([]corev1.Secret, error) {
	var secrets []corev1.Secret

	if pod, ok := podsByMatchingImages[image]; ok {
		secrets = make([]corev1.Secret, len(pod.Spec.ImagePullSecrets))

		for i, imagePullSecret := range pod.Spec.ImagePullSecrets {
			if err := getPullSecret(ctx, r.Client, pod.Namespace, imagePullSecret.Name, &secrets[i]); err != nil {
				return nil, err
			}
		}
	}

	return secrets, nil
}

func (r *ClusterImageSetMirrorReconciler) MirrorImage(ctx context.Context, cism *kuikv1alpha1.ClusterImageSetMirror, podsByMatchingImages map[string]*corev1.Pod, from string, to *kuikv1alpha1.MirrorStatus) error {
	srcSecrets, err := r.getPullSecretsFromPods(ctx, podsByMatchingImages, from)
	if err != nil {
		return err
	}

	destSecrets := make([]corev1.Secret, 1)
	if secret, err := getImageSecretFromMirrors(ctx, r.Client, to.Image, cism.Namespace, cism.Spec.Mirrors); err != nil {
		return err
	} else if secret != nil {
		destSecrets[0] = *secret
	}

	registry := registry.NewClient(nil, nil).WithPullSecrets(srcSecrets)
	srcDesc, err := registry.GetDescriptor(from)
	if err != nil {
		return err
	}

	if err := registry.WithTimeout(0).WithPullSecrets(destSecrets).CopyImage(srcDesc, to.Image, []string{"amd64"}); err != nil {
		return err
	}

	now := metav1.NewTime(time.Now())
	to.MirroredAt = &now

	return nil
}

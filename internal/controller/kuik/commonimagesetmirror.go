package kuik

import (
	"context"
	"path"
	"strings"
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal"
	"github.com/enix/kube-image-keeper/internal/filter"
	"github.com/enix/kube-image-keeper/internal/registry"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const imageSetMirrorFinalizerName = "kuik.enix.io/mirror-cleanup"

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

func (r *ImageSetMirrorBaseReconciler) mirrorImage(ctx context.Context, namespace string, mirrors kuikv1alpha1.Mirrors, podsByMatchingImages map[string]*corev1.Pod, from string, to *kuikv1alpha1.MirrorStatus) error {
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

func mergePreviousAndCurrentMatchingImages(ctx context.Context, pods []corev1.Pod, ismSpec *kuikv1alpha1.ImageSetMirrorSpec, ismStatus *kuikv1alpha1.ImageSetMirrorStatus, mirrorPrefixes map[string][]string) (map[string]*corev1.Pod, map[string]kuikv1alpha1.MatchingImage, error) {
	imageFilter := ismSpec.ImageFilter.MustBuild()
	podsByMatchingImages, err := internal.PodsByNormalizedMatchingImages(ctx, imageFilter, mirrorPrefixes, pods)
	if err != nil {
		return nil, nil, err
	}

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

	if err := updateUnusedSince(ctx, matchingImagesMap, ismStatus.MatchingImages, imageFilter); err != nil {
		return nil, nil, err
	}

	return podsByMatchingImages, matchingImagesMap, nil
}

func updateUnusedSince(ctx context.Context, matchingImagesMap map[string]kuikv1alpha1.MatchingImage, matchingImages []kuikv1alpha1.MatchingImage, imageFilter filter.Filter) error {
	log := logf.FromContext(ctx)
	unusedSinceNotMatching := metav1.Time{Time: (time.Time{}).Add(time.Hour)}

	for _, matchingImage := range matchingImages {
		named, match, err := internal.NormalizeAndMatch(imageFilter, matchingImage.Image)
		if err != nil {
			return err
		} else if !match {
			// The image isn't matching anymore, which is different from matching but stopped to be used in the cluster.
			// This, we set UnusedSince to 0001-01-01 01:00:00 +0000 UTC to trigger instant expiry and deletion.
			// We add 1 hour to the zero value to prevent the patch to be ignored (zero value is considered == to nil)
			if !matchingImage.UnusedSince.Equal(&unusedSinceNotMatching) {
				matchingImage.UnusedSince = &unusedSinceNotMatching
				log.Info("image is not matching anymore, queuing it for deletion", "image", matchingImage.Image)
			}
		} else if _, ok := matchingImagesMap[named.String()]; !ok {
			if matchingImage.UnusedSince.IsZero() {
				matchingImage.UnusedSince = &metav1.Time{Time: time.Now()}
				log.Info("image is not used anymore, marking it as unused", "image", matchingImage.Image)
			}
		} else {
			matchingImage.UnusedSince = nil
		}
		// FIXME: update mirrors recursively (add/remove)
		matchingImagesMap[named.String()] = matchingImage
	}

	return nil
}

func newMirroringRateLimiter() workqueue.TypedRateLimiter[reconcile.Request] {
	// based on workqueue.DefaultTypedControllerRateLimiter
	return workqueue.NewTypedMaxOfRateLimiter(
		workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Second, 1000*time.Second),
		&workqueue.TypedBucketRateLimiter[reconcile.Request]{Limiter: rate.NewLimiter(rate.Limit(10), 100)},
	)
}

func getAllOtherMirrorPrefixes(ctx context.Context, c client.Client, self metav1.ObjectMeta, ignoreNamespaces bool) (map[string][]string, error) {
	var cismList kuikv1alpha1.ClusterImageSetMirrorList
	if err := c.List(ctx, &cismList); err != nil {
		return nil, err
	}
	var ismList kuikv1alpha1.ImageSetMirrorList
	if err := c.List(ctx, &ismList); err != nil {
		return nil, err
	}

	clusterwideMirrorPrefixes := make([]string, 0, len(cismList.Items)) // preallocate at least 1 mirror slot per CISM
	for _, cism := range cismList.Items {
		if self.Namespace == "" && self.Name == cism.Name {
			continue
		}
		for _, mirror := range cism.Spec.Mirrors {
			clusterwideMirrorPrefixes = append(clusterwideMirrorPrefixes, mirror.Prefix())
		}
	}

	mirrorPrefixes := map[string][]string{
		"": clusterwideMirrorPrefixes,
	}

	for _, ism := range ismList.Items {
		if self.Namespace == ism.Namespace && self.Name == ism.Name {
			continue
		}
		if ignoreNamespaces {
			ism.Namespace = ""
		}
		for _, mirror := range ism.Spec.Mirrors {
			mirrorPrefixes[ism.Namespace] = append(mirrorPrefixes[ism.Namespace], mirror.Prefix())
		}
	}

	return mirrorPrefixes, nil
}

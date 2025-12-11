package kuik

import (
	"context"
	"path"
	"strings"
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/imagefilter"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

func getPullSecret(ctx context.Context, k8sClient client.Client, namespace, name string, secret *corev1.Secret) error {
	secretReference := client.ObjectKey{Namespace: namespace, Name: name}
	if err := k8sClient.Get(ctx, secretReference, secret); err != nil {
		return err
	}
	return nil
}

func getImageSecretFromMirrors(ctx context.Context, k8sClient client.Client, image, namespace string, mirrors kuikv1alpha1.Mirrors) (*corev1.Secret, error) {
	destCredentialSecret := mirrors.GetCredentialSecretForImage(image)

	if destCredentialSecret == nil {
		return nil, nil
	}

	// This allows to use the same code for both ClusterImageSetMirror and ImageSetMirror
	if namespace == "" {
		namespace = destCredentialSecret.Namespace
	}

	secret := &corev1.Secret{}
	if err := getPullSecret(ctx, k8sClient, namespace, destCredentialSecret.Name, secret); err != nil {
		return nil, err
	}

	return secret, nil
}

func mergePreviousAndCurrentMatchingImages(ctx context.Context, pods []corev1.Pod, ismSpec *kuikv1alpha1.ImageSetMirrorSpec, ismStatus *kuikv1alpha1.ImageSetMirrorStatus) (map[string]*corev1.Pod, map[string]kuikv1alpha1.MatchingImage, error) {
	filter := ismSpec.ImageFilter.MustBuild()
	podsByMatchingImages, err := imagefilter.PodsByNormalizedMatchingImages(filter, pods)
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

	if err := updateUnusedSince(ctx, matchingImagesMap, ismStatus.MatchingImages, filter); err != nil {
		return nil, nil, err
	}

	return podsByMatchingImages, matchingImagesMap, nil
}

func updateUnusedSince(ctx context.Context, matchingImagesMap map[string]kuikv1alpha1.MatchingImage, matchingImages []kuikv1alpha1.MatchingImage, filter imagefilter.Filter) error {
	log := logf.FromContext(ctx)
	unusedSinceNotMatching := metav1.Time{Time: (time.Time{}).Add(time.Hour)}

	for _, matchingImage := range matchingImages {
		named, match, err := imagefilter.NormalizeAndMatch(filter, matchingImage.Image)
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

package internal

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/distribution/reference"
	"github.com/enix/kube-image-keeper/internal/filter"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

func RegistryAndPathFromReference(image string) (string, string, error) {
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return "", "", err
	}

	parts := strings.SplitN(named.String(), "/", 2)
	return parts[0], parts[1], nil
}

func GetPullSecretsFromPod(ctx context.Context, c client.Client, pod *corev1.Pod) ([]corev1.Secret, error) {
	secrets := []corev1.Secret{}
	for _, imagePullSecret := range pod.Spec.ImagePullSecrets {
		secret := &corev1.Secret{}
		if err := c.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: imagePullSecret.Name}, secret); err != nil {
			return nil, fmt.Errorf("could not get image pull secret %q: %w", imagePullSecret.Name, err)
		}
		secrets = append(secrets, *secret)
	}

	return secrets, nil
}

func NormalizeAndMatch(filter filter.Filter, image string) (reference.Named, bool, error) {
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return nil, false, err
	}

	return named, filter.Match(named.String()), nil
}

func MatchNormalized(filter filter.Filter, image string) bool {
	_, match, _ := NormalizeAndMatch(filter, image)
	return match
}

func PodsByNormalizedMatchingImages(ctx context.Context, filter filter.Filter, mirrorPrefixes map[string][]string, pods []corev1.Pod) (map[string]*corev1.Pod, error) {
	log := logf.FromContext(ctx)

	filteredOutImagesMap := map[string]struct{}{}
	matchingImagesMap := map[string]*corev1.Pod{}
	for _, pod := range pods {
		for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
			if strings.Contains(container.Image, "@") {
				continue // ignore digest-based images
			}

			named, match, err := NormalizeAndMatch(filter, container.Image)
			if err != nil {
				return nil, err
			}

			namedStr := named.String()
			relevantMirrorPrefixes := append(mirrorPrefixes[""], mirrorPrefixes[pod.Namespace]...)
			if slices.ContainsFunc(relevantMirrorPrefixes, func(mirrorPrefix string) bool {
				return strings.HasPrefix(namedStr, mirrorPrefix)
			}) {
				filteredOutImagesMap[namedStr] = struct{}{}
				continue
			}

			if match {
				matchingImagesMap[namedStr] = &pod
			}
		}
	}

	if len(filteredOutImagesMap) > 0 {
		filteredOutImages := slices.Collect(maps.Keys(filteredOutImagesMap))
		log.V(1).Info("filtering out images to prevent mirror loop", "images", filteredOutImages, "count", len(filteredOutImagesMap))
	}

	return matchingImagesMap, nil
}

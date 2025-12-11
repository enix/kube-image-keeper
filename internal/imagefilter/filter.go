package imagefilter

import (
	"github.com/distribution/reference"
	corev1 "k8s.io/api/core/v1"
)

// Filter is an interface that defines rules to match images.
type Filter interface {
	// Match reports whether the given image matches this filter.
	Match(image reference.Named) bool
}

func NormalizeAndMatch(i Filter, image string) (reference.Named, bool, error) {
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return nil, false, err
	}

	return named, i.Match(named), nil
}

func MatchNormalized(i Filter, image string) bool {
	_, match, _ := NormalizeAndMatch(i, image)
	return match
}

func PodsByNormalizedMatchingImages(i Filter, pods []corev1.Pod) (map[string]*corev1.Pod, error) {
	matchingImagesMap := map[string]*corev1.Pod{}
	for _, pod := range pods {
		for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
			named, match, err := NormalizeAndMatch(i, container.Image)
			if err != nil {
				return nil, err
			}

			if match {
				matchingImagesMap[named.String()] = &pod
			}
		}
	}

	return matchingImagesMap, nil
}

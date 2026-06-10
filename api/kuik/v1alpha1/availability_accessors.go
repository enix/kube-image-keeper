package v1alpha1

import (
	"github.com/enix/kube-image-keeper/internal/filter"
	corev1 "k8s.io/api/core/v1"
)

func (c *ClusterImageSetAvailability) PodMatcher() (func(pod *corev1.Pod) bool, error) {
	return podMatcher(c.Spec.Filter)
}

func (c *ClusterImageSetAvailability) ImageFilter() (filter.Filter, error) {
	return imageFilter(c.Spec.Filter, c.Spec.ImageFilter.Build)
}

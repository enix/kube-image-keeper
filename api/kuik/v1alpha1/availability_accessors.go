package v1alpha1

import (
	"github.com/enix/kube-image-keeper/internal/filter"
	corev1 "k8s.io/api/core/v1"
)

// These accessors resolve the active selection mode for ClusterImageSetAvailability:
// when spec.filter is set it wins (covering image, label, annotation and
// namespace dimensions); otherwise pod/namespace selection matches everything
// (the legacy podFilter / namespaceFilter fields have been removed) and image
// selection falls back to the deprecated imageFilter.

// PodMatcher reports whether a pod is in monitoring scope (labels, annotations
// and namespace).
func (c *ClusterImageSetAvailability) PodMatcher() (func(pod *corev1.Pod) bool, error) {
	if !c.Spec.Filter.IsEmpty() {
		return c.Spec.Filter.BuildPodMatcher()
	}
	return matchAllPods, nil
}

// ImageFilter selects which images to monitor.
func (c *ClusterImageSetAvailability) ImageFilter() (filter.Filter, error) {
	if !c.Spec.Filter.IsEmpty() {
		return c.Spec.Filter.BuildImageFilter()
	}
	return c.Spec.ImageFilter.Build()
}

package v1alpha1

import (
	"fmt"

	"github.com/enix/kube-image-keeper/internal/filter"
	corev1 "k8s.io/api/core/v1"
)

// These accessors resolve the active selection mode for ClusterImageSetAvailability
// by precedence: when spec.filter is set it wins (covering image, label,
// annotation and namespace dimensions); otherwise the legacy imageFilter /
// podFilter / namespaceFilter triplet is used.

// PodMatcher reports whether a pod is in monitoring scope (labels, annotations
// and namespace).
func (c *ClusterImageSetAvailability) PodMatcher() (func(pod *corev1.Pod) bool, error) {
	if !c.Spec.Filter.IsEmpty() {
		return c.Spec.Filter.BuildPodMatcher()
	}
	podFilter, err := c.Spec.PodFilter.Build()
	if err != nil {
		return nil, fmt.Errorf("podFilter: %w", err)
	}
	nsFilter, err := c.Spec.NamespaceFilter.Build()
	if err != nil {
		return nil, fmt.Errorf("namespaceFilter: %w", err)
	}
	return func(pod *corev1.Pod) bool {
		return podFilter.Match(pod) && nsFilter.Match(pod.Namespace)
	}, nil
}

// ImageFilter selects which images to monitor.
func (c *ClusterImageSetAvailability) ImageFilter() (filter.Filter, error) {
	if !c.Spec.Filter.IsEmpty() {
		return c.Spec.Filter.BuildImageFilter()
	}
	return c.Spec.ImageFilter.Build()
}

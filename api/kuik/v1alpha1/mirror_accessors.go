package v1alpha1

import (
	"fmt"

	"github.com/enix/kube-image-keeper/internal/filter"
	corev1 "k8s.io/api/core/v1"
)

// These accessors let a single reconciler operate on both ImageSetMirror and
// ClusterImageSetMirror through a common interface (defined in the controller
// package). They MUST live here because Go only allows methods to be defined in
// the package that declares the receiver type.
//
// The cluster-scoped variant folds its namespace filter into PodMatcher, so the
// common reconciler never needs a separate "namespace filter" concept: the
// namespaced variant matches on pod labels/annotations only, the cluster variant
// additionally requires the pod's namespace to match.

// --- ImageSetMirror (namespaced) ---

func (i *ImageSetMirror) MirrorSpec() *ImageSetMirrorSpec     { return &i.Spec }
func (i *ImageSetMirror) MirrorStatus() *ImageSetMirrorStatus { return &i.Status }

func (i *ImageSetMirror) PodMatcher() (func(pod *corev1.Pod) bool, error) {
	podFilter, err := i.Spec.PodFilter.Build()
	if err != nil {
		return nil, fmt.Errorf("podFilter: %w", err)
	}
	return podFilter.Match, nil
}

func (i *ImageSetMirror) ImageFilter() (filter.Filter, error) {
	return i.Spec.ImageFilter.Build()
}

// --- ClusterImageSetMirror (cluster-scoped) ---

func (c *ClusterImageSetMirror) MirrorSpec() *ImageSetMirrorSpec { return &c.Spec.ImageSetMirrorSpec }

func (c *ClusterImageSetMirror) MirrorStatus() *ImageSetMirrorStatus {
	return (*ImageSetMirrorStatus)(&c.Status)
}

func (c *ClusterImageSetMirror) PodMatcher() (func(pod *corev1.Pod) bool, error) {
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

func (c *ClusterImageSetMirror) ImageFilter() (filter.Filter, error) {
	return c.Spec.ImageFilter.Build()
}

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
// Each accessor resolves the active selection mode by precedence: when the
// unified spec.filter is set it wins; otherwise the legacy imageFilter /
// podFilter / namespaceFilter are used. The cluster-scoped variant folds its
// namespace dimension into PodMatcher (filter carries it as a namespace item;
// the legacy path ANDs namespaceFilter), so the common reconciler never needs a
// separate "namespace filter" concept.

// --- ImageSetMirror (namespaced) ---

func (i *ImageSetMirror) MirrorSpec() *ImageSetMirrorBase     { return &i.Spec.ImageSetMirrorBase }
func (i *ImageSetMirror) MirrorStatus() *ImageSetMirrorStatus { return &i.Status }

func (i *ImageSetMirror) PodMatcher() (func(pod *corev1.Pod) bool, error) {
	if !i.Spec.Filter.IsEmpty() {
		return i.Spec.Filter.BuildPodMatcher()
	}
	podFilter, err := i.Spec.PodFilter.Build()
	if err != nil {
		return nil, fmt.Errorf("podFilter: %w", err)
	}
	return podFilter.Match, nil
}

func (i *ImageSetMirror) ImageFilter() (filter.Filter, error) {
	if !i.Spec.Filter.IsEmpty() {
		return i.Spec.Filter.BuildImageFilter()
	}
	return i.Spec.ImageFilter.Build()
}

// --- ClusterImageSetMirror (cluster-scoped) ---

func (c *ClusterImageSetMirror) MirrorSpec() *ImageSetMirrorBase { return &c.Spec.ImageSetMirrorBase }

func (c *ClusterImageSetMirror) MirrorStatus() *ImageSetMirrorStatus {
	return (*ImageSetMirrorStatus)(&c.Status)
}

func (c *ClusterImageSetMirror) PodMatcher() (func(pod *corev1.Pod) bool, error) {
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

func (c *ClusterImageSetMirror) ImageFilter() (filter.Filter, error) {
	if !c.Spec.Filter.IsEmpty() {
		return c.Spec.Filter.BuildImageFilter()
	}
	return c.Spec.ImageFilter.Build()
}

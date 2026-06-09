package v1alpha1

import (
	"github.com/enix/kube-image-keeper/internal/filter"
	corev1 "k8s.io/api/core/v1"
)

// These accessors let a single reconciler operate on both ImageSetMirror and
// ClusterImageSetMirror through a common interface (defined in the controller
// package). They MUST live here because Go only allows methods to be defined in
// the package that declares the receiver type.
//
// Each accessor resolves the active selection mode through the shared
// podMatcher / imageFilter helpers (filter_types.go): when the unified
// spec.filter is set it wins; otherwise pod/namespace selection matches
// everything (the legacy podFilter / namespaceFilter fields have been removed)
// and image selection falls back to the deprecated imageFilter. The
// cluster-scoped variant carries the namespace dimension inside filter.

// --- ImageSetMirror (namespaced) ---

func (i *ImageSetMirror) MirrorSpec() *ImageSetMirrorBase     { return &i.Spec.ImageSetMirrorBase }
func (i *ImageSetMirror) MirrorStatus() *ImageSetMirrorStatus { return &i.Status }

func (i *ImageSetMirror) PodMatcher() (func(pod *corev1.Pod) bool, error) {
	return podMatcher(i.Spec.Filter)
}

func (i *ImageSetMirror) ImageFilter() (filter.Filter, error) {
	return imageFilter(i.Spec.Filter, i.Spec.ImageFilter.Build)
}

// --- ClusterImageSetMirror (cluster-scoped) ---

func (c *ClusterImageSetMirror) MirrorSpec() *ImageSetMirrorBase { return &c.Spec.ImageSetMirrorBase }

func (c *ClusterImageSetMirror) MirrorStatus() *ImageSetMirrorStatus {
	return (*ImageSetMirrorStatus)(&c.Status)
}

func (c *ClusterImageSetMirror) PodMatcher() (func(pod *corev1.Pod) bool, error) {
	return podMatcher(c.Spec.Filter)
}

func (c *ClusterImageSetMirror) ImageFilter() (filter.Filter, error) {
	return imageFilter(c.Spec.Filter, c.Spec.ImageFilter.Build)
}

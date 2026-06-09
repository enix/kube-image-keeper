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
// Each accessor resolves the active selection mode by precedence: when the
// unified spec.filter is set it wins; otherwise pod/namespace selection matches
// everything (the legacy podFilter / namespaceFilter fields have been removed)
// and image selection falls back to the deprecated imageFilter. The
// cluster-scoped variant carries the namespace dimension inside filter.

// matchAllPods is the pod matcher used when spec.filter is unset: it matches
// every pod, which is the behaviour an empty podFilter/namespaceFilter had.
func matchAllPods(*corev1.Pod) bool { return true }

// --- ImageSetMirror (namespaced) ---

func (i *ImageSetMirror) MirrorSpec() *ImageSetMirrorBase     { return &i.Spec.ImageSetMirrorBase }
func (i *ImageSetMirror) MirrorStatus() *ImageSetMirrorStatus { return &i.Status }

func (i *ImageSetMirror) PodMatcher() (func(pod *corev1.Pod) bool, error) {
	if !i.Spec.Filter.IsEmpty() {
		return i.Spec.Filter.BuildPodMatcher()
	}
	return matchAllPods, nil
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
	return matchAllPods, nil
}

func (c *ClusterImageSetMirror) ImageFilter() (filter.Filter, error) {
	if !c.Spec.Filter.IsEmpty() {
		return c.Spec.Filter.BuildImageFilter()
	}
	return c.Spec.ImageFilter.Build()
}

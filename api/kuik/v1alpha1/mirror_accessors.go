package v1alpha1

import (
	"github.com/enix/kube-image-keeper/internal/filter"
	corev1 "k8s.io/api/core/v1"
)

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

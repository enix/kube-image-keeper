package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
)

// These accessors resolve the active selection mode for the replicated kinds:
// when spec.filter is set it wins; otherwise pod/namespace selection matches
// everything (the legacy podFilter / namespaceFilter fields have been removed).
// Replicated kinds have no top-level imageFilter (image selection is
// per-upstream), so the filter image dimension acts as an extra top-level gate;
// an empty filter matches every image, preserving the legacy behaviour.

// --- ReplicatedImageSet (namespaced) ---

func (r *ReplicatedImageSet) PodMatcher() (func(pod *corev1.Pod) bool, error) {
	return podMatcher(r.Spec.Filter)
}

// --- ClusterReplicatedImageSet (cluster-scoped) ---

func (c *ClusterReplicatedImageSet) PodMatcher() (func(pod *corev1.Pod) bool, error) {
	return podMatcher(c.Spec.Filter)
}

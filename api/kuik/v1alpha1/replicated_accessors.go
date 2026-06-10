package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
)

// --- ReplicatedImageSet (namespaced) ---

func (r *ReplicatedImageSet) PodMatcher() (func(pod *corev1.Pod) bool, error) {
	return podMatcher(r.Spec.Filter)
}

// --- ClusterReplicatedImageSet (cluster-scoped) ---

func (c *ClusterReplicatedImageSet) PodMatcher() (func(pod *corev1.Pod) bool, error) {
	return podMatcher(c.Spec.Filter)
}

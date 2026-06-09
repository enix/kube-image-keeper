package v1alpha1

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

// These accessors resolve the active selection mode for the replicated kinds by
// precedence: when spec.filter is set it wins; otherwise the legacy podFilter
// (and namespaceFilter for the cluster-scoped kind) are used. Replicated kinds
// have no top-level imageFilter (image selection is per-upstream), so the filter
// image dimension acts as an extra top-level gate; an empty filter matches every
// image, preserving the legacy behaviour.

// --- ReplicatedImageSet (namespaced) ---

func (r *ReplicatedImageSet) PodMatcher() (func(pod *corev1.Pod) bool, error) {
	if !r.Spec.Filter.IsEmpty() {
		return r.Spec.Filter.BuildPodMatcher()
	}
	podFilter, err := r.Spec.PodFilter.Build()
	if err != nil {
		return nil, fmt.Errorf("podFilter: %w", err)
	}
	return podFilter.Match, nil
}

// --- ClusterReplicatedImageSet (cluster-scoped) ---

func (c *ClusterReplicatedImageSet) PodMatcher() (func(pod *corev1.Pod) bool, error) {
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

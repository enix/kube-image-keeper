package v1alpha1

import (
	"github.com/enix/kube-image-keeper/internal/filter"
)

// SelectorFilter holds include / exclude lists for one pod-metadata
// dimension (labels or annotations). Entries follow Kubernetes label-selector
// syntax (k8s.io/apimachinery/pkg/labels.Parse): equality ("key=value"),
// inequality ("key!=value"), presence ("key"), absence ("!key"), and
// set-based operators ("key in (a,b)", "key notin (a,b)").
type SelectorFilter struct {
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=512
	Include []string `json:"include,omitempty"`
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=512
	Exclude []string `json:"exclude,omitempty"`
}

// PodFilter narrows which pods this resource applies to, by pod labels
// and annotations. When omitted or empty, the resource applies to every pod.
type PodFilterDefinition struct {
	// +optional
	Labels SelectorFilter `json:"labels,omitempty"`
	// +optional
	Annotations SelectorFilter `json:"annotations,omitempty"`
}

func (p PodFilterDefinition) Build() (*filter.PodFilter, error) {
	return filter.CompilePodFilter(
		p.Labels.Include, p.Labels.Exclude,
		p.Annotations.Include, p.Annotations.Exclude,
	)
}

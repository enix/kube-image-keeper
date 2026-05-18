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

// PodFilterDefinition narrows which pods a mirroring resource applies to,
// independently by labels and annotations. Default-allow semantics: an empty
// definition matches every pod; a non-empty Include narrows to its matches;
// an Exclude removes (exclude wins on tie).
//
// Note: label-selector value syntax is constrained to DNS-1123 label values
// (≤63 chars, alphanumeric + "-_."). Equality matches against annotation
// values that don't conform will fail at compile time; for free-form
// annotation values, prefer presence-only entries ("key") or absence ("!key").
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

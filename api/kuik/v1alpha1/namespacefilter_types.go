package v1alpha1

import (
	"github.com/enix/kube-image-keeper/internal/filter"
)

// IncludeExcludeFilterDefinition is a generic filter definition
type IncludeExcludeFilterDefinition struct {
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=128
	// +kubebuilder:validation:XValidation:rule="self.all(p, ''.matches(p) ? true : true)",message="include contains an invalid regular expression"
	Include []string `json:"include,omitempty"`
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=128
	// +kubebuilder:validation:XValidation:rule="self.all(p, ''.matches(p) ? true : true)",message="exclude contains an invalid regular expression"
	Exclude []string `json:"exclude,omitempty"`
}

// NamespaceFilter restricts which namespaces this cluster-scoped resource applies to.
// When omitted or empty, the resource applies to every namespace.
type NamespaceFilterDefinition IncludeExcludeFilterDefinition

func (n NamespaceFilterDefinition) Build() (filter.Filter, error) {
	include := n.Include
	if len(n.Include) == 0 {
		include = []string{".*"}
	}
	return filter.CompileIncludeExcludeFilter(include, n.Exclude)
}

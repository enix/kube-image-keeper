package v1alpha1

import (
	"github.com/enix/kube-image-keeper/internal/filter"
)

// NamespaceFilterDefinition selects which namespaces a cluster-scoped resource
// applies to. Both lists hold RE2 regular expressions matched against the
// pod namespace. Default-allow semantics: both empty means every namespace is
// in scope; a non-empty Include narrows to its matches; an entry matching
// Include wins over an entry matching Exclude (enables a deny-all-then-allowlist
// idiom with Exclude=[".*"] + Include=[allowlist]).
type NamespaceFilterDefinition struct {
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=128
	// +kubebuilder:validation:XValidation:rule="self.all(p, ''.matches(p) ? true : true)",message="include contains an invalid regular expression"
	Include []string `json:"include,omitempty"`
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=128
	// +kubebuilder:validation:XValidation:rule="self.all(p, ''.matches(p) ? true : true)",message="exclude contains an invalid regular expression"
	Exclude []string `json:"exclude,omitempty"`
}

func (n NamespaceFilterDefinition) Build() (*filter.NamespaceFilter, error) {
	return filter.CompileNamespaceFilter(n.Include, n.Exclude)
}

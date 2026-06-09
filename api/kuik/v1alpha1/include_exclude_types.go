package v1alpha1

// IncludeExcludeFilterDefinition is a generic regex include / exclude filter
// definition. It backs ImageFilterDefinition.
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

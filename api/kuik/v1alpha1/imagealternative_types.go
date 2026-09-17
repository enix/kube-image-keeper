package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// ImageAlternativeSpec defines the desired state of ImageAlternative.
type ImageAlternativeSpec struct {
	// podSelector restricts the pods this resource applies to. Empty or absent matches every
	// pod.
	// +optional
	PodSelector *metav1.LabelSelector `json:"podSelector,omitempty"`

	// namespaceSelector restricts the namespaces this resource applies to. Empty or absent
	// matches every namespace.
	// +optional
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// rewritePolicy says where the entries sit relative to the original image: OnFailure tries
	// the original first and the entries as fallbacks, Always tries the entries first.
	// +kubebuilder:validation:Enum=OnFailure;Always
	// +kubebuilder:default=OnFailure
	// +optional
	RewritePolicy RewritePolicy `json:"rewritePolicy,omitempty"`

	// alternatives is the ordered list of equivalent repositories, or repository groups. Every
	// entry of a list uses the same form.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:XValidation:rule="self.all(a, has(a.repository)) || self.all(a, has(a.repositoryGroup))",message="alternatives must all be repository entries or all be repositoryGroup entries"
	// +listType=atomic
	// +required
	Alternatives []Alternative `json:"alternatives"`
}

// ImageAlternativeStatus defines the observed state of ImageAlternative.
type ImageAlternativeStatus struct {
	RoutingStatus `json:",inline"`

	// truncated records, per capped list that reached its cap, how many entries were left
	// out. Present only once a cap was reached.
	// +optional
	Truncated map[string]int32 `json:"truncated,omitempty"`

	// conditions report the state of the resource. Ready is the only one that is True when
	// things are well; FallbackActive, AlternativesExhausted and ListCapacityPressure each name
	// an anomaly and stay True as long as it lasts.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Policy",type=string,JSONPath=`.spec.rewritePolicy`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Fallback",type=string,JSONPath=`.status.conditions[?(@.type=="FallbackActive")].status`
// +kubebuilder:printcolumn:name="Rewritten",type=integer,JSONPath=`.status.containers.rewritten`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ImageAlternative routes images to alternative repositories holding the same images, as
// fallbacks when the original fails or ahead of it.
type ImageAlternative struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ImageAlternative
	// +required
	Spec ImageAlternativeSpec `json:"spec"`

	// status defines the observed state of ImageAlternative
	// +optional
	Status ImageAlternativeStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ImageAlternativeList contains a list of ImageAlternative
type ImageAlternativeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ImageAlternative `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &ImageAlternative{}, &ImageAlternativeList{})
		return nil
	})
}

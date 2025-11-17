package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ReplicatedImageSetSpec defines the desired state of ReplicatedImageSet.
type ReplicatedImageSetSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Foo is an example field of ReplicatedImageSet. Edit replicatedimageset_types.go to remove/update
	Foo string `json:"foo,omitempty"`
}

// ReplicatedImageSetStatus defines the observed state of ReplicatedImageSet.
type ReplicatedImageSetStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ReplicatedImageSet is the Schema for the replicatedimagesets API.
type ReplicatedImageSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ReplicatedImageSetSpec   `json:"spec,omitempty"`
	Status ReplicatedImageSetStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ReplicatedImageSetList contains a list of ReplicatedImageSet.
type ReplicatedImageSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ReplicatedImageSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ReplicatedImageSet{}, &ReplicatedImageSetList{})
}

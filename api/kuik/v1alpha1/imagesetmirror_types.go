package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ImageSetMirrorSpec defines the desired state of ImageSetMirror.
type ImageSetMirrorSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Foo is an example field of ImageSetMirror. Edit imagesetmirror_types.go to remove/update
	Foo string `json:"foo,omitempty"`
}

// ImageSetMirrorStatus defines the observed state of ImageSetMirror.
type ImageSetMirrorStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ImageSetMirror is the Schema for the imagesetmirrors API.
type ImageSetMirror struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ImageSetMirrorSpec   `json:"spec,omitempty"`
	Status ImageSetMirrorStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ImageSetMirrorList contains a list of ImageSetMirror.
type ImageSetMirrorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ImageSetMirror `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ImageSetMirror{}, &ImageSetMirrorList{})
}

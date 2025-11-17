package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ReplicatedImageSetSpec defines the desired state of ReplicatedImageSet.
type ReplicatedImageSetSpec struct {
	Upstreams []ReplicatedUpstream `json:"upstreams,omitempty"`
}

// ReplicatedImageSetStatus defines the observed state of ReplicatedImageSet.
type ReplicatedImageSetStatus struct{}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ris

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

type ReplicatedUpstream struct {
	ImageReference `json:",inline"`
	// ImageMatcher is a regexp identifying the image in a registry
	ImageMatcher string `json:"imageMatcher"`
	// CredentialSecret is the image pull secret to use for matching images
	CredentialSecret *CredentialSecret `json:"credentialSecret,omitempty"`
}

func init() {
	SchemeBuilder.Register(&ReplicatedImageSet{}, &ReplicatedImageSetList{})
}

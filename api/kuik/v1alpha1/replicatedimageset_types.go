package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ReplicatedImageSetBase holds the fields shared by ReplicatedImageSet and
// ClusterReplicatedImageSet. It is extracted from ReplicatedImageSetSpec so each
// scope can carry its own filter type (Filter for the namespaced kind,
// ClusterFilter for the cluster-scoped kind) without the embedded namespaced
// spec forcing a single shared type.
type ReplicatedImageSetBase struct {
	// Priority controls the ordering of alternatives from this CR relative to the original image and other CRs.
	// Negative values place alternatives before the original image; positive values place them after.
	// Default is 0 (original image first, then alternatives in default type order).
	// +optional
	Priority int `json:"priority,omitempty"`
	// +optional
	PodFilter PodFilterDefinition `json:"podFilter,omitempty"`
	// +kubebuilder:validation:MaxItems=32
	Upstreams []ReplicatedUpstream `json:"upstreams,omitempty"`
}

// ReplicatedImageSetSpec defines the desired state of ReplicatedImageSet.
type ReplicatedImageSetSpec struct {
	ReplicatedImageSetBase `json:",inline"`

	// Filter selects which pods and images this resource applies to. It
	// replaces the deprecated imageFilter.
	// +optional
	Filter Filter `json:"filter,omitempty"`
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
	// Priority controls the ordering of this mirror in comparaison to similar alternatives (replicated upstream with same parent priority) when re-routing images.
	// 0 means no specific ordering (YAML declaration order is preserved).
	// Positive values are sorted ascending: lower value = higher priority.
	// +optional
	Priority uint `json:"priority,omitempty"`
	// DiscardAlternative excludes this upstream from image routing while keeping it in the configuration.
	// The upstream still participates in image matching, so other upstreams in the same CR continue to work.
	// Useful when a registry no longer exists, to avoid waiting for the check timeout.
	// +optional
	DiscardAlternative bool `json:"discardAlternative,omitempty"`
	// ImageFilter defines the rules used to select replicated images.
	// +optional
	ImageFilter ImageFilterDefinition `json:"imageFilter"`
	// CredentialSecret is a reference to the secret used to pull matching images.
	CredentialSecret *CredentialSecret `json:"credentialSecret,omitempty"`
}

func init() {
	SchemeBuilder.Register(&ReplicatedImageSet{}, &ReplicatedImageSetList{})
}

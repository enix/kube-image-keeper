package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterReplicatedImageSetSpec defines the desired state of ClusterReplicatedImageSet.
type ClusterReplicatedImageSetSpec ReplicatedImageSetSpec

// ClusterReplicatedImageSetStatus defines the observed state of ClusterReplicatedImageSet.
type ClusterReplicatedImageSetStatus ReplicatedImageSetStatus

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=cris

// ClusterReplicatedImageSet is the Schema for the clusterreplicatedimagesets API.
type ClusterReplicatedImageSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterReplicatedImageSetSpec   `json:"spec,omitempty"`
	Status ClusterReplicatedImageSetStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterReplicatedImageSetList contains a list of ClusterReplicatedImageSet.
type ClusterReplicatedImageSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterReplicatedImageSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterReplicatedImageSet{}, &ClusterReplicatedImageSetList{})
}

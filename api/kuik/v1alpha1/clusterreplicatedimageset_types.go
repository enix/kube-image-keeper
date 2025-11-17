package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ClusterReplicatedImageSetSpec defines the desired state of ClusterReplicatedImageSet.
type ClusterReplicatedImageSetSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Foo is an example field of ClusterReplicatedImageSet. Edit clusterreplicatedimageset_types.go to remove/update
	Foo string `json:"foo,omitempty"`
}

// ClusterReplicatedImageSetStatus defines the observed state of ClusterReplicatedImageSet.
type ClusterReplicatedImageSetStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

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

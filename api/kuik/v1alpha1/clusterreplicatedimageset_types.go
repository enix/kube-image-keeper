package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterReplicatedImageSetSpec defines the desired state of ClusterReplicatedImageSet.
// +kubebuilder:validation:XValidation:rule="!has(self.filter) || ((!has(self.filter.include) || self.filter.include.all(i, !has(i.image))) && (!has(self.filter.exclude) || self.filter.exclude.all(i, !has(i.image))))",message="spec.filter image items are not supported on ClusterReplicatedImageSet; image selection is per-upstream via spec.upstreams[].imageFilter"
type ClusterReplicatedImageSetSpec struct {
	ReplicatedImageSetBase `json:",inline"`

	// Filter selects which pods and namespaces this resource applies to (label,
	// annotation and namespace dimensions). The image dimension is not supported
	// here: image selection is per-upstream via spec.upstreams[].imageFilter.
	// +optional
	Filter ClusterFilter `json:"filter,omitempty"`
}

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

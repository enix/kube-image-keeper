package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ClusterImageSetAvailabilitySpec defines the desired state of ClusterImageSetAvailability.
type ClusterImageSetAvailabilitySpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Foo is an example field of ClusterImageSetAvailability. Edit clusterimagesetavailability_types.go to remove/update
	Foo string `json:"foo,omitempty"`
}

// ClusterImageSetAvailabilityStatus defines the observed state of ClusterImageSetAvailability.
type ClusterImageSetAvailabilityStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// ClusterImageSetAvailability is the Schema for the clusterimagesetavailabilities API.
type ClusterImageSetAvailability struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterImageSetAvailabilitySpec   `json:"spec,omitempty"`
	Status ClusterImageSetAvailabilityStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterImageSetAvailabilityList contains a list of ClusterImageSetAvailability.
type ClusterImageSetAvailabilityList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterImageSetAvailability `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterImageSetAvailability{}, &ClusterImageSetAvailabilityList{})
}

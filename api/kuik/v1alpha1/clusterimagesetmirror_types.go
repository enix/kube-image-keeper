package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ClusterImageSetMirrorSpec defines the desired state of ClusterImageSetMirror.
type ClusterImageSetMirrorSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Foo is an example field of ClusterImageSetMirror. Edit clusterimagesetmirror_types.go to remove/update
	Foo string `json:"foo,omitempty"`
}

// ClusterImageSetMirrorStatus defines the observed state of ClusterImageSetMirror.
type ClusterImageSetMirrorStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// ClusterImageSetMirror is the Schema for the clusterimagesetmirrors API.
type ClusterImageSetMirror struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterImageSetMirrorSpec   `json:"spec,omitempty"`
	Status ClusterImageSetMirrorStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterImageSetMirrorList contains a list of ClusterImageSetMirror.
type ClusterImageSetMirrorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterImageSetMirror `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterImageSetMirror{}, &ClusterImageSetMirrorList{})
}

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ImageMirrorSpec defines the desired state of ImageMirror.
// +required
type ImageMirrorSpec struct {
	// ImageReference is the reference of the image to mirror
	ImageReference `json:",inline"`
	// TargetRegistry is the registry on which the image should be mirrored
	TargetRegistry string `json:"targetRegistry"`
}

// ImageMirrorStatus defines the observed state of ImageMirror.
type ImageMirrorStatus struct {
	// Digest is the digest of the mirrored image
	Digest     string             `json:"digest,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=imgmir
// +kubebuilder:printcolumn:name="Image",type="string",JSONPath=".spec.path"
// +kubebuilder:printcolumn:name="From",type="string",JSONPath=".spec.registry"
// +kubebuilder:printcolumn:name="To",type="string",JSONPath=".spec.targetRegistry"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ImageMirror is the Schema for the imagemirrors API.
type ImageMirror struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ImageMirrorSpec   `json:"spec,omitempty"`
	Status ImageMirrorStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ImageMirrorList contains a list of ImageMirror.
type ImageMirrorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ImageMirror `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ImageMirror{}, &ImageMirrorList{})
}

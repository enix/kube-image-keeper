package v1alpha1

import (
	"github.com/enix/kube-image-keeper/internal/matchers"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterImageSetMirrorSpec defines the desired state of ClusterImageSetMirror.
type ClusterImageSetMirrorSpec ImageSetMirrorSpec

// ClusterImageSetMirrorStatus defines the observed state of ClusterImageSetMirror.
type ClusterImageSetMirrorStatus ImageSetMirrorStatus

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=cism

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

func (i *ClusterImageSetMirrorSpec) BuildMatcher() (matchers.ImageMatcher, error) {
	// TODO: validating webhook for the regexp
	return matchers.NewRegexpImageMatcher(i.ImageMatcher)
}

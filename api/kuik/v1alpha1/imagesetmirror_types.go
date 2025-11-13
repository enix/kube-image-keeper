package v1alpha1

import (
	"path"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ImageSetMirrorSpec defines the desired state of ImageSetMirror.
type ImageSetMirrorSpec struct {
	ImageMatcher string  `json:"imageMatcher,omitempty"`
	Cleanup      Cleanup `json:"cleanup,omitempty"`
	Mirrors      Mirrors `json:"mirrors,omitempty"`
}

// ImageSetMirrorStatus defines the observed state of ImageSetMirror.
type ImageSetMirrorStatus struct {
	// +listType=map
	// +listMapKey=image
	MatchedImages []MatchedImage `json:"matchedImages,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ism

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

// Cleanup defines a cleanup strategy
type Cleanup struct {
	Enabled   bool            `json:"enabled,omitempty"`
	Retention metav1.Duration `json:"retention,omitempty"`
}

type Mirror struct {
	Registry         string            `json:"registry,omitempty"`
	Path             string            `json:"path,omitempty"`
	CredentialSecret *CredentialSecret `json:"credentialSecret,omitempty"`
	Cleanup          *Cleanup          `json:"cleanup,omitempty"`
}

type Mirrors []Mirror

type CredentialSecret struct {
	// Name is the name of the secret
	Name string `json:"name,omitempty"`
	// Namespace is the namespace where the secret is located.
	// This value is ignored for namespaced resources and the namespace of the parent object is used instead.
	Namespace string `json:"namespace,omitempty"` // TODO: make this field required for ClusterImageSetMirrors and prohibited for ImageSetMirrors
}

type MatchedImage struct {
	Image string `json:"image"`
	// +listType=map
	// +listMapKey=image
	Mirrors     []MirrorStatus   `json:"mirrors,omitempty"`
	UnusedSince *metav1.Duration `json:"unusedSince,omitempty"`
}

type MirrorStatus struct {
	Image      string       `json:"image"`
	MirroredAt *metav1.Time `json:"mirroredAt,omitempty"`
}

func init() {
	SchemeBuilder.Register(&ImageSetMirror{}, &ImageSetMirrorList{})
}

func (m Mirrors) GetCredentialSecretForImage(image string) (cred *CredentialSecret) {
	longestPrefixLen := 0
	for _, mirror := range m {
		prefix := path.Join(mirror.Registry, mirror.Path)
		if strings.HasPrefix(image, prefix) && len(prefix) > longestPrefixLen {
			cred = mirror.CredentialSecret
			longestPrefixLen = len(prefix)
		}
	}
	return
}

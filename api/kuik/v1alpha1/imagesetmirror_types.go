package v1alpha1

import (
	"path"
	"strings"

	"github.com/enix/kube-image-keeper/internal/filter"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ImageSetMirrorBase holds the fields shared by ImageSetMirror and
// ClusterImageSetMirror. It is extracted from ImageSetMirrorSpec so each scope
// can carry its own filter type (Filter for the namespaced kind, ClusterFilter
// for the cluster-scoped kind) without the embedded namespaced spec forcing a
// single shared type.
type ImageSetMirrorBase struct {
	// Priority controls the ordering of alternatives from this CR relative to the original image and other CRs.
	// Negative values place alternatives before the original image; positive values place them after.
	// Default is 0 (original image first, then alternatives in default type order).
	// +optional
	Priority int `json:"priority,omitempty"`
	// ImageFilter selects which images to mirror. Superseded by filter.
	// +optional
	ImageFilter ImageFilterDefinition `json:"imageFilter,omitempty"`
	Cleanup     Cleanup               `json:"cleanup,omitempty"`
	Mirrors     Mirrors               `json:"mirrors,omitempty"`
}

// ImageSetMirrorSpec defines the desired state of ImageSetMirror.
// +kubebuilder:validation:XValidation:rule="!((has(self.imageFilter) && (has(self.imageFilter.include) || has(self.imageFilter.exclude))) && (has(self.filter) && (has(self.filter.include) || has(self.filter.exclude))))",message="spec.filter and the deprecated spec.imageFilter are mutually exclusive; fold imageFilter patterns into spec.filter image items"
type ImageSetMirrorSpec struct {
	ImageSetMirrorBase `json:",inline"`

	// Filter selects which pods and images this resource applies to. It
	// replaces the deprecated imageFilter.
	// +optional
	Filter Filter `json:"filter,omitempty"`
}

// ImageSetMirrorStatus defines the observed state of ImageSetMirror.
type ImageSetMirrorStatus struct {
	// +listType=map
	// +listMapKey=image
	MatchingImages []MatchingImage `json:"matchingImages,omitempty"`
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

// ImageFilterDefinition is the definition of an image filter.
type ImageFilterDefinition IncludeExcludeFilterDefinition

// Cleanup defines a cleanup strategy
type Cleanup struct {
	Enabled   bool            `json:"enabled,omitempty"`
	Retention metav1.Duration `json:"retention,omitempty"`
}

type Mirror struct {
	// Priority controls the ordering of this mirror in comparaison to similar alternatives (mirrors with same parent priority) when re-routing images.
	// 0 means no specific ordering (YAML declaration order is preserved).
	// Positive values are sorted ascending: lower value = higher priority.
	// +optional
	Priority         uint              `json:"priority,omitempty"`
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

type MatchingImage struct {
	Image string `json:"image"`
	// +listType=map
	// +listMapKey=image
	Mirrors     []MirrorStatus `json:"mirrors,omitempty"`
	UnusedSince *metav1.Time   `json:"unusedSince,omitempty"`
}

type MirrorStatus struct {
	Image      string       `json:"image"`
	MirroredAt *metav1.Time `json:"mirroredAt,omitempty"`
	LastError  string       `json:"lastError,omitempty"`
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

func (i ImageFilterDefinition) Build() (filter.Filter, error) {
	include := i.Include
	if len(i.Include) == 0 && len(i.Exclude) > 0 {
		include = []string{".*"}
	}
	return filter.CompileIncludeExcludeFilter(include, i.Exclude)
}

func (i ImageFilterDefinition) MustBuild() filter.Filter {
	f, err := i.Build()
	if err != nil {
		panic(err)
	}
	return f
}

func (i ImageFilterDefinition) BuildWithRegistry(registry string) (filter.Filter, error) {
	return filter.AddPrefixToFilter(registry, i.Build)
}

func (i ImageFilterDefinition) MustBuildWithRegistry(registry string) filter.Filter {
	f, err := i.BuildWithRegistry(registry)
	if err != nil {
		panic(err)
	}
	return f
}

func (m *Mirror) Prefix() string {
	return path.Join(m.Registry, m.Path)
}

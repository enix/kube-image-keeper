package v1alpha1

import (
	"strings"

	"github.com/distribution/reference"
	"github.com/enix/kube-image-keeper/internal/registry"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ImageSpec defines the desired state of Image.
type ImageSpec struct {
	// Full name of the image (including its registry and tag)
	Name string `json:"name"`
}

type PodReference struct {
	// NamespacedName is the namespaced name of a pod (namespace/name)
	NamespacedName string `json:"namespacedName,omitempty"`
}

type UsedBy struct {
	// Pods is a list of reference to pods using this Image
	Pods []PodReference `json:"pods,omitempty" patchStrategy:"merge" patchMergeKey:"namespacedName"`
	// Count is the number of pods using this image
	//
	// jsonpath function .length() is not implemented, so the count field is required to display pods count in additionalPrinterColumns
	// see https://github.com/kubernetes-sigs/controller-tools/issues/447
	Count int `json:"count,omitempty"`
}

// ImageStatus defines the observed state of Image.
type ImageStatus struct {
	// UsedBy is the list of pods using this image
	UsedBy UsedBy `json:"usedBy,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=img
// +kubebuilder:printcolumn:name="Pods count",type="integer",JSONPath=".status.usedBy.count"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// Image is the Schema for the images API.
type Image struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ImageSpec   `json:"spec,omitempty"`
	Status ImageStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ImageList contains a list of Image.
type ImageList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Image `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Image{}, &ImageList{})
}

func ImageNameFromSourceImage(sourceImage string) (string, error) {
	ref, err := reference.ParseAnyReference(sourceImage)
	if err != nil {
		return "", err
	}

	sanitizedName := registry.SanitizeName(ref.String())
	if !strings.Contains(sourceImage, ":") {
		sanitizedName += "-latest"
	}

	return sanitizedName, nil
}

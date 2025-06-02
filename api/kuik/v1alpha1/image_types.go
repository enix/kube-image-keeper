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

type ReferencesWithCount struct {
	// Items is a list of reference to objects using this Image
	Items []string `json:"items,omitempty"`
	// Count is the number of objects using this image
	//
	// jsonpath function .length() is not implemented, so the count field is required to display objects count in additionalPrinterColumns
	// see https://github.com/kubernetes-sigs/controller-tools/issues/447
	Count int `json:"count,omitempty"`
}

// ImageStatus defines the observed state of Image.
type ImageStatus struct {
	// UsedByPods is the list of pods using this image
	UsedByPods ReferencesWithCount `json:"usedByPods,omitempty"`
	// AvailableOnNodes is the list of nodes that have this image available locally
	AvailableOnNodes ReferencesWithCount `json:"availableOnNodes,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=img
// +kubebuilder:printcolumn:name="Pods count",type="integer",JSONPath=".status.usedByPods.count"
// +kubebuilder:printcolumn:name="Nodes count",type="integer",JSONPath=".status.availableOnNodes.count"
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

func ImageFromSourceImage(sourceImage string) (*Image, error) {
	sanitizedName, err := imageNameFromSourceImage(sourceImage)
	if err != nil {
		return nil, err
	}

	return &Image{
		TypeMeta: metav1.TypeMeta{APIVersion: GroupVersion.String(), Kind: "Image"},
		ObjectMeta: metav1.ObjectMeta{
			Name: sanitizedName,
		},
		Spec: ImageSpec{
			Name: sourceImage,
		},
	}, nil
}

func imageNameFromSourceImage(sourceImage string) (string, error) {
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

package v1alpha1

import (
	"fmt"
	"strings"

	"github.com/cespare/xxhash/v2"
	"github.com/distribution/reference"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ImageSpec defines the desired state of Image.
// +required
type ImageSpec struct {
	// Registry is the registry where the image is located
	Registry string `json:"registry"`
	// Image is a string identifying the image
	Image string `json:"image"`
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

type Upstream struct {
	// LastMonitor is the last time the upstream image was monitored
	LastMonitor metav1.Time `json:"lastMonitor,omitempty"`
	// LastSeen is the last time the image was seen upstream
	LastSeen metav1.Time `json:"lastSeen,omitempty"`
	// Digest is the digest of the upstream image manifest, if available
	Digest string `json:"digest,omitempty"`
	// MediaType is the media type of the upstream image manifest, if available
	MediaType string `json:"mediaType,omitempty"`
}

// ImageStatus defines the observed state of Image.
type ImageStatus struct {
	// UsedByPods is the list of pods using this image
	UsedByPods ReferencesWithCount `json:"usedByPods,omitempty"`
	// AvailableOnNodes is the list of nodes that have this image available locally
	AvailableOnNodes ReferencesWithCount `json:"availableOnNodes,omitempty"`
	// Upstream is the information about the upstream image
	Upstream Upstream `json:"upstream,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=img
// +kubebuilder:selectablefield:JSONPath=".spec.registry"
// +kubebuilder:printcolumn:name="Registry",type="string",JSONPath=".spec.registry"
// +kubebuilder:printcolumn:name="Image",type="string",JSONPath=".spec.image"
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

func ImageFromReference(reference string) (*Image, error) {
	sanitizedName, err := imageNameFromReference(reference)
	if err != nil {
		return nil, err
	}

	registry, image, err := registryNameFromReference(reference)
	if err != nil {
		return nil, err
	}

	return &Image{
		TypeMeta: metav1.TypeMeta{APIVersion: GroupVersion.String(), Kind: "Image"},
		ObjectMeta: metav1.ObjectMeta{
			Name: sanitizedName,
		},
		Spec: ImageSpec{
			Registry: registry,
			Image:    image,
		},
	}, nil
}

func (i *Image) Reference() string {
	return i.Spec.Registry + "/" + i.Spec.Image
}

func imageNameFromReference(image string) (string, error) {
	ref, err := reference.ParseAnyReference(image)
	if err != nil {
		return "", err
	}

	image = ref.String()
	if !strings.Contains(image, ":") {
		image += "-latest"
	}

	h := xxhash.Sum64String(image)

	return fmt.Sprintf("%016x", h), nil
}

func registryNameFromReference(image string) (string, string, error) {
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return "", "", err
	}

	parts := strings.SplitN(named.String(), "/", 2)
	return parts[0], parts[1], nil
}

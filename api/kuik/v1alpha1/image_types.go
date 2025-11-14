package v1alpha1

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/enix/kube-image-keeper/internal"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ImageSpec defines the desired state of Image.
// +required
type ImageSpec struct {
	// ImageReference is the reference of the image
	ImageReference `json:",inline"`
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
	// UnusedSince is the time when the image was last used by a pod
	UnusedSince metav1.Time `json:"unusedSince,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=img
// +kubebuilder:selectablefield:JSONPath=".spec.registry"
// +kubebuilder:printcolumn:name="Registry",type="string",JSONPath=".spec.registry"
// +kubebuilder:printcolumn:name="Path",type="string",JSONPath=".spec.path"
// +kubebuilder:printcolumn:name="Pods count",type="integer",JSONPath=".status.usedByPods.count"
// +kubebuilder:printcolumn:name="Unused since",type="date",JSONPath=".status.unusedSince"
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

func ImagesFromPod(pod *corev1.Pod) ([]Image, []error) {
	return imagesFromContainers(append(pod.Spec.Containers, pod.Spec.InitContainers...))
}

func imagesFromContainers(containers []corev1.Container) ([]Image, []error) {
	images := []Image{}
	errs := []error{}

	for _, container := range containers {
		image, err := imageFromReference(container.Image)
		if err != nil {
			errs = append(errs, fmt.Errorf("could not create image from reference %q: %w", container.Image, err))
		}
		images = append(images, *image)
	}

	return images, errs
}

func imageFromReference(reference string) (*Image, error) {
	name, err := internal.ImageNameFromReference(reference)
	if err != nil {
		return nil, err
	}

	registry, image, err := internal.RegistryNameFromReference(reference)
	if err != nil {
		return nil, err
	}

	return &Image{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: ImageSpec{
			ImageReference{
				Registry: registry,
				Path:     image,
			},
		},
	}, nil
}

func (i *Image) Reference() string {
	return i.Spec.Reference()
}

func (i *Image) GetPullSecrets(ctx context.Context, c client.Client) (secrets []corev1.Secret, err error) {
	if len(i.Status.UsedByPods.Items) == 0 {
		return nil, errors.New("image has no pods using it")
	}
	podRef := strings.Split(i.Status.UsedByPods.Items[0], "/")
	pod := &corev1.Pod{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: podRef[0], Name: podRef[1]}, pod); err != nil {
		return nil, err
	}

	return internal.GetPullSecretsFromPod(ctx, c, pod)
}

func (i *Image) IsUsedByPods() bool {
	return len(i.Status.UsedByPods.Items) > 0
}

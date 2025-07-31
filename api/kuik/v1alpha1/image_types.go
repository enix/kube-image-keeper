package v1alpha1

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cespare/xxhash/v2"
	"github.com/distribution/reference"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

type ImageStatusUpstream string

const (
	ImageStatusUpstreamScheduled         = ImageStatusUpstream("Scheduled")
	ImageStatusUpstreamAvailable         = ImageStatusUpstream("Available")
	ImageStatusUpstreamUnavailable       = ImageStatusUpstream("Unavailable")
	ImageStatusUpstreamUnreachable       = ImageStatusUpstream("Unreachable")
	ImageStatusUpstreamInvalidAuth       = ImageStatusUpstream("InvalidAuth")
	ImageStatusUpstreamUnavailableSecret = ImageStatusUpstream("UnavailableSecret")
	ImageStatusUpstreamQuotaExceeded     = ImageStatusUpstream("QuotaExceeded")
)

var ImageStatusUpstreamList = []ImageStatusUpstream{
	ImageStatusUpstreamScheduled,
	ImageStatusUpstreamAvailable,
	ImageStatusUpstreamUnavailable,
	ImageStatusUpstreamUnreachable,
	ImageStatusUpstreamInvalidAuth,
	ImageStatusUpstreamUnavailableSecret,
	ImageStatusUpstreamQuotaExceeded,
}

type Upstream struct {
	// LastMonitor is the last time a monitoring task for the upstream image was was started
	LastMonitor metav1.Time `json:"lastMonitor,omitempty"`
	// LastSeen is the last time the image was seen upstream
	LastSeen metav1.Time `json:"lastSeen,omitempty"`
	// LastError is the last error encountered while trying to monitor the upstream image
	LastError string `json:"lastError,omitempty"`
	// Status is the status of the last finished monitoring task
	// +kubebuilder:validation:Enum=Scheduled;Available;Unavailable;Unreachable;InvalidAuth;UnavailableSecret;QuotaExceeded
	// +default="Scheduled"
	Status ImageStatusUpstream `json:"status,omitempty"`
	// Digest is the digest of the upstream image manifest, if available
	Digest string `json:"digest,omitempty"`
}

// ImageStatus defines the observed state of Image.
type ImageStatus struct {
	// UsedByPods is the list of pods using this image
	UsedByPods ReferencesWithCount `json:"usedByPods,omitempty"`
	// Upstream is the information about the upstream image
	Upstream Upstream `json:"upstream,omitempty"`
	// UnusedSince is the time when the image was last used by a pod
	UnusedSince metav1.Time `json:"unusedSince,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=img
// +kubebuilder:selectablefield:JSONPath=".spec.registry"
// +kubebuilder:printcolumn:name="Registry",type="string",JSONPath=".spec.registry"
// +kubebuilder:printcolumn:name="Image",type="string",JSONPath=".spec.image"
// +kubebuilder:printcolumn:name="Pods count",type="integer",JSONPath=".status.usedByPods.count"
// +kubebuilder:printcolumn:name="Upstream status",type="string",JSONPath=".status.upstream.status"
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

func (i ImageStatusUpstream) ToString() string {
	value := string(i)
	if value == "" {
		value = "unknown"
	}
	return strings.ToLower(value)
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

func (i *Image) GetPullSecrets(ctx context.Context, c client.Client) (secrets []corev1.Secret, err error) {
	if len(i.Status.UsedByPods.Items) == 0 {
		return nil, errors.New("image has no pods using it")
	}
	podRef := strings.Split(i.Status.UsedByPods.Items[0], "/")
	pod := &corev1.Pod{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: podRef[0], Name: podRef[1]}, pod); err != nil {
		return nil, err
	}

	for _, imagePullSecret := range pod.Spec.ImagePullSecrets {
		secret := &corev1.Secret{}
		if err := c.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: imagePullSecret.Name}, secret); err != nil {
			return nil, fmt.Errorf("could not get image pull secret %q: %w", imagePullSecret.Name, err)
		}
		secrets = append(secrets, *secret)
	}

	return secrets, nil
}

func (i *Image) IsUnused() bool {
	return len(i.Status.UsedByPods.Items) == 0
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

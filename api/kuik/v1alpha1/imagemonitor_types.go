package v1alpha1

import (
	"context"
	"fmt"
	"strings"

	"github.com/cespare/xxhash"
	"github.com/enix/kube-image-keeper/internal/registry"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ImageMonitorSpec defines the desired state of Image.
// +required
type ImageMonitorSpec struct {
	// ImageReference is the reference of the image to monitor
	ImageReference `json:",inline"`
}

type ImageMonitorStatusUpstream string

const (
	ImageMonitorStatusUpstreamScheduled         = ImageMonitorStatusUpstream("Scheduled")
	ImageMonitorStatusUpstreamAvailable         = ImageMonitorStatusUpstream("Available")
	ImageMonitorStatusUpstreamUnavailable       = ImageMonitorStatusUpstream("Unavailable")
	ImageMonitorStatusUpstreamUnreachable       = ImageMonitorStatusUpstream("Unreachable")
	ImageMonitorStatusUpstreamInvalidAuth       = ImageMonitorStatusUpstream("InvalidAuth")
	ImageMonitorStatusUpstreamUnavailableSecret = ImageMonitorStatusUpstream("UnavailableSecret")
	ImageMonitorStatusUpstreamQuotaExceeded     = ImageMonitorStatusUpstream("QuotaExceeded")
)

var ImageMonitorStatusUpstreamList = []ImageMonitorStatusUpstream{
	ImageMonitorStatusUpstreamScheduled,
	ImageMonitorStatusUpstreamAvailable,
	ImageMonitorStatusUpstreamUnavailable,
	ImageMonitorStatusUpstreamUnreachable,
	ImageMonitorStatusUpstreamInvalidAuth,
	ImageMonitorStatusUpstreamUnavailableSecret,
	ImageMonitorStatusUpstreamQuotaExceeded,
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
	Status ImageMonitorStatusUpstream `json:"status,omitempty"`
	// Digest is the digest of the upstream image manifest, if available
	Digest string `json:"digest,omitempty"`
}

// ImageMonitorStatus defines the observed state of Image.
type ImageMonitorStatus struct {
	// Upstream is the information about the upstream image
	Upstream Upstream `json:"upstream,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=imgmon
// +kubebuilder:selectablefield:JSONPath=".spec.registry"
// +kubebuilder:printcolumn:name="Registry",type="string",JSONPath=".spec.registry"
// +kubebuilder:printcolumn:name="Path",type="string",JSONPath=".spec.path"
// +kubebuilder:printcolumn:name="Upstream status",type="string",JSONPath=".status.upstream.status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ImageMonitor is the Schema for the images API.
type ImageMonitor struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ImageMonitorSpec   `json:"spec,omitempty"`
	Status ImageMonitorStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ImageMonitorList contains a list of Image.
type ImageMonitorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ImageMonitor `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ImageMonitor{}, &ImageMonitorList{})
}

func (i ImageMonitorStatusUpstream) ToString() string {
	value := string(i)
	if value == "" {
		value = "unknown"
	}
	return strings.ToLower(value)
}

func (i *ImageMonitor) Reference() string {
	return i.Spec.Reference()
}

func (i *ImageMonitor) GetImage(ctx context.Context, c client.Client, image *Image) error {
	name, err := registry.ImageNameFromReference(i.Reference())
	if err != nil {
		return err
	}

	return c.Get(ctx, client.ObjectKey{Name: name}, image)
}

func (i *ImageMonitor) GetRegistryMonitor(ctx context.Context, c client.Client) (*RegistryMonitor, error) {
	name := fmt.Sprintf("%016x", xxhash.Sum64String(i.Spec.Registry))
	registryMonitor := &RegistryMonitor{}
	return registryMonitor, c.Get(ctx, client.ObjectKey{Name: name}, registryMonitor)
}

func (i *ImageMonitor) GetPullSecrets(ctx context.Context, c client.Client) (secrets []corev1.Secret, err error) {
	image := &Image{}
	if err := i.GetImage(ctx, c, image); err != nil {
		return nil, err
	}

	return image.GetPullSecrets(ctx, c)
}

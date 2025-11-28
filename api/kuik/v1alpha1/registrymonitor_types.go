package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RegistryMonitorSpec defines the desired state of RegistryMonitor.
// +required
type RegistryMonitorSpec struct {
	// Registry is the registry to monitor for image updates, it filters local image to check upstream
	// FIXME: make it immutable
	Registry string `json:"registry"`
	// MaxPerInterval is the maximum number of images to check for the given interval, defaults to 1
	// +kubebuilder:validation:Minimum=1
	// +default:value=1
	MaxPerInterval int `json:"maxPerInterval"`
	// Interval is the interval at which the image monitor checks for updates
	// +default:value="10m"
	Interval metav1.Duration `json:"interval"`
	// Method is the HTTP method to use to monitor an image of this registry
	// +kubebuilder:validation:Enum=HEAD;GET
	// +default:value="HEAD"
	Method string `json:"method"`
	// Timeout is the maximum duration of a monitoring task
	// +default:value="30s"
	Timeout metav1.Duration `json:"timeout"`
}

// RegistryMonitorStatus defines the observed state of RegistryMonitor.
type RegistryMonitorStatus struct {
	Images []ImageMonitorStatus `json:"images,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=regmon
// +kubebuilder:printcolumn:name="Registry",type="string",JSONPath=".spec.registry"
// +kubebuilder:printcolumn:name="Parallel",type="integer",JSONPath=".spec.parallel"
// +kubebuilder:printcolumn:name="MaxPerInterval",type="integer",JSONPath=".spec.maxPerInterval"
// +kubebuilder:printcolumn:name="Interval",type="string",JSONPath=".spec.interval"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// RegistryMonitor is the Schema for the registrymonitors API.
type RegistryMonitor struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RegistryMonitorSpec   `json:"spec,omitempty"`
	Status RegistryMonitorStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RegistryMonitorList contains a list of RegistryMonitor.
type RegistryMonitorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RegistryMonitor `json:"items"`
}

type ImageMonitorStatusName string

const (
	ImageMonitorStatusUpstreamScheduled         = ImageMonitorStatusName("Scheduled")
	ImageMonitorStatusUpstreamAvailable         = ImageMonitorStatusName("Available")
	ImageMonitorStatusUpstreamUnavailable       = ImageMonitorStatusName("Unavailable")
	ImageMonitorStatusUpstreamUnreachable       = ImageMonitorStatusName("Unreachable")
	ImageMonitorStatusUpstreamInvalidAuth       = ImageMonitorStatusName("InvalidAuth")
	ImageMonitorStatusUpstreamUnavailableSecret = ImageMonitorStatusName("UnavailableSecret")
	ImageMonitorStatusUpstreamQuotaExceeded     = ImageMonitorStatusName("QuotaExceeded")
)

var ImageMonitorStatusUpstreamList = []ImageMonitorStatusName{
	ImageMonitorStatusUpstreamScheduled,
	ImageMonitorStatusUpstreamAvailable,
	ImageMonitorStatusUpstreamUnavailable,
	ImageMonitorStatusUpstreamUnreachable,
	ImageMonitorStatusUpstreamInvalidAuth,
	ImageMonitorStatusUpstreamUnavailableSecret,
	ImageMonitorStatusUpstreamQuotaExceeded,
}

type ImageMonitorStatus struct {
	// Path is the path to the monitored image (does not include the registry)
	Path string `json:"path,omitempty"`
	// Status is the status of the last finished monitoring task
	// +kubebuilder:validation:Enum=Scheduled;Available;Unavailable;Unreachable;InvalidAuth;UnavailableSecret;QuotaExceeded
	// +default="Scheduled"
	Status ImageMonitorStatusName `json:"status,omitempty"`
	// Digest is the digest of the upstream image manifest, if available
	Digest string `json:"digest,omitempty"`
	// LastMonitor is the last time a monitoring task for the upstream image was was started
	LastMonitor metav1.Time `json:"lastMonitor,omitempty"`
	// LastError is the last error encountered while trying to monitor the upstream image
	LastError string `json:"lastError,omitempty"`
	// LastSeen is the last time the image was seen upstream
	LastSeen metav1.Time `json:"lastSeen,omitempty"`
	// UnusedSince is the last time the image was used in a pod
	UnusedSince metav1.Time `json:"unusedSince,omitempty"`
}

func init() {
	SchemeBuilder.Register(&RegistryMonitor{}, &RegistryMonitorList{})
}

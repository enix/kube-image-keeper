package v1alpha1

import (
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RegistryMonitorSpec defines the desired state of RegistryMonitor.
// +required
type RegistryMonitorSpec struct {
	// Registry is the registry to monitor for image updates, it filters local image to check upstream
	Registry string `json:"registry"`
	// Parallel is the number of images to check in parallel, defaults to 1
	// +kubebuilder:validation:Minimum=1
	// +default:value=1
	Parallel int `json:"parallel"`
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
}

type RegistryStatus string

const (
	RegistryStatusUp   = RegistryStatus("Up")
	RegistryStatusDown = RegistryStatus("Down")
)

// RegistryMonitorStatus defines the observed state of RegistryMonitor.
type RegistryMonitorStatus struct{}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=regmon
// +kubebuilder:printcolumn:name="Registry",type="string",JSONPath=".spec.registry"
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.registryStatus"
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

func init() {
	SchemeBuilder.Register(&RegistryMonitor{}, &RegistryMonitorList{})
}

func (r RegistryStatus) ToString() string {
	value := string(r)
	if value == "" {
		value = "unknown"
	}
	return strings.ToLower(value)
}

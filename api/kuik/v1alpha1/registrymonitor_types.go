package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RegistryMonitorSpec defines the desired state of RegistryMonitor.
// +required
type RegistryMonitorSpec struct {
	// Registry is the registry to monitor for image updates, it filters local image to check upstream
	Registry string `json:"registry"`
	// Interval is the interval at which the image monitor checks for updates
	Interval metav1.Duration `json:"interval"`
	// Burst is the number of images to check in parallel, defaults to 1
	// +kubebuilder:validation:Minimum=1
	// +default:value=1
	Burst int `json:"burst,omitempty"`
}

// RegistryMonitorStatus defines the observed state of RegistryMonitor.
type RegistryMonitorStatus struct {
	// LastExecution is the last time the image monitor checked for updates
	LastExecution metav1.Time `json:"LastExecution,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=regmon
// +kubebuilder:printcolumn:name="Registry",type="string",JSONPath=".spec.registry"
// +kubebuilder:printcolumn:name="Interval",type="string",JSONPath=".spec.interval"
// +kubebuilder:printcolumn:name="Burst",type="integer",JSONPath=".spec.burst"
// +kubebuilder:printcolumn:name="Last Execution",type="date",JSONPath=".status.lastExecution"

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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ImageMonitorSpec defines the desired state of ImageMonitor.
// +required
type ImageMonitorSpec struct {
	// Registry is the registry to monitor for image updates, it filters local image to check upstream
	Registry string `json:"registry"`
	// Interval is the interval at which the image monitor checks for updates
	Interval metav1.Duration `json:"interval"`
	// Burst is the number of images to check in parallel, defaults to 1
	// +kubebuilder:validation:Minimum=1
	// +default:value=1
	Burst int `json:"burst,omitempty"`
}

// ImageMonitorStatus defines the observed state of ImageMonitor.
type ImageMonitorStatus struct {
	// LastExecution is the last time the image monitor checked for updates
	LastExecution metav1.Time `json:"LastExecution,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=imgmon
// +kubebuilder:printcolumn:name="Registry",type="string",JSONPath=".spec.registry"
// +kubebuilder:printcolumn:name="Interval",type="string",JSONPath=".spec.interval"
// +kubebuilder:printcolumn:name="Burst",type="integer",JSONPath=".spec.burst"
// +kubebuilder:printcolumn:name="Last Execution",type="date",JSONPath=".status.lastExecution"

// ImageMonitor is the Schema for the imagemonitors API.
type ImageMonitor struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ImageMonitorSpec   `json:"spec,omitempty"`
	Status ImageMonitorStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ImageMonitorList contains a list of ImageMonitor.
type ImageMonitorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ImageMonitor `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ImageMonitor{}, &ImageMonitorList{})
}

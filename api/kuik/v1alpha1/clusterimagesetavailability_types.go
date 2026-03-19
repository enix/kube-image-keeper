package v1alpha1

import (
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ImageAvailabilityStatus represents the result of an image availability check.
// +kubebuilder:validation:Enum=Scheduled;Available;NotFound;Unreachable;InvalidAuth;UnavailableSecret;QuotaExceeded
type ImageAvailabilityStatus string

const (
	ImageAvailabilityScheduled         ImageAvailabilityStatus = "Scheduled"
	ImageAvailabilityAvailable         ImageAvailabilityStatus = "Available"
	ImageAvailabilityNotFound          ImageAvailabilityStatus = "NotFound"
	ImageAvailabilityUnreachable       ImageAvailabilityStatus = "Unreachable"
	ImageAvailabilityInvalidAuth       ImageAvailabilityStatus = "InvalidAuth"
	ImageAvailabilityUnavailableSecret ImageAvailabilityStatus = "UnavailableSecret"
	ImageAvailabilityQuotaExceeded     ImageAvailabilityStatus = "QuotaExceeded"
)

// ClusterImageSetAvailabilitySpec defines the desired monitoring configuration.
type ClusterImageSetAvailabilitySpec struct {
	// UnusedImageExpiry is how long to keep tracking an image after no Pod uses it.
	// Once elapsed the image is removed from status. Example: "720h" (30 days).
	// Zero means unused images are never removed.
	// +optional
	UnusedImageExpiry metav1.Duration `json:"unusedImageExpiry,omitempty"`

	// ImageFilter selects which images to monitor.
	// +optional
	ImageFilter ImageFilterDefinition `json:"imageFilter,omitempty"`
}

// MonitoredImage holds the current availability state for a single image.
type MonitoredImage struct {
	// Image is the full normalised image reference, e.g. "docker.io/library/nginx:1.27".
	Image string `json:"image"`

	// Status is the result of the last availability check.
	// +default="Scheduled"
	Status ImageAvailabilityStatus `json:"status"`

	// UnusedSince is the timestamp when the last Pod referencing this image disappeared.
	// Nil means at least one Pod currently uses this image.
	// +optional
	UnusedSince *metav1.Time `json:"unusedSince,omitempty"`

	// LastError contains the error message from the last failed check, if any.
	// +optional
	LastError string `json:"lastError,omitempty"`

	// LastMonitor is the timestamp of the last availability check.
	// Nil means the image has not been checked yet.
	// +optional
	LastMonitor *metav1.Time `json:"lastMonitor,omitempty"`
}

// ClusterImageSetAvailabilityStatus defines the observed state.
type ClusterImageSetAvailabilityStatus struct {
	// ImageCount is the total number of images currently being tracked.
	ImageCount int `json:"imageCount,omitempty"`

	// +listType=map
	// +listMapKey=image
	Images []MonitoredImage `json:"images,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=cisa
// +kubebuilder:printcolumn:name="Images",type=integer,JSONPath=".status.imageCount",description="Total number of monitored images"

// ClusterImageSetAvailability is the Schema for the clusterimagesetavailabilities API.
type ClusterImageSetAvailability struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterImageSetAvailabilitySpec   `json:"spec,omitempty"`
	Status ClusterImageSetAvailabilityStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterImageSetAvailabilityList contains a list of ClusterImageSetAvailability.
type ClusterImageSetAvailabilityList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterImageSetAvailability `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterImageSetAvailability{}, &ClusterImageSetAvailabilityList{})
}

func (i ImageAvailabilityStatus) ToString() string {
	value := string(i)
	if value == "" {
		value = "unknown"
	}
	return strings.ToLower(value)
}

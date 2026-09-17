package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// ImageMonitorSpec defines the desired state of ImageMonitor.
type ImageMonitorSpec struct {
	// podSelector restricts the pods this resource applies to. Empty or absent matches every
	// pod.
	// +optional
	PodSelector *metav1.LabelSelector `json:"podSelector,omitempty"`

	// namespaceSelector restricts the namespaces this resource applies to. Empty or absent
	// matches every namespace.
	// +optional
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// unusedImageRetention keeps monitoring an image for this long after no pod declares it
	// any more, so that a CronJob's image stays checked between runs.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+)$`
	// +kubebuilder:default="168h"
	// +optional
	UnusedImageRetention *metav1.Duration `json:"unusedImageRetention,omitempty"`

	// driftDetection reports the tracked tags whose upstream digest differs from the one
	// running in the cluster.
	// +kubebuilder:default=true
	// +optional
	DriftDetection *bool `json:"driftDetection,omitempty"`

	// monitorAlternatives also monitors the alternatives kuik would offer for each tracked
	// image, from ImageAlternative entries only: a mirror destination is verified by the
	// mirror's own self-check, and entries marked unavailable are never tracked.
	// +kubebuilder:default=false
	// +optional
	MonitorAlternatives *bool `json:"monitorAlternatives,omitempty"`
}

// DetectsDrift applies the default: drift detection is on unless set to false.
func (s *ImageMonitorSpec) DetectsDrift() bool {
	return s.DriftDetection == nil || *s.DriftDetection
}

// MonitorsAlternatives applies the default: alternatives are not monitored unless set to true.
func (s *ImageMonitorSpec) MonitorsAlternatives() bool {
	return s.MonitorAlternatives != nil && *s.MonitorAlternatives
}

// ImageCounts are gauges on one population of references an ImageMonitor tracks.
type ImageCounts struct {
	// tracked is running + standby + retained.
	Tracked int32 `json:"tracked"`

	// running counts the references a container carries.
	Running int32 `json:"running"`

	// standby counts the references a pod still declares but kuik routed elsewhere, for an
	// origin, or the checked candidates never served, for an alternative.
	Standby int32 `json:"standby"`

	// retained counts the references no pod declares any more, kept for
	// unusedImageRetention.
	Retained int32 `json:"retained"`

	// available counts the references whose last check succeeded. available + unavailable is
	// short of tracked by the references the ring has not reached yet.
	Available int32 `json:"available"`

	// unavailable counts the references whose last check failed.
	Unavailable int32 `json:"unavailable"`
}

// OriginImageCounts are the gauges of the origin population, which alone can drift: an
// alternative is checked, never compared to what runs.
type OriginImageCounts struct {
	ImageCounts `json:",inline"`

	// drifted counts the tags whose running digest differs from the upstream one, under
	// driftDetection.
	Drifted int32 `json:"drifted"`
}

// MonitorImages groups the two populations an ImageMonitor tracks.
type MonitorImages struct {
	// origin are the origin references declared by the selected pods.
	// +optional
	Origin *OriginImageCounts `json:"origin,omitempty"`

	// alternatives are the candidates ImageAlternative entries would offer for them, under
	// monitorAlternatives.
	// +optional
	Alternatives *ImageCounts `json:"alternatives,omitempty"`
}

// RetainedImage is a reference no pod declares any more, kept for unusedImageRetention.
type RetainedImage struct {
	// ref is the origin reference.
	Ref string `json:"ref"`

	// unusedSince is when its last pod went away.
	UnusedSince metav1.Time `json:"unusedSince"`

	// digest is the digest that was running.
	// +optional
	Digest string `json:"digest,omitempty"`
}

// UnavailableImage is a tracked origin whose last check failed.
type UnavailableImage struct {
	// ref is the origin reference.
	Ref string `json:"ref"`

	// reason is what the check observed.
	Reason CheckFailureReason `json:"reason"`

	// since is when the failure was first observed. Stamped once, never refreshed.
	Since metav1.Time `json:"since"`

	// pods counts the pods declaring it.
	Pods int32 `json:"pods"`
}

// UnavailableAlternative is a monitored alternative whose last check failed.
type UnavailableAlternative struct {
	// ref is the alternative reference.
	Ref string `json:"ref"`

	// derivedFrom is the origin it stands for.
	DerivedFrom string `json:"derivedFrom"`

	// via names the ImageAlternative the entry came from, as `ImageAlternative/<name>`.
	Via string `json:"via"`

	// reason is what the check observed.
	Reason CheckFailureReason `json:"reason"`

	// since is when the failure was first observed. Stamped once, never refreshed.
	Since metav1.Time `json:"since"`
}

// RunningDigest is one digest running for a tag, with how many pods reference it.
type RunningDigest struct {
	// digest running in the cluster.
	Digest string `json:"digest"`

	// pods counts the pods running it.
	Pods int32 `json:"pods"`
}

// MonitorDriftedImage is a tracked tag whose running digest differs from the upstream one.
type MonitorDriftedImage struct {
	// ref is the origin reference.
	Ref string `json:"ref"`

	// upstreamDigest is the digest the upstream tag points at now.
	UpstreamDigest string `json:"upstreamDigest"`

	// since is when the drift was first observed. Stamped once, never refreshed.
	Since metav1.Time `json:"since"`

	// runningDigests lists every digest currently running for the tag: pods pulled at
	// different times may run different ones.
	// +listType=map
	// +listMapKey=digest
	RunningDigests []RunningDigest `json:"runningDigests"`
}

// ImageMonitorStatus defines the observed state of ImageMonitor.
type ImageMonitorStatus struct {
	// images are gauges on the two populations this monitor tracks.
	// +optional
	Images *MonitorImages `json:"images,omitempty"`

	// retainedImages lists the references kept for unusedImageRetention. Not capped: they
	// cannot be recomputed from the cluster.
	// +optional
	// +listType=atomic
	RetainedImages []RetainedImage `json:"retainedImages,omitempty"`

	// unavailableImages lists the tracked origins whose last check failed. Capped, see
	// ListCapacityPressure.
	// +optional
	// +listType=atomic
	UnavailableImages []UnavailableImage `json:"unavailableImages,omitempty"`

	// unavailableAlternatives lists the monitored alternatives whose last check failed.
	// Capped, see ListCapacityPressure.
	// +optional
	// +listType=atomic
	UnavailableAlternatives []UnavailableAlternative `json:"unavailableAlternatives,omitempty"`

	// driftedImages lists the tracked tags whose running digest differs from the upstream
	// one. Capped, see ListCapacityPressure.
	// +optional
	// +listType=atomic
	DriftedImages []MonitorDriftedImage `json:"driftedImages,omitempty"`

	// checks is the health of the check schedule, one ring per registry host.
	// +optional
	Checks *ChecksStatus `json:"checks,omitempty"`

	// truncated records, per capped list that reached its cap, how many entries were left
	// out. Present only once a cap was reached.
	// +optional
	Truncated map[string]int32 `json:"truncated,omitempty"`

	// conditions report the state of the resource. Ready is the only one that is True when
	// things are well; ImagesUnavailable, AlternativesUnavailable, ImagesDrifted and
	// ListCapacityPressure each name an anomaly and stay True as long as it lasts.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Tracked",type=integer,JSONPath=`.status.images.origin.tracked`
// +kubebuilder:printcolumn:name="Unavailable",type=integer,JSONPath=`.status.images.origin.unavailable`
// +kubebuilder:printcolumn:name="Drifted",type=integer,JSONPath=`.status.images.origin.drifted`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ImageMonitor checks the availability of the images of the pods it selects, and reports the
// ones that fail or drift.
type ImageMonitor struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ImageMonitor
	// +required
	Spec ImageMonitorSpec `json:"spec"`

	// status defines the observed state of ImageMonitor
	// +optional
	Status ImageMonitorStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ImageMonitorList contains a list of ImageMonitor
type ImageMonitorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ImageMonitor `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &ImageMonitor{}, &ImageMonitorList{})
		return nil
	})
}

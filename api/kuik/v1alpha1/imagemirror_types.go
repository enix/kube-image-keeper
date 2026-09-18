package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// DriftPolicy says what an ImageMirror does when the upstream digest of a copied tag moves.
// +kubebuilder:validation:Enum=Ignore;Warn;Sync
type DriftPolicy string

const (
	// DriftPolicyIgnore copies a tag once and never re-reads the upstream.
	DriftPolicyIgnore DriftPolicy = "Ignore"
	// DriftPolicyWarn periodically re-reads the upstream tag and reports a moved digest.
	DriftPolicyWarn DriftPolicy = "Warn"
	// DriftPolicySync periodically re-reads the upstream tag and copies it again when its
	// digest moved.
	DriftPolicySync DriftPolicy = "Sync"
)

// DestinationCredentials wraps the auth of one destination role.
type DestinationCredentials struct {
	// auth is the credential, see Auth.
	// +required
	Auth Auth `json:"auth"`
}

// MirrorDestination is the registry path an ImageMirror copies images under.
type MirrorDestination struct {
	// path is the registry path the full original reference is appended to, hostname
	// included: `registry.tld/mirror/` turns `docker.io/library/nginx:1.27` into
	// `registry.tld/mirror/docker.io/library/nginx:1.27_<clusterID>`.
	// +kubebuilder:validation:Pattern=`^(localhost(:[0-9]+)?|[a-zA-Z0-9-]+(\.[a-zA-Z0-9-]+)+(:[0-9]+)?|[a-zA-Z0-9-]+:[0-9]+)(/[a-z0-9]+((\.|_|__|-+)[a-z0-9]+)*)*/?$`
	// +required
	Path string `json:"path"`

	// insecure makes kuik push to and read this registry over HTTP. The container runtime of
	// the nodes must be configured for it separately.
	// +optional
	Insecure bool `json:"insecure,omitempty"`

	// manage is the controller's credential at the destination: the pushes, the self-check
	// reads, the tag listings and the deletions. Its injectPullSecret is ignored.
	// +optional
	Manage *DestinationCredentials `json:"manage,omitempty"`

	// pull is the credential the kubelet pulls the mirrored images with, injected in the
	// namespaces that need it.
	// +optional
	Pull *DestinationCredentials `json:"pull,omitempty"`
}

// Cleanup configures the deletion of destination tags no pod references any more.
type Cleanup struct {
	// enabled deletes the tags no longer referenced by any pod once their retention elapsed.
	// With false nothing is deleted and retention has no effect.
	// +kubebuilder:default=true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// retention is how long an unused tag is held before deletion, so that a CronJob's image
	// survives between runs.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+)$`
	// +kubebuilder:default="168h"
	// +optional
	Retention *metav1.Duration `json:"retention,omitempty"`
}

// IsEnabled applies the default: cleanup is enabled unless set to false.
func (c *Cleanup) IsEnabled() bool {
	return c == nil || c.Enabled == nil || *c.Enabled
}

// ImageMirrorSpec defines the desired state of ImageMirror.
type ImageMirrorSpec struct {
	// podSelector restricts the pods this resource applies to. Empty or absent matches every
	// pod.
	// +optional
	PodSelector *metav1.LabelSelector `json:"podSelector,omitempty"`

	// namespaceSelector restricts the namespaces this resource applies to. Empty or absent
	// matches every namespace.
	// +optional
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// excludeImages keeps the images matching any of these globs out of the mirror: no copy,
	// no candidate, no tag at the destination. A glob is matched whole against the normalized
	// reference, `*` matches inside one path segment and `**` across segments; a `:tag` part
	// narrows it to matching tags. The mirror's own destination.path is excluded on top.
	// +listType=atomic
	// +optional
	ExcludeImages []string `json:"excludeImages,omitempty"`

	// rewritePolicy says where the mirrored image sits relative to the original: OnFailure
	// puts it last, behind the original and every alternative, Always puts it ahead of
	// everything, None never routes to it and only copies.
	// +kubebuilder:validation:Enum=OnFailure;Always;None
	// +kubebuilder:default=OnFailure
	// +optional
	RewritePolicy RewritePolicy `json:"rewritePolicy,omitempty"`

	// destination is where images are copied.
	// +required
	Destination MirrorDestination `json:"destination"`

	// cleanup deletes the destination tags no pod references any more.
	// +kubebuilder:default={}
	// +optional
	Cleanup *Cleanup `json:"cleanup,omitempty"`

	// driftPolicy says what to do when the upstream digest of a copied tag moves: Ignore it,
	// Warn about it, or Sync the copy.
	// +kubebuilder:default=Ignore
	// +optional
	DriftPolicy DriftPolicy `json:"driftPolicy,omitempty"`
}

// CopyCounts are gauges on the references an ImageMirror holds at its destination.
type CopyCounts struct {
	// tracked is running + standby + retained.
	Tracked int32 `json:"tracked"`

	// running counts the references a container carries.
	Running int32 `json:"running"`

	// standby counts the references copied and held that no container carries.
	Standby int32 `json:"standby"`

	// retained counts the references whose origin no pod declares any more, held for
	// cleanup.retention.
	Retained int32 `json:"retained"`

	// available counts the references held by the destination, as of the last self-check.
	Available int32 `json:"available"`

	// unavailable counts the references not copied yet, or whose copy is failing.
	Unavailable int32 `json:"unavailable"`

	// drifted counts the tags whose upstream digest moved away from the copy, under Warn or
	// Sync.
	Drifted int32 `json:"drifted"`

	// missingSource counts the references no source can supply any more: the
	// failedImageCopies entries with reason SourceNotFound.
	MissingSource int32 `json:"missingSource"`

	// orphanTags counts the destination tags the sweep found that no tracked reference
	// accounts for: the pendingDeletion entries carrying no origin.
	OrphanTags int32 `json:"orphanTags"`
}

// MirrorImages groups the image gauges of an ImageMirror.
type MirrorImages struct {
	// copy are the references this mirror holds at its destination.
	// +optional
	Copy *CopyCounts `json:"copy,omitempty"`
}

// MirrorDriftedImage is a copied tag whose upstream digest moved away from the copy.
type MirrorDriftedImage struct {
	// ref is the origin reference.
	Ref string `json:"ref"`

	// upstreamDigest is the digest the upstream tag points at now.
	UpstreamDigest string `json:"upstreamDigest"`

	// copiedDigest is the digest held at the destination.
	CopiedDigest string `json:"copiedDigest"`

	// since is when the drift was first observed. Stamped once, never refreshed.
	Since metav1.Time `json:"since"`
}

// FailedImageCopy is an origin reference whose copy has not succeeded.
type FailedImageCopy struct {
	// ref is the origin reference.
	Ref string `json:"ref"`

	// reason is what the last attempt observed.
	Reason CopyFailureReason `json:"reason"`

	// since is the first failure. Stamped once, never refreshed.
	Since metav1.Time `json:"since"`

	// lastAttempt is refreshed at every retry.
	// +optional
	LastAttempt *metav1.Time `json:"lastAttempt,omitempty"`
}

// PendingDeletion is a destination tag no pod references any more, held for cleanup.retention.
type PendingDeletion struct {
	// ref is the destination reference, tag included.
	Ref string `json:"ref"`

	// origin is the reference the image was copied from. Known when a pod event created the
	// entry, absent for a tag found by listing the destination.
	// +optional
	Origin string `json:"origin,omitempty"`

	// unusedSince is when the entry appeared. Never refreshed afterwards.
	UnusedSince metav1.Time `json:"unusedSince"`
}

// ImageMirrorStatus defines the observed state of ImageMirror: the copy side first, then the
// routing side, which is field for field an ImageAlternative's.
type ImageMirrorStatus struct {
	// images are gauges on the references this mirror holds at its destination.
	// +optional
	Images *MirrorImages `json:"images,omitempty"`

	// driftedImages lists the tags whose upstream digest moved away from the copy, under Warn
	// or Sync. Capped, see ListCapacityPressure.
	// +optional
	// +listType=atomic
	DriftedImages []MirrorDriftedImage `json:"driftedImages,omitempty"`

	// failedImageCopies lists the origin references whose copy has not succeeded. Capped, see
	// ListCapacityPressure.
	// +optional
	// +listType=atomic
	FailedImageCopies []FailedImageCopy `json:"failedImageCopies,omitempty"`

	// pendingDeletion lists the destination tags waiting out cleanup.retention. Not capped:
	// a missing entry would be a tag never deleted.
	// +optional
	// +listType=atomic
	PendingDeletion []PendingDeletion `json:"pendingDeletion,omitempty"`

	// selfChecked is the end of the last full comparison against the destination.
	// +optional
	SelfChecked *metav1.Time `json:"selfChecked,omitempty"`

	// repositories lists the destination repositories this mirror wrote to, for the cleanup
	// sweep. Written before the first push, removed when no tag of this cluster remains. Not
	// capped: a missing entry would be a repository the sweep never visits again.
	// +optional
	// +listType=set
	Repositories []string `json:"repositories,omitempty"`

	// checks is the health of the drift check schedule, one ring per source host. Present
	// under Warn or Sync only; the destination never appears here.
	// +optional
	Checks *ChecksStatus `json:"checks,omitempty"`

	RoutingStatus `json:",inline"`

	// truncated records, per capped list that reached its cap, how many entries were left
	// out. Present only once a cap was reached.
	// +optional
	Truncated map[string]int32 `json:"truncated,omitempty"`

	// conditions report the state of the resource. Ready is the only one that is True when
	// things are well; DestinationOutOfSync, FallbackActive, AlternativesExhausted and
	// ListCapacityPressure each name an anomaly and stay True as long as it lasts.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Destination",type=string,JSONPath=`.spec.destination.path`
// +kubebuilder:printcolumn:name="Policy",type=string,JSONPath=`.spec.rewritePolicy`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="OutOfSync",type=string,JSONPath=`.status.conditions[?(@.type=="DestinationOutOfSync")].status`
// +kubebuilder:printcolumn:name="Tracked",type=integer,JSONPath=`.status.images.copy.tracked`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ImageMirror copies the images of the pods it selects to a destination registry, and routes
// to that copy according to its rewritePolicy.
type ImageMirror struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ImageMirror
	// +required
	Spec ImageMirrorSpec `json:"spec"`

	// status defines the observed state of ImageMirror
	// +optional
	Status ImageMirrorStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ImageMirrorList contains a list of ImageMirror
type ImageMirrorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ImageMirror `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &ImageMirror{}, &ImageMirrorList{})
		return nil
	})
}

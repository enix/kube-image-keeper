package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var RepositoryLabelName = "kuik.enix.io/repository"

// CachedImageSpec defines the desired state of CachedImage
type CachedImageSpec struct {
	// SourceImage is the path of the image to cache
	SourceImage string `json:"sourceImage"`
	// ExpiresAt is the time when the image should be deleted from cache if not in use (unset when the image is used again)
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`
	// Retain defines if the image should be retained in cache even when not used (will prevent ExpiresAt to be populated)
	// +optional
	Retain bool `json:"retain,omitempty"`
}

type PodReference struct {
	// NamespacedName is the namespaced name of a pod (namespace/name)
	NamespacedName string `json:"namespacedName,omitempty"`
}

type UsedBy struct {
	// Pods is a list of reference to pods using this CachedImage
	Pods []PodReference `json:"pods,omitempty" patchStrategy:"merge" patchMergeKey:"namespacedName"`
	// Count is the number of pods using this image
	//
	// jsonpath function .length() is not implemented, so the count field is required to display pods count in additionalPrinterColumns
	// see https://github.com/kubernetes-sigs/controller-tools/issues/447
	Count int `json:"count,omitempty"`
}

// CachedImageStatus defines the observed state of CachedImage
type CachedImageStatus struct {
	// IsCached indicate whether the image is already cached or not
	IsCached bool `json:"isCached,omitempty"`
	// Phase is the current phase of the image
	Phase string `json:"phase,omitempty"`
	// UsedBy is the list of pods using this image
	UsedBy UsedBy `json:"usedBy,omitempty"`

	// Digest is the digest of the cached image
	Digest string `json:"digest,omitempty"`
	// UpstreamDigest is the upstream image digest
	UpstreamDigest string `json:"upstreamDigest,omitempty"`
	// UpToDate indicate whether if the cached image is up to date with the upstream one or not
	UpToDate bool `json:"upToDate,omitempty"`
	// LastSync is the last time the remote image digest has been checked
	LastSync metav1.Time `json:"lastSync,omitempty"`
	// LastSuccessfulPull is the last time the upstream image has been successfully cached
	LastSuccessfulPull metav1.Time `json:"lastSuccessfulPull,omitempty"`

	// AvailableUpstream indicate whether if the referenced image is available upstream or not
	AvailableUpstream bool `json:"availableUpstream,omitempty"`
	// LastSeenUpstream is the last time the referenced image has been seen upstream
	LastSeenUpstream metav1.Time `json:"lastSeenUpstream,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Cluster,shortName=ci
//+kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.phase"
//+kubebuilder:printcolumn:name="Cached",type="boolean",JSONPath=".status.isCached"
//+kubebuilder:printcolumn:name="Retain",type="boolean",JSONPath=".spec.retain"
//+kubebuilder:printcolumn:name="Expires at",type="string",JSONPath=".spec.expiresAt"
//+kubebuilder:printcolumn:name="Pods count",type="integer",JSONPath=".status.usedBy.count"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// CachedImage is the Schema for the cachedimages API
type CachedImage struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CachedImageSpec   `json:"spec,omitempty"`
	Status CachedImageStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// CachedImageList contains a list of CachedImage
type CachedImageList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CachedImage `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CachedImage{}, &CachedImageList{})
}

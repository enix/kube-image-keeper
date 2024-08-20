package v1alpha1ext1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var RepositoryLabelName = "kuik.enix.io/repository"

// CachedImageSpec defines the desired state of CachedImage
type CachedImageSpec struct {
	SourceImage string `json:"sourceImage"`
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`
	// +optional
	Retain bool `json:"retain,omitempty"`
}

type PodReference struct {
	NamespacedName string `json:"namespacedName,omitempty"`
}

type UsedBy struct {
	Pods []PodReference `json:"pods,omitempty" patchStrategy:"merge" patchMergeKey:"namespacedName"`
	// jsonpath function .length() is not implemented, so the count field is required to display pods count in additionalPrinterColumns
	// see https://github.com/kubernetes-sigs/controller-tools/issues/447
	Count int `json:"count,omitempty"`
}

type Progress struct {
	Total     int64 `json:"total,omitempty"`
	Available int64 `json:"available,omitempty"`
}

// CachedImageStatus defines the observed state of CachedImage
type CachedImageStatus struct {
	IsCached bool   `json:"isCached,omitempty"`
	Phase    string `json:"phase,omitempty"`
	UsedBy   UsedBy `json:"usedBy,omitempty"`

	Progress Progress `json:"progress,omitempty"`

	Digest             string      `json:"digest,omitempty"`
	UpstreamDigest     string      `json:"upstreamDigest,omitempty"`
	UpToDate           bool        `json:"upToDate,omitempty"`
	LastSync           metav1.Time `json:"lastSync,omitempty"`
	LastSuccessfulPull metav1.Time `json:"lastSuccessfulPull,omitempty"`

	AvailableUpstream bool        `json:"availableUpstream,omitempty"`
	LastSeenUpstream  metav1.Time `json:"lastSeenUpstream,omitempty"`
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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var RepositoryLabelName = "kuik.enix.io/repository"

// CachedImageSpec defines the desired state of CachedImage
type CachedImageSpec struct {
	SourceImage string `json:"sourceImage"`
	// +optional
	ExpiresAt            *metav1.Time `json:"expiresAt,omitempty"`
	PullSecretNames      []string     `json:"pullSecretNames,omitempty"`
	PullSecretsNamespace string       `json:"pullSecretsNamespace,omitempty"`
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

// CachedImageStatus defines the observed state of CachedImage
type CachedImageStatus struct {
	IsCached bool   `json:"isCached,omitempty"`
	UsedBy   UsedBy `json:"usedBy,omitempty" `
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Cluster,shortName=ci
//+kubebuilder:printcolumn:name="Cached",type="boolean",JSONPath=".status.isCached"
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

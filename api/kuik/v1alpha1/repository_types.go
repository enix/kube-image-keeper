package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RepositorySpec defines the desired state of Repository
type RepositorySpec struct {
	// Name is the path of the repository (for instance enix/kube-image-keeper)
	Name string `json:"name"`
	// PullSecretNames is the names of pull secret to use to pull CachedImages of this Repository
	PullSecretNames []string `json:"pullSecretNames,omitempty"`
	// PullSecretsNamespace is the namespace where pull secrets can be found for CachedImages of this Repository
	PullSecretsNamespace string `json:"pullSecretsNamespace,omitempty"`
	// UpdateInterval is the interval in human readable format (1m, 1h, 1d...) at which matched CachedImages from this Repository are updated (see spec.UpdateFilters)
	UpdateInterval *metav1.Duration `json:"updateInterval,omitempty"`
	// UpdateFilters is a list of regexps that need to match (at least one of them) the .spec.SourceImage of a CachedImage from this Repository to update it at regular interval
	UpdateFilters []string `json:"updateFilters,omitempty"`
}

// RepositoryStatus defines the observed state of Repository
type RepositoryStatus struct {
	// Images is the count of CachedImages that come from this repository
	Images int `json:"images,omitempty"`
	// Phase is the current phase of this repository
	Phase string `json:"phase,omitempty"`
	// LastUpdate is the last time images of this repository has been updated
	LastUpdate metav1.Time `json:"lastUpdate,omitempty"`
	//+listType=map
	//+listMapKey=type
	//+patchStrategy=merge
	//+patchMergeKey=type
	//+optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Cluster,shortName=repo
//+kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.phase"
//+kubebuilder:printcolumn:name="Images",type="string",JSONPath=".status.images"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// Repository is the Schema for the repositories API
type Repository struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RepositorySpec   `json:"spec,omitempty"`
	Status RepositoryStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// RepositoryList contains a list of Repository
type RepositoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Repository `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Repository{}, &RepositoryList{})
}

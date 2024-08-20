package v1alpha1ext1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RepositorySpec defines the desired state of Repository
type RepositorySpec struct {
	Name                 string           `json:"name"`
	PullSecretNames      []string         `json:"pullSecretNames,omitempty"`
	PullSecretsNamespace string           `json:"pullSecretsNamespace,omitempty"`
	UpdateInterval       *metav1.Duration `json:"updateInterval,omitempty"`
	UpdateFilters        []string         `json:"updateFilters,omitempty"`
}

// RepositoryStatus defines the observed state of Repository
type RepositoryStatus struct {
	Images     int         `json:"images,omitempty"`
	Phase      string      `json:"phase,omitempty"`
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

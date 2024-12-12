package v1alpha1

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type DurationOverride struct {
	time.Duration `json:"-" protobuf:"varint,1,opt,name=duration,casttype=time.Duration"`
}

// This re-implements metav1.Duration' UnmarshalJSON and adds support for a 'd' (days) suffix
func (d *DurationOverride) UnmarshalJSON(b []byte) error {
	var str string

	err := json.Unmarshal(b, &str)
	if err != nil {
		return err
	}

	uidx := len(str) - 1 // last char in str
	if uidx < 1 {
		return errors.New(fmt.Sprintf("Can't parse interval: %s", str))
	}

	if string(str[uidx]) == "d" {
		days, err := strconv.ParseInt(string(str[:uidx]), 10, 64)
		if err != nil {
			return err
		}
		d.Duration = time.Duration(days) * 24 * time.Hour
		return nil
	}

	pd, err := time.ParseDuration(str)
	if err != nil {
		return err
	}
	d.Duration = pd
	return nil
}

// RepositorySpec defines the desired state of Repository
type RepositorySpec struct {
	// Name is the path of the repository (for instance enix/kube-image-keeper)
	Name string `json:"name"`
	// PullSecretNames is the names of pull secret to use to pull CachedImages of this Repository
	PullSecretNames []string `json:"pullSecretNames,omitempty"`
	// PullSecretsNamespace is the namespace where pull secrets can be found for CachedImages of this Repository
	PullSecretsNamespace string `json:"pullSecretsNamespace,omitempty"`
	// UpdateInterval is the interval in human readable format (1m, 1h, 1d...) at which matched CachedImages from this Repository are updated (see spec.UpdateFilters)
	UpdateInterval *DurationOverride `json:"updateInterval,omitempty"`
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

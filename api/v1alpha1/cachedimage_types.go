/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var RepositoryLabelName = "cache-registry.enix.io/repository"

// CachedImageSpec defines the desired state of CachedImage
type CachedImageSpec struct {
	Image       string `json:"image"`
	SourceImage string `json:"sourceImage"`
	// +optional
	ExpiresAt            *metav1.Time `json:"expiresAt,omitempty"`
	PullSecretNames      []string     `json:"pullSecretNames,omitempty"`
	PullSecretsNamespace string       `json:"pullSecretsNamespace,omitempty"`
}

// CachedImageStatus defines the observed state of CachedImage
type CachedImageStatus struct {
	IsCached bool `json:"isCached,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Cluster,shortName=ci
//+kubebuilder:printcolumn:name="Source image",type="string",JSONPath=".spec.sourceImage"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
//+kubebuilder:printcolumn:name="Cached",type="boolean",JSONPath=".status.isCached"

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

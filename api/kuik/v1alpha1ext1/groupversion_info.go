// Package v1alpha1ext1 contains API Schema definitions for the kuik.enix.io v1alpha1ext1 API group
// +kubebuilder:object:generate=true
// +groupName=kuik.enix.io
package v1alpha1ext1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is group version used to register these objects
	GroupVersion = schema.GroupVersion{Group: "kuik.enix.io", Version: "v1alpha1ext1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

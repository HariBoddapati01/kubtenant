// Package v1alpha1 contains API Schema definitions for the platform v1alpha1 API group.
// +kubebuilder:object:generate=true
// +groupName=platform.hariboddapati.dev
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is group version used to register these objects.
	GroupVersion = schema.GroupVersion{Group: "platform.hariboddapati.dev", Version: "v1alpha1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	//nolint:staticcheck // scheme.Builder is the idiomatic kubebuilder API-package pattern.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

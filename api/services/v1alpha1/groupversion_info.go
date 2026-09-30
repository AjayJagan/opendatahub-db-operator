/*
Copyright 2026.

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

// Package v1alpha1 defines the services.platform.opendatahub.io API.
// +kubebuilder:object:generate=true
// +groupName=services.platform.opendatahub.io
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	// GroupName is the DNS subdomain for this API.
	GroupName = "services.platform.opendatahub.io"
	// Version is the served API version.
	Version = "v1alpha1"
)

var (
	// SchemeGroupVersion identifies the API version registered by this package.
	SchemeGroupVersion = schema.GroupVersion{Group: GroupName, Version: Version}

	// GroupVersion is the compatibility name for SchemeGroupVersion.
	GroupVersion = SchemeGroupVersion

	// SchemeBuilder registers this package's API types with runtime.Scheme.
	SchemeBuilder = &runtime.SchemeBuilder{}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		metav1.AddToGroupVersion(s, SchemeGroupVersion)
		return nil
	})
}

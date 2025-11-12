/*
Copyright 2025.

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
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// DecofileSpec defines the desired state of Decofile.
type DecofileSpec struct {
	// Source specifies where to get the configuration data
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=inline;github
	Source string `json:"source"`

	// Inline contains direct JSON values (used when source=inline)
	// +optional
	Inline *InlineSource `json:"inline,omitempty"`

	// GitHub contains repository information (used when source=github)
	// +optional
	GitHub *GitHubSource `json:"github,omitempty"`
}

// InlineSource contains direct JSON configuration data
type InlineSource struct {
	// Value is a map where each key becomes a ConfigMap key,
	// and each value is a JSON object that will be stringified
	// +kubebuilder:validation:Required
	Value map[string]runtime.RawExtension `json:"value"`
}

// GitHubSource contains GitHub repository information
type GitHubSource struct {
	// Org is the GitHub organization or user
	// +kubebuilder:validation:Required
	Org string `json:"org"`

	// Repo is the repository name
	// +kubebuilder:validation:Required
	Repo string `json:"repo"`

	// Commit is the commit SHA or ref to fetch
	// +kubebuilder:validation:Required
	Commit string `json:"commit"`

	// Path is the directory path within the repository
	// +kubebuilder:validation:Required
	Path string `json:"path"`

	// Secret is the name of the Kubernetes secret containing GitHub credentials
	// +kubebuilder:validation:Required
	Secret string `json:"secret"`
}

// DecofileStatus defines the observed state of Decofile.
type DecofileStatus struct {
	// ConfigMapName is the name of the ConfigMap created for this Decofile
	// +optional
	ConfigMapName string `json:"configMapName,omitempty"`

	// LastUpdated is the timestamp of the last update
	// +optional
	LastUpdated metav1.Time `json:"lastUpdated,omitempty"`

	// Conditions represent the latest available observations of the Decofile's state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// SourceType indicates which source was used (inline or github)
	// +optional
	SourceType string `json:"sourceType,omitempty"`

	// GitHubCommit stores the commit SHA if using GitHub source
	// +optional
	GitHubCommit string `json:"githubCommit,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Decofile is the Schema for the decofiles API.
type Decofile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DecofileSpec   `json:"spec,omitempty"`
	Status DecofileStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DecofileList contains a list of Decofile.
type DecofileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Decofile `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Decofile{}, &DecofileList{})
}

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
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// Decofile source kinds (DecofileSpec.Source).
const (
	SourceInline = "inline"
	SourceGitHub = "github"
)

// Decofile delivery targets (DecofileSpec.Target) — selects the FastDeployment
// strategy that reconciles the CR.
const (
	// TargetConfigMap writes a ConfigMap + notifies Knative pods (default).
	TargetConfigMap = "configmap"
	// TargetTanstackKV runs a Job that pushes the decofile to Cloudflare KV.
	TargetTanstackKV = "tanstack-kv"
	// TargetS3 uploads the merged decofile JSON to S3 and points the runtime at
	// it over HTTP (DECO_RELEASE=https://…) instead of mounting a ConfigMap.
	// Escapes the ~1MB etcd ConfigMap ceiling for content-heavy sites. Bucket /
	// region / public host come from operator env (DECOFILE_S3_*).
	TargetS3 = "s3"
)

// DecofileSpec defines the desired state of Decofile.
// +kubebuilder:validation:XValidation:rule="self.target != 'tanstack-kv' || has(self.tanstackKV)",message="spec.tanstackKV is required when target is tanstack-kv"
// +kubebuilder:validation:XValidation:rule="self.target != 'tanstack-kv' || self.source == 'github'",message="source must be 'github' when target is tanstack-kv"
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

	// DeploymentId is used for pod label matching (defaults to metadata.name if absent)
	// Pods are queried using the app.deco/deploymentId label
	// +optional
	DeploymentId string `json:"deploymentId,omitempty"`

	// Target selects how this Decofile is delivered (the FastDeployment strategy).
	// "configmap" (default) writes a ConfigMap and notifies Knative pods.
	// "tanstack-kv" runs a self-cleaning Job that pushes the decofile to Cloudflare
	// KV — the fast-deploy content path for TanStack/Workers sites.
	// "s3" uploads the decofile to S3 and serves it over HTTP (no ConfigMap) —
	// the path for content-heavy sites that would exceed the etcd ConfigMap limit.
	// +kubebuilder:validation:Enum=configmap;tanstack-kv;s3
	// +kubebuilder:default=configmap
	// +optional
	Target string `json:"target,omitempty"`

	// TanstackKV configures the tanstack-kv target. Required when target=tanstack-kv.
	// The repo/commit to sync come from spec.github (source=github).
	// +optional
	TanstackKV *TanstackKVTarget `json:"tanstackKV,omitempty"`
}

// TanstackKVTarget configures Cloudflare KV fast-deploy for a TanStack/Workers site.
type TanstackKVTarget struct {
	// KVNamespaceID is the Cloudflare KV namespace id for this site (one per site).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	KVNamespaceID string `json:"kvNamespaceId"`

	// SiteOrigin is the deployed site origin used to POST /_cache/purge after the
	// sync (e.g. https://www.example.com). Optional — purge is skipped when empty.
	// +optional
	SiteOrigin string `json:"siteOrigin,omitempty"`
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

	// Secret is the name of the Kubernetes secret containing GitHub credentials.
	// If omitted, the GITHUB_TOKEN environment variable will be used.
	// +optional
	Secret string `json:"secret,omitempty"`
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

	// JobName is the K8s Job name for the current tanstack-kv sync (target=tanstack-kv).
	// +optional
	JobName string `json:"jobName,omitempty"`

	// ContentHash is the SHA-256 of the last delivered decofile JSON. Used by the
	// s3 target to skip re-upload/notify when content is unchanged.
	// +optional
	ContentHash string `json:"contentHash,omitempty"`

	// S3URL is the HTTP URL the runtime reads from when target=s3.
	// +optional
	S3URL string `json:"s3URL,omitempty"`
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

// ConfigMapName returns the deterministic name of the ConfigMap for this Decofile
func (d *Decofile) ConfigMapName() string {
	return "decofile-" + d.Name
}

// DeploymentIdOrName returns spec.deploymentId, defaulting to the object name.
func (d *Decofile) DeploymentIdOrName() string {
	if d.Spec.DeploymentId != "" {
		return d.Spec.DeploymentId
	}
	return d.Name
}

// S3ObjectKey returns the deterministic S3 key for this Decofile's config,
// derived the same way by the reconciler (upload) and the Service webhook
// (DECO_RELEASE URL) so neither depends on status. prefix is the operator-wide
// DECOFILE_S3_PREFIX (may be empty).
func (d *Decofile) S3ObjectKey(prefix string) string {
	key := d.Namespace + "/" + d.DeploymentIdOrName() + "/decofile.json"
	if prefix != "" {
		return strings.Trim(prefix, "/") + "/" + key
	}
	return key
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

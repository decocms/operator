package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DecoSpec defines the desired state of a Deco workload.
type DecoSpec struct {
	// Type is the workload type. site | server | admin | preview
	// +optional
	Type string `json:"type,omitempty"`

	// Site is the site/repository name.
	// +kubebuilder:validation:Required
	Site string `json:"site"`

	// Org is the GitHub organization or owner.
	// +kubebuilder:validation:Required
	Org string `json:"org"`

	// Framework is the site framework. deno | tanstack | next | remix | static
	// +optional
	Framework string `json:"framework,omitempty"`

	// Build describes the production build pipeline.
	// +optional
	Build *DecoSpecBuild `json:"build,omitempty"`

	// Serving describes the runtime serving configuration.
	// +optional
	Serving *DecoSpecServing `json:"serving,omitempty"`

	// Previews configures preview builds for this site.
	// Admin adds entries to Previews.Active on PR open and removes on PR close.
	// +optional
	Previews *DecoPreviewPolicy `json:"previews,omitempty"`
}

// DecoEnvVar is a plain environment variable injected into the build Job.
type DecoEnvVar struct {
	// Name is the environment variable name.
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// Value is the literal value.
	// +optional
	Value string `json:"value,omitempty"`
}

// DecoSecretRef mounts all keys of a K8s Secret as environment variables in the build Job.
type DecoSecretRef struct {
	// Name is the name of the K8s Secret in the same namespace as the Job.
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// Optional specifies whether the Secret must exist. Defaults to false.
	// +optional
	Optional *bool `json:"optional,omitempty"`
}

// DecoSpecBuild describes the build pipeline for a workload.
type DecoSpecBuild struct {
	// Type is the build mechanism. Currently only k8s-job is supported.
	// +optional
	Type string `json:"type,omitempty"`

	// Source identifies the code revision to build.
	// Repository and owner come from spec.site and spec.org.
	Source DecoSpecBuildSource `json:"source"`

	// Builder overrides the builder image (repository:tag).
	// +optional
	Builder string `json:"builder,omitempty"`

	// Envs are additional plain environment variables injected into the build Job.
	// +optional
	Envs []DecoEnvVar `json:"envs,omitempty"`

	// Secrets are K8s Secrets whose keys are mounted as environment variables in the build Job.
	// The secrets must exist in the same namespace as the Job (the site namespace).
	// +optional
	Secrets []DecoSecretRef `json:"secrets,omitempty"`

	// TTLSecondsAfterFinished controls how long the Job is kept after completion.
	// Defaults to 600 (10 minutes) when not set.
	// +optional
	// +kubebuilder:validation:Minimum=0
	TTLSecondsAfterFinished *int32 `json:"ttlSecondsAfterFinished,omitempty"`

	// NodeSelector constrains the build Job pods to nodes matching all the specified labels.
	// Use this to target a specific node pool (e.g. cloud.google.com/gke-nodepool: build-pool).
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations are applied to the build Job pods, allowing them to be scheduled on tainted nodes.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
}

// DecoSpecBuildSource identifies the code revision to build.
type DecoSpecBuildSource struct {
	// CommitSha is the git commit SHA to build.
	// Updating this field triggers a new build.
	// +kubebuilder:validation:Required
	CommitSha string `json:"commitSha"`

	// Production indicates whether this is a production deploy.
	// +optional
	Production bool `json:"production,omitempty"`

	// BranchRef is the branch name for preview aliases (non-production only).
	// +optional
	BranchRef string `json:"branchRef,omitempty"`
}

// DecoSpecServing describes the runtime serving configuration.
type DecoSpecServing struct {
	// Type is the serving runtime. Drives both serving and build job selection.
	// Supported: cloudflare-worker | knative | deployment
	// +kubebuilder:validation:Required
	Type string `json:"type"`
}

// DecoPreviewPolicy configures the preview system for this site.
type DecoPreviewPolicy struct {
	// Type is the preview runtime. cloudflare-preview | statefulset | sandbox
	// +kubebuilder:validation:Required
	Type string `json:"type"`

	// MaxActive is the maximum number of concurrent previews the operator will build.
	// Operator processes only the most recent MaxActive entries in Active.
	// +optional
	MaxActive int32 `json:"maxActive,omitempty"`

	// TTL is the duration after which completed previews are eligible for cleanup (e.g. "48h").
	// +optional
	TTL string `json:"ttl,omitempty"`

	// Active is the list of preview builds currently requested.
	// Admin adds entries on PR open, removes on PR close.
	// +optional
	Active []DecoPreviewRequest `json:"active,omitempty"`
}

// DecoPreviewRequest identifies a single preview build request.
type DecoPreviewRequest struct {
	// CommitSha is the git commit SHA to build.
	// +kubebuilder:validation:Required
	CommitSha string `json:"commitSha"`

	// BranchRef is the branch or PR ref (used as the wrangler preview alias).
	// +kubebuilder:validation:Required
	BranchRef string `json:"branchRef"`

	// PrId is the pull request ID, for tracking.
	// +optional
	PrId string `json:"prId,omitempty"`
}

// DecoStatus defines the observed state of a Deco workload.
type DecoStatus struct {
	// Build tracks the current production build lifecycle.
	// +optional
	Build *DecoStatusBuild `json:"build,omitempty"`

	// Previews tracks the build status of each active preview.
	// +optional
	Previews []DecoPreviewStatus `json:"previews,omitempty"`
}

// DecoStatusBuild tracks the production build lifecycle.
type DecoStatusBuild struct {
	// Phase is the current build phase: Running | Succeeded | Failed
	// +optional
	Phase string `json:"phase,omitempty"`

	// CommitSha is the commit currently being built (or last attempted).
	// +optional
	CommitSha string `json:"commitSha,omitempty"`

	// LastBuiltCommit is the commit SHA of the last successful build.
	// +optional
	LastBuiltCommit string `json:"lastBuiltCommit,omitempty"`

	// JobName is the K8s Job name for the current build.
	// +optional
	JobName string `json:"jobName,omitempty"`

	// StartTime is when the current build started.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// CompletionTime is when the current build finished.
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`
}

// DecoPreviewStatus tracks the build status of a single preview.
type DecoPreviewStatus struct {
	CommitSha      string       `json:"commitSha"`
	BranchRef      string       `json:"branchRef"`
	PrId           string       `json:"prId,omitempty"`
	JobName        string       `json:"jobName,omitempty"`
	Phase          string       `json:"phase"`
	StartTime      *metav1.Time `json:"startTime,omitempty"`
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=decos
// +kubebuilder:printcolumn:name="Site",type=string,JSONPath=`.spec.site`
// +kubebuilder:printcolumn:name="Serving",type=string,JSONPath=`.spec.serving.type`
// +kubebuilder:printcolumn:name="Commit",type=string,JSONPath=`.spec.build.source.commitSha`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.build.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Deco is the Schema for the decos API.
type Deco struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DecoSpec   `json:"spec,omitempty"`
	Status DecoStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DecoList contains a list of Deco.
type DecoList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Deco `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Deco{}, &DecoList{})
}

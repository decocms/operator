package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DecoBuildPhase is the lifecycle phase of a DecoBuild.
type DecoBuildPhase string

const (
	DecoBuildPhasePending   DecoBuildPhase = "Pending"
	DecoBuildPhaseRunning   DecoBuildPhase = "Running"
	DecoBuildPhaseSucceeded DecoBuildPhase = "Succeeded"
	DecoBuildPhaseFailed    DecoBuildPhase = "Failed"
)

// DecoBuildSpec defines the desired state of a Cloudflare Workers build.
type DecoBuildSpec struct {
	// Site is the deco site name (used as the Cloudflare Worker name by default).
	// +kubebuilder:validation:Required
	Site string `json:"site"`

	// Owner is the GitHub repository owner/org.
	// +kubebuilder:validation:Required
	Owner string `json:"owner"`

	// Repo is the GitHub repository name.
	// +kubebuilder:validation:Required
	Repo string `json:"repo"`

	// CommitSha is the git commit SHA to build.
	// +kubebuilder:validation:Required
	CommitSha string `json:"commitSha"`

	// Production indicates whether this is a production deploy.
	// When false, a wrangler preview alias is created instead.
	// +optional
	Production bool `json:"production,omitempty"`

	// BranchRef is the branch name used as the preview alias message (non-production only).
	// +optional
	BranchRef string `json:"branchRef,omitempty"`

	// WorkerName overrides the Cloudflare Worker name. Defaults to site name.
	// +optional
	WorkerName string `json:"workerName,omitempty"`

	// EntryPoint is the worker entry file path. Defaults to src/worker-entry.ts.
	// +optional
	EntryPoint string `json:"entryPoint,omitempty"`

	// CompatDate is the Cloudflare compatibility date. Defaults to 2025-04-01.
	// +optional
	CompatDate string `json:"compatDate,omitempty"`

	// GithubToken is a GitHub token for cloning private repositories.
	// Prefer GithubTokenSecret for production; this field is for convenience.
	// +optional
	GithubToken string `json:"githubToken,omitempty"`

	// GithubTokenSecret is the name of a K8s Secret (in this namespace) containing
	// a "token" key with a GitHub token. Takes precedence over GithubToken.
	// +optional
	GithubTokenSecret string `json:"githubTokenSecret,omitempty"`
}

// DecoBuildStatus defines the observed state of a DecoBuild.
type DecoBuildStatus struct {
	// Phase is the current lifecycle phase.
	// +optional
	Phase DecoBuildPhase `json:"phase,omitempty"`

	// JobName is the K8s Job created for this build.
	// +optional
	JobName string `json:"jobName,omitempty"`

	// StartTime is when the build job was created.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// CompletionTime is when the build job finished (succeeded or failed).
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	// Conditions represent the latest observations of the build state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Site",type=string,JSONPath=`.spec.site`
// +kubebuilder:printcolumn:name="Commit",type=string,JSONPath=`.spec.commitSha`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DecoBuild is the Schema for the decobuilds API.
type DecoBuild struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DecoBuildSpec   `json:"spec,omitempty"`
	Status DecoBuildStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DecoBuildList contains a list of DecoBuild.
type DecoBuildList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DecoBuild `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DecoBuild{}, &DecoBuildList{})
}

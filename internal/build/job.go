// Package build contains helpers for creating Cloudflare Workers build Jobs.
// It is the Go equivalent of hosting/cfworkers/build.ts in the admin.
package build

import (
	"crypto/sha256"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
)

const (
	BuilderImage           = "ghcr.io/decocms/infra_applications/cfworkers-builder:v1.0.0"
	DefaultEntryPoint      = "src/worker-entry.ts"
	DefaultCompatDate      = "2025-04-01"
	LogsBucket             = "deco-sites-build-logs"
	CacheBucket            = "deco-cfworkers-deployments"
	ttlSecondsAfterFinished = int32(24 * 60 * 60) // 24h
)

// JobName returns a deterministic job name from a commit SHA.
// Mirrors generateJobName() in the admin's build.ts.
func JobName(commitSha string) string {
	h := sha256.Sum256([]byte("build-" + commitSha))
	return fmt.Sprintf("build-%x", h[:6])
}

// PresignedURLs are the three S3 presigned URLs the build job needs.
type PresignedURLs struct {
	LogsUpload    string
	CacheDownload string
	CacheUpload   string
}

// JobOpts are the inputs for NewJob.
type JobOpts struct {
	Build         *decositesv1alpha1.DecoBuild
	JobName       string
	GithubToken   string
	CfApiToken    string
	CfAccountId   string
	PresignedURLs PresignedURLs
}

// NewJob builds the batchv1.Job spec for a cfworkers build.
// This is the Go equivalent of buildJobOf() in the admin's build.ts.
func NewJob(opts JobOpts) *batchv1.Job {
	spec := opts.Build.Spec

	workerName := spec.WorkerName
	if workerName == "" {
		workerName = spec.Site
	}
	entryPoint := spec.EntryPoint
	if entryPoint == "" {
		entryPoint = DefaultEntryPoint
	}
	compatDate := spec.CompatDate
	if compatDate == "" {
		compatDate = DefaultCompatDate
	}
	isProduction := "false"
	if spec.Production {
		isProduction = "true"
	}

	env := []corev1.EnvVar{
		{Name: "GIT_REPO", Value: fmt.Sprintf("https://github.com/%s/%s", spec.Owner, spec.Repo)},
		{Name: "COMMIT_SHA", Value: spec.CommitSha},
		{Name: "DECO_SITE_NAME", Value: spec.Site},
		{Name: "BUILD_NAME", Value: opts.JobName},
		{Name: "IS_PRODUCTION", Value: isProduction},
		{Name: "WORKER_NAME", Value: workerName},
		{Name: "CF_ACCOUNT_ID", Value: opts.CfAccountId},
		{Name: "CLOUDFLARE_API_TOKEN", Value: opts.CfApiToken},
		{Name: "ENTRY_POINT", Value: entryPoint},
		{Name: "COMPAT_DATE", Value: compatDate},
		{Name: "LOGS_UPLOAD_URL", Value: opts.PresignedURLs.LogsUpload},
		{Name: "CACHE_DOWNLOAD_URL", Value: opts.PresignedURLs.CacheDownload},
		{Name: "CACHE_UPLOAD_URL", Value: opts.PresignedURLs.CacheUpload},
	}
	if spec.BranchRef != "" {
		env = append(env, corev1.EnvVar{Name: "BRANCH_REF", Value: spec.BranchRef})
	}
	if opts.GithubToken != "" {
		env = append(env, corev1.EnvVar{Name: "GITHUB_TOKEN", Value: opts.GithubToken})
	}

	backoffLimit := int32(0)
	ttl := ttlSecondsAfterFinished

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      opts.JobName,
			Namespace: opts.Build.Namespace,
			Labels: map[string]string{
				"app.deco/site":     spec.Site,
				"app.deco/owner":    spec.Owner,
				"app.deco/repo":     spec.Repo,
				"app.deco/platform": "cfworkers",
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:  "builder",
							Image: BuilderImage,
							Env:   env,
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceMemory:           resource.MustParse("4Gi"),
									corev1.ResourceCPU:              resource.MustParse("500m"),
									corev1.ResourceEphemeralStorage: resource.MustParse("2Gi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceMemory:           resource.MustParse("4Gi"),
									corev1.ResourceEphemeralStorage: resource.MustParse("3Gi"),
								},
							},
						},
					},
				},
			},
		},
	}
}

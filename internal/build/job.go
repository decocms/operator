// Package build contains helpers for creating Cloudflare Workers build Jobs.
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
	BuilderImage            = "ghcr.io/decocms/infra_applications/cfworkers-builder:latest"
	LogsBucket              = "deco-sites-build-logs"
	CacheBucket             = "deco-cfworkers-deployments"
	ttlSecondsAfterFinished = int32(24 * 60 * 60) // 24h
)

// JobName returns a deterministic job name: sha256("build-{commitSha}-{site}"), first 4 bytes as hex.
func JobName(commitSha, site string) string {
	h := sha256.Sum256([]byte("build-" + commitSha + "-" + site))
	return fmt.Sprintf("build-%x", h[:4])
}

// PresignedURLs are the S3 presigned URLs the build job needs.
type PresignedURLs struct {
	LogsUpload    string
	CacheDownload string
	CacheUpload   string
}

// JobOpts are the inputs for NewJob.
type JobOpts struct {
	Deco          *decositesv1alpha1.Deco
	JobName       string
	GithubToken   string
	CfApiToken    string
	CfAccountId   string
	PresignedURLs PresignedURLs
	// SourceOverride replaces spec.build.source when set (used for preview builds).
	SourceOverride *decositesv1alpha1.DecoSpecBuildSource
}

// NewJob builds the batchv1.Job spec for a cfworkers build.
func NewJob(opts JobOpts) *batchv1.Job {
	spec := opts.Deco.Spec
	var src decositesv1alpha1.DecoSpecBuildSource
	if opts.SourceOverride != nil {
		src = *opts.SourceOverride
	} else if spec.Build != nil {
		src = spec.Build.Source
	}

	owner := spec.Org
	repo := spec.Site

	isProduction := "false"
	if src.Production {
		isProduction = "true"
	}

	builderImage := BuilderImage
	if spec.Build != nil && spec.Build.Builder != "" {
		builderImage = spec.Build.Builder
	}

	env := []corev1.EnvVar{
		{Name: "GIT_REPO", Value: fmt.Sprintf("https://github.com/%s/%s", owner, repo)},
		{Name: "COMMIT_SHA", Value: src.CommitSha},
		{Name: "DECO_SITE_NAME", Value: repo},
		{Name: "WORKER_NAME", Value: repo},
		{Name: "BUILD_NAME", Value: opts.JobName},
		{Name: "IS_PRODUCTION", Value: isProduction},
		{Name: "CF_ACCOUNT_ID", Value: opts.CfAccountId},
		{Name: "CLOUDFLARE_API_TOKEN", Value: opts.CfApiToken},
		{Name: "LOGS_UPLOAD_URL", Value: opts.PresignedURLs.LogsUpload},
		{Name: "CACHE_DOWNLOAD_URL", Value: opts.PresignedURLs.CacheDownload},
		{Name: "CACHE_UPLOAD_URL", Value: opts.PresignedURLs.CacheUpload},
	}
	if src.BranchRef != "" {
		env = append(env, corev1.EnvVar{Name: "BRANCH_REF", Value: src.BranchRef})
	}
	if opts.GithubToken != "" {
		env = append(env, corev1.EnvVar{Name: "GITHUB_TOKEN", Value: opts.GithubToken})
	}

	backoffLimit := int32(0)
	ttl := ttlSecondsAfterFinished

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      opts.JobName,
			Namespace: opts.Deco.Namespace,
			Labels: map[string]string{
				"app.deco/site":    repo,
				"app.deco/org":     owner,
				"app.deco/serving": spec.Serving.Type,
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
							Image: builderImage,
							Env:   env,
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceMemory:           resource.MustParse("1Gi"),
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

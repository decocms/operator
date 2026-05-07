// Package build contains helpers for creating cfworkers build Jobs.
package build

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
)

// JobName returns a deterministic job name: sha256("build-{commitSha}-{site}"), first 4 bytes as hex.
func JobName(commitSha, site string) string {
	h := sha256.Sum256([]byte("build-" + commitSha + "-" + site))
	return fmt.Sprintf("build-%x", h[:4])
}

// presignedURLs are the S3 presigned URLs the build job needs.
type presignedURLs struct {
	LogsUpload    string
	CacheDownload string
	CacheUpload   string
}

// cfWorkersJobOpts are the inputs for NewJob.
type cfWorkersJobOpts struct {
	Deco          *decositesv1alpha1.Deco
	JobName       string
	GithubToken   string
	CfApiToken    string
	CfAccountId   string
	presignedURLs presignedURLs
	// SourceOverride replaces spec.build.source when set (used for preview builds).
	SourceOverride *decositesv1alpha1.DecoSpecBuildSource
	// BuilderImage is the platform default. spec.build.builder in the CR takes precedence when set.
	BuilderImage string
	// TTLSeconds controls how long the Job is kept after completion.
	TTLSeconds int32
}

// newCfWorkersJob builds the batchv1.Job spec for a cfworkers build.
func newCfWorkersJob(opts cfWorkersJobOpts) *batchv1.Job {
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

	// CR takes precedence over the platform default.
	builderImage := opts.BuilderImage
	if spec.Build != nil && spec.Build.Builder != "" {
		builderImage = spec.Build.Builder
	}

	env := []corev1.EnvVar{
		{Name: "GIT_REPO", Value: fmt.Sprintf("https://github.com/%s/%s", owner, repo)},
		{Name: "COMMIT_SHA", Value: src.CommitSha},
		{Name: "DECO_SITE_NAME", Value: repo},
		{Name: "BUILD_NAME", Value: opts.JobName},
		{Name: "IS_PRODUCTION", Value: isProduction},
		{Name: "CF_ACCOUNT_ID", Value: opts.CfAccountId},
		{Name: "CLOUDFLARE_API_TOKEN", Value: opts.CfApiToken},
		{Name: "LOGS_UPLOAD_URL", Value: opts.presignedURLs.LogsUpload},
		{Name: "CACHE_DOWNLOAD_URL", Value: opts.presignedURLs.CacheDownload},
		{Name: "CACHE_UPLOAD_URL", Value: opts.presignedURLs.CacheUpload},
	}
	if src.BranchRef != "" {
		env = append(env, corev1.EnvVar{Name: "BRANCH_REF", Value: src.BranchRef})
	}
	if opts.GithubToken != "" {
		env = append(env, corev1.EnvVar{Name: "GITHUB_TOKEN", Value: opts.GithubToken})
	}
	if opts.Deco.Spec.Build != nil {
		for _, e := range opts.Deco.Spec.Build.Envs {
			env = append(env, corev1.EnvVar{Name: e.Name, Value: e.Value})
		}
	}

	var envFrom []corev1.EnvFromSource
	if opts.Deco.Spec.Build != nil {
		secrets := opts.Deco.Spec.Build.Secrets
		envFrom = make([]corev1.EnvFromSource, len(secrets))
		for i, s := range secrets {
			envFrom[i] = corev1.EnvFromSource{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: s.Name},
					Optional:             s.Optional,
				},
			}
		}
	}

	backoffLimit := int32(0)
	ttl := opts.TTLSeconds
	if spec.Build != nil && spec.Build.TTLSecondsAfterFinished != nil {
		ttl = *spec.Build.TTLSecondsAfterFinished
	}

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
							Name:    "builder",
							Image:   builderImage,
							Env:     env,
							EnvFrom: envFrom,
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

// CfWorkersConfig holds all configuration the Cloudflare Workers builder needs.
type CfWorkersConfig struct {
	CfApiToken   string
	CfAccountId  string
	GithubToken  string
	BuilderImage string
	TTLSeconds   int32
	S3           S3Config
}

// CfWorkersConfigFromEnv reads CfWorkersConfig from standard environment variables.
func CfWorkersConfigFromEnv() CfWorkersConfig {
	return CfWorkersConfig{
		CfApiToken:   os.Getenv("CLOUDFLARE_API_WORKERS_TOKEN"),
		CfAccountId:  os.Getenv("CLOUDFLARE_ACCOUNT_ID"),
		GithubToken:  os.Getenv("GITHUB_TOKEN"),
		BuilderImage: os.Getenv("CFWORKERS_BUILDER_IMAGE"),
		TTLSeconds:   10 * 60,
		S3: S3Config{
			Region:          os.Getenv("S3_REGION"),
			AccessKeyID:     os.Getenv("S3_ACCESS_KEY_ID"),
			SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
			LogsBucket:      os.Getenv("S3_LOGS_BUCKET"),
			ArtifactsBucket: os.Getenv("S3_ARTIFACTS_BUCKET"),
		},
	}
}

type cfWorkersBuilder struct {
	cfg       CfWorkersConfig
	presignFn func(ctx context.Context, cfg S3Config, site, jobName string) (presignedURLs, error)
}

// NewCloudflareFactory returns a Builder for spec.serving.type = "cloudflare-worker".
func NewCloudflareFactory(cfg CfWorkersConfig) Builder {
	return &cfWorkersBuilder{cfg: cfg, presignFn: generatePresignedURLs}
}

func (b *cfWorkersBuilder) NewJob(ctx context.Context, deco *decositesv1alpha1.Deco, jobName string, source decositesv1alpha1.DecoSpecBuildSource) (*batchv1.Job, error) {
	urls, err := b.presignFn(ctx, b.cfg.S3, deco.Spec.Site, jobName)
	if err != nil {
		return nil, fmt.Errorf("generating presigned URLs: %w", err)
	}
	return newCfWorkersJob(cfWorkersJobOpts{
		Deco:           deco,
		JobName:        jobName,
		GithubToken:    b.cfg.GithubToken,
		CfApiToken:     b.cfg.CfApiToken,
		CfAccountId:    b.cfg.CfAccountId,
		presignedURLs:  urls,
		SourceOverride: &source,
		BuilderImage:   b.cfg.BuilderImage,
		TTLSeconds:     b.cfg.TTLSeconds,
	}), nil
}

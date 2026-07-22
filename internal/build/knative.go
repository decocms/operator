// Package build — Knative (Node/TanStack) builder.
//
// Sibling of the Cloudflare Workers builder (cfworkers.go). Instead of building
// a Worker and deploying it with wrangler, this builder produces a Node build
// of a TanStack site and uploads a single self-contained dist tar to S3. A
// generic node-runner Knative Service then pulls that tar at boot and runs it.
//
// The build image is expected to:
//   1. clone {org}/{site} @ commitSha
//   2. npm ci
//   3. vite build --config vite.config.node.ts   (DECO_TARGET=node; the config
//      is generic and strips the Cloudflare plugin — see
//      infra_applications/images/node-runner)
//   4. tar dist/ (zstd) and upload to s3://{ARTIFACTS_BUCKET}/{ARTIFACT_KEY}
//
// The site repo is NOT modified — the node build config is supplied by the
// builder image, so this scales across the whole *-tanstack fleet.
package build

import (
	"context"
	"fmt"
	"os"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
	"github.com/deco-sites/decofile-operator/internal/envparse"
)

// ArtifactKey is the deterministic S3 key of a site's Node build tar.
// The Knative Service passes this same key to the node-runner as SOURCE_ASSET_PATH.
func ArtifactKey(org, site, commitSha string) string {
	return fmt.Sprintf("%s/%s/%s/dist.tar.zst", org, site, commitSha)
}

// KnativeConfig holds configuration the Knative (Node) builder needs.
// Credentials are provided via Pod Identity (no static keys needed).
type KnativeConfig struct {
	GithubToken           string
	BuilderImage          string
	BuilderServiceAccount string
	TTLSeconds            int32
	S3                    S3Config
	NodeSelector          map[string]string
	Tolerations           []corev1.Toleration
}

// KnativeConfigFromEnv reads KnativeConfig from standard environment variables.
func KnativeConfigFromEnv() KnativeConfig {
	return KnativeConfig{
		GithubToken:           os.Getenv("GITHUB_TOKEN"),
		BuilderImage:          os.Getenv("KNATIVE_BUILDER_IMAGE"),
		BuilderServiceAccount: os.Getenv("BUILD_SERVICE_ACCOUNT"),
		TTLSeconds:            10 * 60,
		NodeSelector:          envparse.NodeSelector(os.Getenv("BUILD_NODE_SELECTOR")),
		Tolerations:           envparse.Tolerations(os.Getenv("BUILD_TOLERATIONS")),
		S3: S3Config{
			Region:          os.Getenv("S3_REGION"),
			LogsBucket:      os.Getenv("S3_LOGS_BUCKET"),
			ArtifactsBucket: os.Getenv("S3_ARTIFACTS_BUCKET"),
			StateBucket:     os.Getenv("S3_STATE_BUCKET"),
		},
	}
}

type knativeBuilder struct {
	cfg KnativeConfig
}

// NewKnativeFactory returns a Builder for spec.serving.type = "knative".
func NewKnativeFactory(cfg KnativeConfig) Builder {
	return &knativeBuilder{cfg: cfg}
}

func (b *knativeBuilder) NewJob(_ context.Context, deco *decositesv1alpha1.Deco, jobName string, source decositesv1alpha1.DecoSpecBuildSource) (*batchv1.Job, error) {
	spec := deco.Spec
	owner := spec.Org
	repo := spec.Site

	isProduction := "false"
	if source.Production {
		isProduction = "true"
	}

	// CR overrides the platform default builder image.
	builderImage := b.cfg.BuilderImage
	if spec.Build != nil && spec.Build.Builder != "" {
		builderImage = spec.Build.Builder
	}

	artifactKey := ArtifactKey(owner, repo, source.CommitSha)

	env := []corev1.EnvVar{
		{Name: "GIT_REPO", Value: fmt.Sprintf("https://github.com/%s/%s", owner, repo)},
		{Name: "COMMIT_SHA", Value: source.CommitSha},
		{Name: "DECO_SITE_NAME", Value: repo},
		{Name: "BUILD_NAME", Value: jobName},
		{Name: "IS_PRODUCTION", Value: isProduction},
		// DECO_TARGET selects the Node build path in the builder image.
		{Name: "DECO_TARGET", Value: "node"},
		// Where the dist tar is uploaded; the Knative Service reads the same key.
		{Name: "ARTIFACT_KEY", Value: artifactKey},
		{Name: "S3_LOGS_BUCKET", Value: b.cfg.S3.LogsBucket},
		{Name: "S3_ARTIFACTS_BUCKET", Value: b.cfg.S3.ArtifactsBucket},
		{Name: "S3_REGION", Value: b.cfg.S3.Region},
	}
	if source.BranchRef != "" {
		env = append(env, corev1.EnvVar{Name: "BRANCH_REF", Value: source.BranchRef})
	}
	if b.cfg.GithubToken != "" {
		env = append(env, corev1.EnvVar{Name: "GITHUB_TOKEN", Value: b.cfg.GithubToken})
	}
	if spec.Build != nil {
		for _, e := range spec.Build.Envs {
			env = append(env, corev1.EnvVar{Name: e.Name, Value: e.Value})
		}
	}

	var envFrom []corev1.EnvFromSource
	if spec.Build != nil {
		secrets := spec.Build.Secrets
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
	ttl := b.cfg.TTLSeconds
	if spec.Build != nil && spec.Build.TTLSecondsAfterFinished != nil {
		ttl = *spec.Build.TTLSecondsAfterFinished
	}

	nodeSelector := b.cfg.NodeSelector
	if spec.Build != nil && len(spec.Build.NodeSelector) > 0 {
		nodeSelector = spec.Build.NodeSelector
	}

	tolerations := b.cfg.Tolerations
	if spec.Build != nil && len(spec.Build.Tolerations) > 0 {
		tolerations = spec.Build.Tolerations
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: deco.Namespace,
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
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: b.cfg.BuilderServiceAccount,
					NodeSelector:       nodeSelector,
					Tolerations:        tolerations,
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
	}, nil
}

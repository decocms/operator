package build

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
)

// Compile-time: cfWorkersBuilder must satisfy Builder.
var _ Builder = (*cfWorkersBuilder)(nil)


func TestCfWorkersConfigFromEnv(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_WORKERS_TOKEN", "cf-token")
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "cf-account")
	t.Setenv("GITHUB_TOKEN", "gh-token")
	t.Setenv("S3_REGION", "us-east-1")
	t.Setenv("S3_ACCESS_KEY_ID", "key-id")
	t.Setenv("S3_SECRET_ACCESS_KEY", "secret")

	cfg := CfWorkersConfigFromEnv()

	if cfg.CfApiToken != "cf-token" {
		t.Errorf("CfApiToken: want %q, got %q", "cf-token", cfg.CfApiToken)
	}
	if cfg.CfAccountId != "cf-account" {
		t.Errorf("CfAccountId: want %q, got %q", "cf-account", cfg.CfAccountId)
	}
	if cfg.GithubToken != "gh-token" {
		t.Errorf("GithubToken: want %q, got %q", "gh-token", cfg.GithubToken)
	}
	if cfg.S3.Region != "us-east-1" {
		t.Errorf("S3.Region: want %q, got %q", "us-east-1", cfg.S3.Region)
	}
	if cfg.S3.AccessKeyID != "key-id" {
		t.Errorf("S3.AccessKeyID: want %q, got %q", "key-id", cfg.S3.AccessKeyID)
	}
}


func TestCloudflareBuilder_NewJob_BuildsValidJob(t *testing.T) {
	cfg := CfWorkersConfig{
		CfApiToken:   "cf-token",
		CfAccountId:  "cf-account",
		GithubToken:  "gh-token",
		BuilderImage: "ghcr.io/test:v1",
		TTLSeconds:   3600,
		S3:           S3Config{Region: "sa-east-1", LogsBucket: "logs", CacheBucket: "cache"},
	}

	stubPresign := func(_ context.Context, _ S3Config, _, _ string) (presignedURLs, error) {
		return presignedURLs{LogsUpload: "s3://logs/job.log", CacheDownload: "s3://cache/dl", CacheUpload: "s3://cache/ul"}, nil
	}

	b := &cfWorkersBuilder{cfg: cfg, presignFn: stubPresign}

	d := &decositesv1alpha1.Deco{
		ObjectMeta: metav1.ObjectMeta{Name: "mysite", Namespace: "default"},
		Spec: decositesv1alpha1.DecoSpec{
			Site:    "mysite",
			Org:     "myorg",
			Serving: &decositesv1alpha1.DecoSpecServing{Type: "cloudflare-worker"},
		},
	}
	source := decositesv1alpha1.DecoSpecBuildSource{CommitSha: "abc123", Production: true}

	job, err := b.NewJob(context.Background(), d, "build-abc", source)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if job.Name != "build-abc" {
		t.Errorf("job name: want build-abc, got %q", job.Name)
	}
	if job.Namespace != "default" {
		t.Errorf("job namespace: want default, got %q", job.Namespace)
	}
	if len(job.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(job.Spec.Template.Spec.Containers))
	}
	c := job.Spec.Template.Spec.Containers[0]
	if c.Image != "ghcr.io/test:v1" {
		t.Errorf("container image: want ghcr.io/test:v1, got %q", c.Image)
	}
	envMap := make(map[string]string, len(c.Env))
	for _, e := range c.Env {
		envMap[e.Name] = e.Value
	}
	checks := map[string]string{
		"COMMIT_SHA":           "abc123",
		"DECO_SITE_NAME":       "mysite",
		"CF_ACCOUNT_ID":        "cf-account",
		"CLOUDFLARE_API_TOKEN": "cf-token",
		"GITHUB_TOKEN":         "gh-token",
		"IS_PRODUCTION":        "true",
	}
	for k, want := range checks {
		if got := envMap[k]; got != want {
			t.Errorf("env %s: want %q, got %q", k, want, got)
		}
	}
}

func TestCloudflareBuilder_NewJob_PropagatesPresignError(t *testing.T) {
	cfg := CfWorkersConfig{BuilderImage: "img:v1", TTLSeconds: 60}
	b := &cfWorkersBuilder{
		cfg: cfg,
		presignFn: func(_ context.Context, _ S3Config, _, _ string) (presignedURLs, error) {
			return presignedURLs{}, fmt.Errorf("s3 unavailable")
		},
	}
	d := &decositesv1alpha1.Deco{
		ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"},
		Spec: decositesv1alpha1.DecoSpec{
			Site:    "s",
			Org:     "o",
			Serving: &decositesv1alpha1.DecoSpecServing{Type: "cloudflare-worker"},
		},
	}
	_, err := b.NewJob(context.Background(), d, "job", decositesv1alpha1.DecoSpecBuildSource{CommitSha: "sha"})
	if err == nil {
		t.Fatal("expected error from presign failure")
	}
}

func TestNewCloudflareFactory_SatisfiesBuilder(t *testing.T) {
	var _ Builder = NewCloudflareFactory(CfWorkersConfig{})
}

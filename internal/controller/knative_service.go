package controller

import (
	"context"
	"fmt"
	"os"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	servingv1 "knative.dev/serving/pkg/apis/serving/v1"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
	"github.com/deco-sites/decofile-operator/internal/build"
)

// KnativeServingConfig is the platform configuration for the generic node-runner
// Knative Service. Everything site-specific comes from the Deco CR (self-contained
// CR invariant); this is only the platform-level defaults.
type KnativeServingConfig struct {
	RunnerImage    string // generic node-runner image (one for all sites)
	AssetsBucket   string // S3 bucket holding the dist artifact
	S3Region       string
	ServiceAccount string // SA with IRSA for S3 read (empty = default)
	EnvName        string // DECO_ENV_NAME (default "production")
	MinScale       int    // 0 = scale-to-zero standby
	MaxScale       int
	AppPort        int32 // container port (default 8000)
}

const knativeAppPort int32 = 8000

// KnativeServingConfigFromEnv reads the platform Knative serving config from env.
func KnativeServingConfigFromEnv() KnativeServingConfig {
	atoiOr := func(s string, d int) int {
		if v, err := strconv.Atoi(s); err == nil {
			return v
		}
		return d
	}
	return KnativeServingConfig{
		RunnerImage:    os.Getenv("NODE_RUNNER_IMAGE"),
		AssetsBucket:   os.Getenv("S3_ARTIFACTS_BUCKET"),
		S3Region:       os.Getenv("S3_REGION"),
		ServiceAccount: os.Getenv("RUNNER_SERVICE_ACCOUNT"),
		EnvName:        os.Getenv("DECO_ENV_NAME"),
		MinScale:       atoiOr(os.Getenv("NODE_RUNNER_MIN_SCALE"), 0),
		MaxScale:       atoiOr(os.Getenv("NODE_RUNNER_MAX_SCALE"), 5),
		AppPort:        int32(atoiOr(os.Getenv("NODE_RUNNER_PORT"), int(knativeAppPort))),
	}
}

// serviceName / revisionName mirror the admin's naming so the two creators are
// interchangeable during the admin→operator migration.
func serviceName(site string) string { return site + "-site" }

func revisionName(site, commitSha string) string {
	short := commitSha
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf("%s-site-%s", site, short)
}

// BuildKnativeService renders the Knative Service for a TanStack node-runner site.
// The site's code arrives via the S3 tar (SOURCE_ASSET_PATH) at boot — the image
// is generic and shared across the fleet.
func BuildKnativeService(deco *decositesv1alpha1.Deco, cfg KnativeServingConfig) *servingv1.Service {
	site := deco.Spec.Site
	org := deco.Spec.Org
	commitSha := deco.Spec.Build.Source.CommitSha

	port := cfg.AppPort
	if port == 0 {
		port = knativeAppPort
	}
	envName := cfg.EnvName
	if envName == "" {
		envName = "production"
	}

	env := []corev1.EnvVar{
		{Name: "SOURCE_ASSET_PATH", Value: build.ArtifactKey(org, site, commitSha)},
		{Name: "DECO_SITE_NAME", Value: site},
		{Name: "DECO_ENV_NAME", Value: envName},
		{Name: "PORT", Value: strconv.Itoa(int(port))},
		{Name: "ASSETS_BUCKET", Value: cfg.AssetsBucket},
		{Name: "ADMIN_S3_REGION", Value: cfg.S3Region},
		{Name: "BUILD_HASH", Value: commitSha},
	}
	// Site env from the CR (self-contained CR: config lives in the Deco, not
	// injected by admin's runtime env).
	if deco.Spec.Build != nil {
		for _, e := range deco.Spec.Build.Envs {
			env = append(env, corev1.EnvVar{Name: e.Name, Value: e.Value})
		}
	}

	labels := map[string]string{
		"app.deco/site":    site,
		"app.deco/org":     org,
		"app.deco/serving": "knative",
	}

	return &servingv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName(site),
			Namespace: deco.Namespace,
			Labels:    labels,
		},
		Spec: servingv1.ServiceSpec{
			ConfigurationSpec: servingv1.ConfigurationSpec{
				Template: servingv1.RevisionTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Name:   revisionName(site, commitSha),
						Labels: labels,
						Annotations: map[string]string{
							"autoscaling.knative.dev/min-scale": strconv.Itoa(cfg.MinScale),
							"autoscaling.knative.dev/max-scale": strconv.Itoa(cfg.MaxScale),
						},
					},
					Spec: servingv1.RevisionSpec{
						PodSpec: corev1.PodSpec{
							ServiceAccountName: cfg.ServiceAccount,
							Containers: []corev1.Container{
								{
									Name:  "app",
									Image: cfg.RunnerImage,
									Ports: []corev1.ContainerPort{{Name: "http1", ContainerPort: port}},
									Env:   env,
								},
							},
						},
					},
				},
			},
		},
	}
}

// ensureKnativeService creates or updates the Knative Service for a knative-served
// Deco. Idempotent: safe to call on every successful reconcile. The Deco owns the
// Service (cascade delete). Only the mutable template is updated so unrelated
// Knative-managed fields are preserved.
func (r *DecoReconciler) ensureKnativeService(ctx context.Context, deco *decositesv1alpha1.Deco) error {
	if deco.Spec.Serving == nil || deco.Spec.Serving.Type != "knative" {
		return nil
	}
	if deco.Spec.Build == nil || deco.Spec.Build.Source.CommitSha == "" {
		return nil
	}
	if r.KnativeServing.RunnerImage == "" {
		return fmt.Errorf("knative serving: NODE_RUNNER_IMAGE is not configured")
	}

	desired := BuildKnativeService(deco, r.KnativeServing)
	svc := &servingv1.Service{ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace}}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.Labels = desired.Labels
		svc.Spec = desired.Spec
		return controllerutil.SetControllerReference(deco, svc, r.Scheme)
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("knative serving: upsert service: %w", err)
	}
	return nil
}

package deploy

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
)

// ServingCloudflareWorker is the deco.spec.serving.type this target handles.
const ServingCloudflareWorker = "cloudflare-worker"

// cloudflareWorkersTarget is the DeploymentTarget for Cloudflare Workers / TanStack
// sites. On a content-only push to a fast-deploy-enabled site it emits a Decofile
// CR with target=tanstack-kv; the FastDeployment side turns that into a KV-sync Job.
type cloudflareWorkersTarget struct{}

// NewCloudflareWorkersTarget returns the DeploymentTarget for
// serving.type = "cloudflare-worker".
func NewCloudflareWorkersTarget() DeploymentTarget {
	return &cloudflareWorkersTarget{}
}

func (t *cloudflareWorkersTarget) Plan(_ context.Context, push PushEvent, site SiteConfig) ([]client.Object, error) {
	deco := site.Deco
	if deco == nil || deco.Spec.FastDeploy == nil || !deco.Spec.FastDeploy.Enabled {
		// Fast-deploy not configured/enabled for this site → nothing to do.
		return nil, nil
	}
	if !isContentOnly(push.ChangedFiles) {
		// Code (or mixed) change → normal build/deploy path owns it.
		return nil, nil
	}

	fd := deco.Spec.FastDeploy
	df := &decositesv1alpha1.Decofile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      decofileName(deco.Spec.Site),
			Namespace: deco.Namespace,
			Labels: map[string]string{
				"app.deco/site":    deco.Spec.Site,
				"app.deco/org":     deco.Spec.Org,
				"app.deco/serving": ServingCloudflareWorker,
			},
		},
		Spec: decositesv1alpha1.DecofileSpec{
			Source: decositesv1alpha1.SourceGitHub,
			GitHub: &decositesv1alpha1.GitHubSource{
				Org:    push.Owner,
				Repo:   push.Repo,
				Commit: push.Commit,
				// Trailing slash so prefix-based extraction can't match sibling
				// dirs like `.deco/blocks-old`.
				Path: ".deco/blocks/",
			},
			Target: decositesv1alpha1.TargetTanstackKV,
			TanstackKV: &decositesv1alpha1.TanstackKVTarget{
				KVNamespaceID: fd.KVNamespaceID,
				SiteOrigin:    fd.SiteOrigin,
			},
		},
	}
	return []client.Object{df}, nil
}

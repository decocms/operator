package build

import (
	"context"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
)

// Factory builds a *batchv1.Job for a given Deco and build source.
type Factory func(ctx context.Context, deco *decositesv1alpha1.Deco, jobName string, source decositesv1alpha1.DecoSpecBuildSource) (*batchv1.Job, error)

// Registry maps serving types to their job factories.
type Registry struct {
	platforms map[string]Factory
}

func NewRegistry() *Registry {
	return &Registry{platforms: map[string]Factory{}}
}

func (r *Registry) Register(servingType string, f Factory) {
	r.platforms[servingType] = f
}

func (r *Registry) NewJob(ctx context.Context, deco *decositesv1alpha1.Deco, jobName string, source decositesv1alpha1.DecoSpecBuildSource) (*batchv1.Job, error) {
	f, ok := r.platforms[deco.Spec.Serving.Type]
	if !ok {
		return nil, fmt.Errorf("no factory registered for serving type %q", deco.Spec.Serving.Type)
	}
	return f(ctx, deco, jobName, source)
}

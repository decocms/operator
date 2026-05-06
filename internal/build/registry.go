package build

import (
	"context"
	"errors"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
)

var errNoFactory = errors.New("no builder registered for serving type")

// Builder creates a K8s Job for a given Deco workload and build source.
type Builder interface {
	NewJob(ctx context.Context, deco *decositesv1alpha1.Deco, jobName string, source decositesv1alpha1.DecoSpecBuildSource) (*batchv1.Job, error)
}

// Registry dispatches to the correct Builder by spec.serving.type.
// Registry itself satisfies Builder.
type Registry struct {
	platforms map[string]Builder
}

func NewRegistry() *Registry {
	return &Registry{platforms: map[string]Builder{}}
}

func (r *Registry) Register(servingType string, b Builder) {
	r.platforms[servingType] = b
}

func (r *Registry) NewJob(ctx context.Context, deco *decositesv1alpha1.Deco, jobName string, source decositesv1alpha1.DecoSpecBuildSource) (*batchv1.Job, error) {
	b, ok := r.platforms[deco.Spec.Serving.Type]
	if !ok {
		return nil, fmt.Errorf("%w %q", errNoFactory, deco.Spec.Serving.Type)
	}
	return b.NewJob(ctx, deco, jobName, source)
}

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

// builderKey composes the two orthogonal dimensions that select a builder:
// the hosting framework (spec.serving.type, e.g. knative) and the stack
// framework (spec.framework, e.g. tanstack). The same hosting framework runs
// different stacks (knative+tanstack vs knative+deno), so dispatch keys on both.
// An empty framework registers a hosting-framework-agnostic builder (fallback).
func builderKey(servingType, framework string) string {
	if framework == "" {
		return servingType
	}
	return servingType + "/" + framework
}

// BuilderRegistry dispatches to the correct Builder by (spec.serving.type,
// spec.framework). BuilderRegistry itself satisfies Builder.
type BuilderRegistry struct {
	platforms map[string]Builder
}

func NewBuilderRegistry() *BuilderRegistry {
	return &BuilderRegistry{platforms: map[string]Builder{}}
}

// Register a builder for a (servingType, framework) pair. Pass framework="" to
// register a hosting-framework-agnostic builder (used as fallback when no
// stack-specific builder matches).
func (r *BuilderRegistry) Register(servingType, framework string, b Builder) {
	r.platforms[builderKey(servingType, framework)] = b
}

func (r *BuilderRegistry) NewJob(ctx context.Context, deco *decositesv1alpha1.Deco, jobName string, source decositesv1alpha1.DecoSpecBuildSource) (*batchv1.Job, error) {
	st := deco.Spec.Serving.Type
	fw := deco.Spec.Framework
	// Prefer the stack-specific builder (e.g. knative/tanstack); fall back to
	// the framework-agnostic one (e.g. cloudflare-worker).
	b, ok := r.platforms[builderKey(st, fw)]
	if !ok {
		b, ok = r.platforms[st]
	}
	if !ok {
		return nil, fmt.Errorf("%w %q (framework %q)", errNoFactory, st, fw)
	}
	return b.NewJob(ctx, deco, jobName, source)
}

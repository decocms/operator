package deploy

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
)

// FastDeployment is the watcher-side seam: given a Decofile CR, drive it to its
// deployed effect (create child resources, report status). One implementation
// per Decofile target. The first impl is tanstack-kv (KV sync via a Job); the
// existing ConfigMap/Knative path can be extracted into a "configmap" impl
// behind this same interface later.
type FastDeployment interface {
	// Reconcile drives the Decofile toward its target state. It owns any child
	// resources it creates (via owner references) and updates the Decofile's
	// status. Returning a non-zero Result requeues (e.g. while a Job runs).
	Reconcile(ctx context.Context, c client.Client, scheme *runtime.Scheme, df *decositesv1alpha1.Decofile) (ctrl.Result, error)
}

// DeploymentRegistry dispatches to a FastDeployment by the Decofile's target.
type DeploymentRegistry struct {
	impls map[string]FastDeployment
}

func NewDeploymentRegistry() *DeploymentRegistry {
	return &DeploymentRegistry{impls: map[string]FastDeployment{}}
}

func (r *DeploymentRegistry) Register(target string, d FastDeployment) {
	r.impls[target] = d
}

// For returns the FastDeployment for a Decofile target. ok=false means no
// custom strategy is registered (the caller falls back to the default
// ConfigMap path).
func (r *DeploymentRegistry) For(target string) (FastDeployment, bool) {
	d, ok := r.impls[target]
	return d, ok
}

// errMisconfigured is returned when a Decofile selects a target but omits the
// config that target needs.
func errMisconfigured(target, reason string) error {
	return fmt.Errorf("decofile target %q misconfigured: %s", target, reason)
}

/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"context"
	"errors"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deco-sites/decofile-operator/internal/buildsecrets"
)

const (
	buildSecretsAnnotation      = "deco.sites/build-secrets-managed"
	buildSecretsAnnotationValue = "enabled"

	// DefaultBuildSecretsResyncPeriod is the safety-net interval at which
	// the reconciler re-fetches the upstream backend even when nothing
	// changed in the cluster — picks up out-of-band edits to AWS Secrets
	// Manager. Configurable via BUILD_SECRETS_RESYNC_PERIOD.
	DefaultBuildSecretsResyncPeriod = 15 * time.Minute
)

// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// BuildSecretsReconciler keeps the K8s Secret `build-secrets` in each
// opted-in site namespace aligned with the upstream backend (AWS
// Secrets Manager today; see buildsecrets.Source for the abstraction).
//
// Opt-in is the annotation `deco.sites/build-secrets-managed: enabled`
// on the Namespace. The reconciler is fully event-driven (no polling)
// but requeues at ResyncPeriod as a safety net for upstream edits the
// operator did not observe (manual aws CLI rotation, etc.).
//
// # State machine
//
//	annotation off       → ensure no operator-managed Secret exists
//	annotation on, no upstream → no Secret (status: upstream-missing)
//	annotation on, upstream    → Secret created/updated with data
//
// # Force-sync recipes
//
// Re-fetch ONE site from the upstream immediately (instead of waiting
// for ResyncPeriod):
//
//	kubectl annotate ns sites-<site> \
//	  deco.sites/build-secrets-sync=$(date +%s) --overwrite
//
// Re-fetch ALL managed sites at once:
//
//	kubectl annotate ns -l deco.sites/build-secrets-managed=enabled \
//	  deco.sites/build-secrets-sync=$(date +%s) --overwrite
type BuildSecretsReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	Source       buildsecrets.Source
	ResyncPeriod time.Duration
}

func (r *BuildSecretsReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithName("buildsecrets").WithValues("namespace", req.Name)

	// Strip the `sites-` prefix inline; we deliberately avoid the
	// valkey-specific helper that filters reserved usernames.
	site := strings.TrimPrefix(req.Name, siteNamespacePrefix)
	if site == req.Name || site == "" {
		return ctrl.Result{}, nil
	}

	ns := &corev1.Namespace{}
	if err := r.Get(ctx, req.NamespacedName, ns); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	optedIn := ns.Annotations[buildSecretsAnnotation] == buildSecretsAnnotationValue
	if !optedIn || !ns.DeletionTimestamp.IsZero() {
		if err := buildsecrets.Remove(ctx, r.Client, ns.Name); err != nil {
			if errors.Is(err, buildsecrets.ErrNotOwned) {
				log.Info("Secret exists without operator labels — leaving it alone", "site", site)
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if err := buildsecrets.Sync(ctx, r.Client, ns.Name, site, r.Source); err != nil {
		if errors.Is(err, buildsecrets.ErrNotOwned) {
			log.Info("Skipping unowned Secret build-secrets — operator will not adopt it", "site", site)
			return ctrl.Result{RequeueAfter: r.ResyncPeriod}, nil
		}
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: r.ResyncPeriod}, nil
}

func (r *BuildSecretsReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Map Secret events back to the parent Namespace so deletions or
	// out-of-band edits trigger a re-reconcile that restores state.
	secretToNamespace := handler.EnqueueRequestsFromMapFunc(
		func(_ context.Context, obj client.Object) []reconcile.Request {
			if obj.GetName() != buildsecrets.SecretName {
				return nil
			}
			return []reconcile.Request{
				{NamespacedName: types.NamespacedName{Name: obj.GetNamespace()}},
			}
		},
	)

	return ctrl.NewControllerManagedBy(mgr).
		Named("buildsecrets").
		For(&corev1.Namespace{}).
		Watches(&corev1.Secret{}, secretToNamespace).
		WithOptions(controller.Options{MaxConcurrentReconciles: 4}).
		Complete(r)
}

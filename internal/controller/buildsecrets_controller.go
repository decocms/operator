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
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
)

// +kubebuilder:rbac:groups=external-secrets.io,resources=externalsecrets,verbs=get;list;watch;create;update;patch;delete

// BuildSecretsReconciler keeps an ExternalSecret in sync with each
// opted-in site namespace. Opt-in is the annotation
// `deco.sites/build-secrets-managed: enabled` on the Namespace.
//
// Removing the annotation (or deleting the Namespace) deletes the EE,
// which cascades into the K8s Secret via ESO's `creationPolicy: Owner`.
// Builds revert silently because admin's envFrom is `optional: true`.
//
// # Force sync recipes
//
// Re-fetch ONE site from AWS Secrets Manager immediately, without
// waiting for the EE's refreshInterval:
//
//	kubectl annotate es build-secrets -n sites-<site> \
//	  force-sync=$(date +%s) --overwrite
//
// Re-fetch ALL managed sites at once:
//
//	kubectl get es -A -l deco.sites/feature=build-secrets -o name \
//	  | xargs -I{} kubectl annotate {} force-sync=$(date +%s) --overwrite
//
// Bump the operator (re-applies the EE spec template across all
// managed namespaces — use after editing this controller):
//
//	kubectl annotate ns -l deco.sites/build-secrets-managed=enabled \
//	  deco.sites/build-secrets-sync=$(date +%s) --overwrite
type BuildSecretsReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *BuildSecretsReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithName("buildsecrets").WithValues("namespace", req.Name)

	// Derive site from namespace by stripping the `sites-` prefix. We do NOT
	// reuse siteNameFromNamespace from namespace_controller.go because that
	// helper also filters out Valkey-reserved usernames ("default",
	// "redis-root"), which has no bearing on build-secrets reconciliation.
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
			return ctrl.Result{}, err
		}
		log.V(1).Info("ExternalSecret removed", "site", site)
		return ctrl.Result{}, nil
	}

	if err := buildsecrets.Ensure(ctx, r.Client, ns.Name, site); err != nil {
		return ctrl.Result{}, err
	}
	log.V(1).Info("ExternalSecret ensured", "site", site)
	return ctrl.Result{}, nil
}

func (r *BuildSecretsReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Map ExternalSecret events back to the parent Namespace so deletions
	// or out-of-band edits trigger a re-reconcile that restores the spec.
	esToNamespace := handler.EnqueueRequestsFromMapFunc(
		func(_ context.Context, obj client.Object) []reconcile.Request {
			if obj.GetName() != buildsecrets.SecretName {
				return nil
			}
			return []reconcile.Request{
				{NamespacedName: types.NamespacedName{Name: obj.GetNamespace()}},
			}
		},
	)

	es := &unstructured.Unstructured{}
	es.SetGroupVersionKind(buildsecrets.GVK)

	return ctrl.NewControllerManagedBy(mgr).
		Named("buildsecrets").
		For(&corev1.Namespace{}).
		Watches(es, esToNamespace).
		WithOptions(controller.Options{MaxConcurrentReconciles: 4}).
		Complete(r)
}

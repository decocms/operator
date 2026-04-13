/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	servingknativedevv1 "knative.dev/serving/pkg/apis/serving/v1"

	"github.com/deco-sites/decofile-operator/internal/valkey"
)

const (
	valkeyACLAnnotation    = "deco.sites/valkey-acl"
	valkeyACLFinalizer     = "deco.sites/valkey-acl"
	valkeySecretName       = "valkey-acl"
	valkeyProvisionedAnnot = "deco.sites/valkey-acl-provisioned"

	siteNamespacePrefix = "sites-"
)

// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=serving.knative.dev,resources=services,verbs=get;list;watch;update;patch

// DefaultResyncPeriod is the default interval at which the reconciler re-syncs
// ACL users to all Valkey nodes even when nothing changed. Configurable via
// VALKEY_ACL_RESYNC_PERIOD (e.g. "10m", "30m", "1h").
const DefaultResyncPeriod = 10 * time.Minute

// NamespaceReconciler provisions per-tenant Valkey ACL credentials for site namespaces.
// When a Namespace has the annotation "deco.sites/valkey-acl: true", the reconciler:
//   - Creates a Valkey ACL user restricted to the site's key prefix.
//   - Creates a K8s Secret "valkey-acl" in that namespace with the credentials.
//   - Patches the Knative Service to trigger a new Revision that picks up the Secret.
//   - Cleans up the Valkey ACL user when the namespace is deleted.
//
// # What triggers a reconcile
//
//   - Namespace created or annotated with deco.sites/valkey-acl: "true"
//   - Secret "valkey-acl" deleted in a managed namespace (via Watches)
//   - Periodic requeue (RequeueAfter: 10min) for self-healing after Valkey restarts
//
// To force an immediate full resync of all managed namespaces (e.g. after a Valkey
// failover), touch all annotated namespaces to trigger reconcile on each:
//
//	kubectl annotate ns -l deco.sites/valkey-acl=true \
//	  deco.sites/valkey-acl-sync=$(date +%s) --overwrite
//
// # ACL replication caveat
//
// Valkey does NOT replicate ACL commands (ACL SETUSER/DELUSER) to replicas.
// The operator runs ACL commands only on the current Sentinel master. This means:
//
//  1. After a Sentinel failover, the new master starts without per-tenant ACLs.
//     The periodic reconcile (10min) detects this and re-provisions all users.
//     During the recovery window, deco falls back to FILE_SYSTEM cache.
//
//  2. Read replicas used by pods (LOADER_CACHE_REDIS_READ_URL) also lack the
//     per-tenant ACL users. This is not enforced today (auth.enabled: false),
//     but MUST be addressed before enabling Valkey auth in production.
//
// TODO: when enabling auth, extend ValkeyClient to provision ACL SETUSER on
// all nodes (master + every replica), not only the Sentinel master.
type NamespaceReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	ValkeyClient valkey.Client
	ResyncPeriod time.Duration
}

// InitMetrics seeds the tenants_provisioned gauge from current cluster state.
// Must be called after the cache is synced (i.e. inside a Runnable or after mgr.Start).
func (r *NamespaceReconciler) InitMetrics(ctx context.Context) error {
	nsList := &corev1.NamespaceList{}
	if err := r.List(ctx, nsList); err != nil {
		return err
	}
	count := 0.0
	for _, ns := range nsList.Items {
		if ns.Annotations[valkeyACLAnnotation] != "true" {
			continue
		}
		secret := &corev1.Secret{}
		if err := r.Get(ctx, types.NamespacedName{Name: valkeySecretName, Namespace: ns.Name}, secret); err == nil {
			count++
		}
	}
	valkeyTenantsProvisioned.Set(count)
	return nil
}

// SetupWithManager registers the Namespace controller with a resync period for
// self-healing (recovers ACLs lost after a Valkey restart).
func (r *NamespaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Watch Secrets named "valkey-acl" and enqueue the parent Namespace.
	// Namespace is cluster-scoped so Owns() (which relies on owner references) cannot
	// be used across scopes. Instead we map Secret → Namespace by name.
	secretToNamespace := handler.EnqueueRequestsFromMapFunc(
		func(_ context.Context, obj client.Object) []reconcile.Request {
			if obj.GetName() != valkeySecretName {
				return nil
			}
			return []reconcile.Request{
				{NamespacedName: types.NamespacedName{Name: obj.GetNamespace()}},
			}
		},
	)

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Namespace{}).
		Watches(&corev1.Secret{}, secretToNamespace).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 4,
		}).
		Named("namespace-valkey").
		Complete(r)
}

func (r *NamespaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("namespace", req.Name)

	ns := &corev1.Namespace{}
	if err := r.Get(ctx, req.NamespacedName, ns); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Only process namespaces with the opt-in annotation.
	if ns.Annotations[valkeyACLAnnotation] != "true" {
		return ctrl.Result{}, nil
	}

	siteName := siteNameFromNamespace(ns.Name)

	// Handle deletion: remove the Valkey ACL user before the namespace is gone.
	if !ns.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(ns, valkeyACLFinalizer) {
			log.Info("Deleting Valkey ACL user", "user", siteName)
			if err := r.ValkeyClient.DeleteUser(ctx, siteName); err != nil {
				log.Error(err, "Failed to delete Valkey ACL user, will retry")
				valkeyACLErrors.WithLabelValues("delete").Inc()
				return ctrl.Result{}, err
			}
			valkeyACLDeleted.Inc()
			valkeyTenantsProvisioned.Dec()
			log.Info("Valkey ACL user deleted", "user", siteName)
			controllerutil.RemoveFinalizer(ns, valkeyACLFinalizer)
			if err := r.Update(ctx, ns); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Ensure finalizer is present so we can clean up on deletion.
	if !controllerutil.ContainsFinalizer(ns, valkeyACLFinalizer) {
		controllerutil.AddFinalizer(ns, valkeyACLFinalizer)
		if err := r.Update(ctx, ns); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Check whether the credential Secret already exists.
	secret := &corev1.Secret{}
	secretKey := types.NamespacedName{Name: valkeySecretName, Namespace: ns.Name}
	err := r.Get(ctx, secretKey, secret)

	switch {
	case errors.IsNotFound(err):
		// Secret does not exist: generate credentials, create ACL user and Secret.
		password, genErr := generatePassword()
		if genErr != nil {
			return ctrl.Result{}, fmt.Errorf("generate password: %w", genErr)
		}

		log.Info("Provisioning Valkey ACL user", "user", siteName)
		if upsertErr := r.ValkeyClient.UpsertUser(ctx, siteName, password); upsertErr != nil {
			valkeyACLErrors.WithLabelValues("upsert").Inc()
			return ctrl.Result{}, fmt.Errorf("upsert Valkey user: %w", upsertErr)
		}

		if createErr := r.createSecret(ctx, ns.Name, siteName, password); createErr != nil {
			return ctrl.Result{}, fmt.Errorf("create secret: %w", createErr)
		}

		valkeyACLProvisioned.Inc()
		valkeyTenantsProvisioned.Inc()
		log.Info("Valkey ACL provisioned", "user", siteName, "namespace", ns.Name)

		// Trigger a new Knative Revision so running pods pick up the new Secret.
		if patchErr := r.patchKnativeServiceTimestamp(ctx, ns.Name); patchErr != nil {
			log.Error(patchErr, "Failed to patch Knative Service (non-fatal)")
		}

	case err != nil:
		return ctrl.Result{}, fmt.Errorf("get secret: %w", err)

	default:
		// Secret exists. Always call UpsertUser on the periodic reconcile — it is
		// idempotent and ensures ACLs are present on ALL nodes (master + replicas).
		// Valkey does not replicate ACL commands, so a single UpsertUser call from
		// the operator is the only way to keep every node in sync. This also covers:
		//   - Valkey master restart (ACLs lost from memory)
		//   - Replica replacement or restart
		//   - Sentinel failover (new master starts without ACLs)
		password := string(secret.Data["LOADER_CACHE_REDIS_PASSWORD"])
		if upsertErr := r.ValkeyClient.UpsertUser(ctx, siteName, password); upsertErr != nil {
			valkeyACLErrors.WithLabelValues("upsert").Inc()
			return ctrl.Result{}, fmt.Errorf("sync Valkey user: %w", upsertErr)
		}
		log.V(1).Info("Valkey ACL synced to all nodes", "user", siteName)
	}

	// Requeue periodically to sync ACLs to all Valkey nodes.
	return ctrl.Result{RequeueAfter: r.ResyncPeriod}, nil
}

// createSecret creates the "valkey-acl" Secret in the given namespace with
// credentials ready to be consumed by deco via LOADER_CACHE_REDIS_USERNAME/PASSWORD.
func (r *NamespaceReconciler) createSecret(ctx context.Context, namespace, username, password string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      valkeySecretName,
			Namespace: namespace,
		},
		StringData: map[string]string{
			"LOADER_CACHE_REDIS_USERNAME": username,
			"LOADER_CACHE_REDIS_PASSWORD": password,
		},
	}
	return r.Create(ctx, secret)
}

// patchKnativeServiceTimestamp adds/updates the "deco.sites/valkey-acl-provisioned"
// annotation on every Knative Service in the namespace. This causes Knative to create
// a new Revision whose pods will mount the just-created valkey-acl Secret.
func (r *NamespaceReconciler) patchKnativeServiceTimestamp(ctx context.Context, namespace string) error {
	log := logf.FromContext(ctx)

	svcList := &servingknativedevv1.ServiceList{}
	if err := r.List(ctx, svcList, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("list Knative Services: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	for i := range svcList.Items {
		svc := &svcList.Items[i]
		patch := client.MergeFrom(svc.DeepCopy())
		// Must annotate spec.template, not metadata — Knative only creates a new
		// Revision when spec.template changes.
		if svc.Spec.Template.Annotations == nil {
			svc.Spec.Template.Annotations = make(map[string]string)
		}
		svc.Spec.Template.Annotations[valkeyProvisionedAnnot] = now
		if err := r.Patch(ctx, svc, patch); err != nil {
			log.Error(err, "Failed to patch Knative Service", "service", svc.Name)
		}
	}
	return nil
}

// siteNameFromNamespace derives the Valkey ACL username from the K8s namespace name.
// The "sites-" prefix is stripped when present so the username matches DECO_SITE_NAME.
func siteNameFromNamespace(namespace string) string {
	return strings.TrimPrefix(namespace, siteNamespacePrefix)
}

// generatePassword produces a cryptographically random 32-byte URL-safe base64 string.
func generatePassword() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

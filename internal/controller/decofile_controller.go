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
	"fmt"
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
)

// DecofileReconciler reconciles a Decofile object
type DecofileReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=deco.sites,resources=decofiles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=deco.sites,resources=decofiles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=deco.sites,resources=decofiles/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *DecofileReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the Decofile instance
	decofile := &decositesv1alpha1.Decofile{}
	err := r.Get(ctx, req.NamespacedName, decofile)
	if err != nil {
		if errors.IsNotFound(err) {
			// Decofile was deleted, nothing to do (ConfigMap will be garbage collected via owner reference)
			log.Info("Decofile resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get Decofile")
		return ctrl.Result{}, err
	}

	// Define the ConfigMap name
	configMapName := fmt.Sprintf("decofile-%s", decofile.Name)

	// Get the appropriate source implementation
	source, err := NewSource(r.Client, decofile)
	if err != nil {
		log.Error(err, "Failed to create source")
		return ctrl.Result{}, err
	}

	// Retrieve configuration data from source (single JSON string)
	jsonContent, err := source.Retrieve(ctx)
	if err != nil {
		log.Error(err, "Failed to retrieve data from source")
		return ctrl.Result{}, err
	}

	sourceType := source.SourceType()

	// Define the desired ConfigMap with single decofile.json key
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: decofile.Namespace,
		},
		Data: map[string]string{
			"decofile.json": jsonContent,
		},
	}

	// Set Decofile instance as the owner of the ConfigMap
	if err := controllerutil.SetControllerReference(decofile, configMap, r.Scheme); err != nil {
		log.Error(err, "Failed to set owner reference on ConfigMap")
		return ctrl.Result{}, err
	}

	// Check if the ConfigMap already exists and detect changes
	found := &corev1.ConfigMap{}
	err = r.Get(ctx, client.ObjectKey{Name: configMapName, Namespace: decofile.Namespace}, found)

	var dataChanged bool

	if err != nil && errors.IsNotFound(err) {
		log.Info("Creating a new ConfigMap", "ConfigMap.Namespace", configMap.Namespace, "ConfigMap.Name", configMap.Name)
		err = r.Create(ctx, configMap)
		if err != nil {
			log.Error(err, "Failed to create new ConfigMap", "ConfigMap.Namespace", configMap.Namespace, "ConfigMap.Name", configMap.Name)
			return ctrl.Result{}, err
		}
		dataChanged = false // New ConfigMap, no notification needed
	} else if err != nil {
		log.Error(err, "Failed to get ConfigMap")
		return ctrl.Result{}, err
	} else {
		// ConfigMap exists, check if data changed
		dataChanged = !reflect.DeepEqual(found.Data, configMap.Data)

		if dataChanged {
			log.Info("ConfigMap data changed, updating", "ConfigMap.Name", found.Name)
			found.Data = configMap.Data
			err = r.Update(ctx, found)
			if err != nil {
				log.Error(err, "Failed to update ConfigMap", "ConfigMap.Namespace", found.Namespace, "ConfigMap.Name", found.Name)
				return ctrl.Result{}, err
			}
			log.Info("Updated existing ConfigMap", "ConfigMap.Namespace", found.Namespace, "ConfigMap.Name", found.Name)
		} else {
			log.V(1).Info("ConfigMap data unchanged, skipping update", "ConfigMap.Name", found.Name)
		}
	}

	// Notify pods if ConfigMap data changed
	if dataChanged {
		log.Info("ConfigMap data changed, notifying pods")

		notifier := NewNotifier(r.Client)
		err = notifier.NotifyPodsForDecofile(ctx, decofile.Namespace, decofile.Name)
		if err != nil {
			log.Error(err, "Failed to notify pods", "decofile", decofile.Name)
			return ctrl.Result{}, fmt.Errorf("failed to notify pods: %w", err)
		}

		log.Info("Successfully notified all pods")
	}

	// Re-fetch the Decofile to get the latest version before updating status
	// This prevents conflicts if the object was modified during reconciliation
	freshDecofile := &decositesv1alpha1.Decofile{}
	err = r.Get(ctx, req.NamespacedName, freshDecofile)
	if err != nil {
		log.Error(err, "Failed to re-fetch Decofile for status update")
		return ctrl.Result{}, err
	}

	// Update Decofile status
	freshDecofile.Status.ConfigMapName = configMapName
	freshDecofile.Status.LastUpdated = metav1.Time{Time: time.Now()}
	freshDecofile.Status.SourceType = sourceType

	// Store GitHub commit if using GitHub source
	if freshDecofile.Spec.Source == SourceTypeGitHub && freshDecofile.Spec.GitHub != nil {
		freshDecofile.Status.GitHubCommit = freshDecofile.Spec.GitHub.Commit
	}

	// Update the status condition
	conditionMessage := fmt.Sprintf("ConfigMap %s created successfully from %s source", configMapName, sourceType)
	condition := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "ConfigMapCreated",
		Message:            conditionMessage,
		LastTransitionTime: metav1.Now(),
	}

	// Update or append condition
	foundCondition := false
	for i, cond := range freshDecofile.Status.Conditions {
		if cond.Type == "Ready" {
			freshDecofile.Status.Conditions[i] = condition
			foundCondition = true
			break
		}
	}
	if !foundCondition {
		freshDecofile.Status.Conditions = append(freshDecofile.Status.Conditions, condition)
	}

	err = r.Status().Update(ctx, freshDecofile)
	if err != nil {
		log.Error(err, "Failed to update Decofile status")
		return ctrl.Result{}, err
	}

	log.Info("Successfully reconciled Decofile")
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DecofileReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&decositesv1alpha1.Decofile{}).
		Owns(&corev1.ConfigMap{}).
		Named("decofile").
		Complete(r)
}

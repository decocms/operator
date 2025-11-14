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
	"encoding/base64"
	"fmt"
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

const (
	// Compression threshold: 2.5MB (ConfigMap limit is 3MB, leave buffer)
	compressionThreshold = 2.5 * 1024 * 1024
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

	// Prepare ConfigMap data with optional compression
	var configData map[string]string
	var contentKey string // "decofile.json" or "decofile.bin"

	if len(jsonContent) > compressionThreshold {
		// Compress large content with Brotli
		compressed, err := compressBrotli([]byte(jsonContent))
		if err != nil {
			log.Error(err, "Failed to compress config")
			return ctrl.Result{}, fmt.Errorf("failed to compress config: %w", err)
		}

		configData = map[string]string{
			"decofile.bin": base64.StdEncoding.EncodeToString(compressed),
		}
		contentKey = "decofile.bin"

		compressionRatio := float64(len(compressed)) / float64(len(jsonContent)) * 100
		log.Info("Compressed large config",
			"originalSize", len(jsonContent),
			"compressedSize", len(compressed),
			"ratio", fmt.Sprintf("%.1f%%", compressionRatio))
	} else {
		// Store uncompressed for small configs
		configData = map[string]string{
			"decofile.json": jsonContent,
		}
		contentKey = "decofile.json"
	}

	// Check if the ConfigMap already exists
	found := &corev1.ConfigMap{}
	err = r.Get(ctx, client.ObjectKey{Name: configMapName, Namespace: decofile.Namespace}, found)

	var dataChanged bool
	var timestamp string

	if err != nil && errors.IsNotFound(err) {
		// New ConfigMap - create with new timestamp (Unix seconds)
		timestamp = fmt.Sprintf("%d", time.Now().Unix())
		dataChanged = false // New ConfigMap, no notification needed

		// Add timestamp
		configData["timestamp.txt"] = timestamp

		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMapName,
				Namespace: decofile.Namespace,
			},
			Data: configData,
		}

		if err := controllerutil.SetControllerReference(decofile, configMap, r.Scheme); err != nil {
			log.Error(err, "Failed to set owner reference on ConfigMap")
			return ctrl.Result{}, err
		}

		log.Info("Creating a new ConfigMap", "ConfigMap.Namespace", configMap.Namespace, "ConfigMap.Name", configMap.Name, "timestamp", timestamp)
		err = r.Create(ctx, configMap)
		if err != nil {
			log.Error(err, "Failed to create new ConfigMap", "ConfigMap.Namespace", configMap.Namespace, "ConfigMap.Name", configMap.Name)
			return ctrl.Result{}, err
		}
	} else if err != nil {
		log.Error(err, "Failed to get ConfigMap")
		return ctrl.Result{}, err
	} else {
		// ConfigMap exists - check if content changed
		// Determine what key the existing ConfigMap uses
		var existingKey string
		if _, hasBin := found.Data["decofile.bin"]; hasBin {
			existingKey = "decofile.bin"
		} else {
			existingKey = "decofile.json"
		}

		// Check if format changed (compressed <-> uncompressed) or content changed
		formatChanged := existingKey != contentKey
		contentChanged := found.Data[existingKey] != configData[contentKey]
		dataChanged = formatChanged || contentChanged

		if formatChanged {
			log.Info("ConfigMap format changed", "from", existingKey, "to", contentKey)
		}

		if dataChanged {
			// Content changed - update with new timestamp (Unix seconds)
			timestamp = fmt.Sprintf("%d", time.Now().Unix())
			log.Info("ConfigMap content changed, updating", "ConfigMap.Name", found.Name, "newTimestamp", timestamp)

			// Replace all data
			found.Data = configData
			found.Data["timestamp.txt"] = timestamp

			err = r.Update(ctx, found)
			if err != nil {
				log.Error(err, "Failed to update ConfigMap", "ConfigMap.Namespace", found.Namespace, "ConfigMap.Name", found.Name)
				return ctrl.Result{}, err
			}
			log.Info("Updated existing ConfigMap", "ConfigMap.Namespace", found.Namespace, "ConfigMap.Name", found.Name)
		} else {
			// Content unchanged - keep existing timestamp
			timestamp = found.Data["timestamp.txt"]
			log.V(1).Info("ConfigMap content unchanged, keeping existing timestamp", "ConfigMap.Name", found.Name)
		}
	}

	// Notify pods if ConfigMap data changed
	if dataChanged {
		log.Info("ConfigMap data changed, notifying pods", "timestamp", timestamp)

		notifier := NewNotifier(r.Client)
		err = notifier.NotifyPodsForDecofile(ctx, decofile.Namespace, decofile.Name, timestamp)
		if err != nil {
			log.Error(err, "Failed to notify pods", "decofile", decofile.Name)
			return ctrl.Result{}, fmt.Errorf("failed to notify pods: %w", err)
		}

		log.Info("Successfully notified all pods", "timestamp", timestamp)
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

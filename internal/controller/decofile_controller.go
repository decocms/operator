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
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
	"github.com/deco-sites/decofile-operator/internal/github"
)

const (
	sourceTypeInline = "inline"
	sourceTypeGitHub = "github"
)

// DecofileReconciler reconciles a Decofile object
type DecofileReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=deco.sites.deco.sites,resources=decofiles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=deco.sites.deco.sites,resources=decofiles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=deco.sites.deco.sites,resources=decofiles/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

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

	// Prepare ConfigMap data based on source type
	var configMapData map[string]string
	var sourceType string

	switch decofile.Spec.Source {
	case sourceTypeInline:
		if decofile.Spec.Inline == nil {
			err := fmt.Errorf("inline source specified but no inline data provided")
			log.Error(err, "Invalid Decofile spec")
			return ctrl.Result{}, err
		}

		configMapData = make(map[string]string)
		for key, rawExt := range decofile.Spec.Inline.Value {
			// Convert RawExtension to JSON string
			jsonBytes, err := json.Marshal(rawExt.Raw)
			if err != nil {
				log.Error(err, "Failed to marshal value for key", "key", key)
				return ctrl.Result{}, err
			}
			configMapData[key] = string(jsonBytes)
		}
		sourceType = sourceTypeInline

	case sourceTypeGitHub:
		if decofile.Spec.GitHub == nil {
			err := fmt.Errorf("github source specified but no github config provided")
			log.Error(err, "Invalid Decofile spec")
			return ctrl.Result{}, err
		}

		// Fetch GitHub token from secret
		secret := &corev1.Secret{}
		err := r.Get(ctx, types.NamespacedName{
			Name:      decofile.Spec.GitHub.Secret,
			Namespace: decofile.Namespace,
		}, secret)
		if err != nil {
			log.Error(err, "Failed to get GitHub secret", "secret", decofile.Spec.GitHub.Secret)
			return ctrl.Result{}, fmt.Errorf("failed to get secret %s: %w", decofile.Spec.GitHub.Secret, err)
		}

		token := string(secret.Data["token"])
		if token == "" {
			err := fmt.Errorf("secret %s does not contain 'token' key", decofile.Spec.GitHub.Secret)
			log.Error(err, "Invalid secret")
			return ctrl.Result{}, err
		}

		// Download and extract from GitHub
		log.Info("Downloading from GitHub",
			"org", decofile.Spec.GitHub.Org,
			"repo", decofile.Spec.GitHub.Repo,
			"commit", decofile.Spec.GitHub.Commit,
			"path", decofile.Spec.GitHub.Path)

		downloader := &github.Downloader{Token: token}
		files, err := downloader.DownloadAndExtract(
			decofile.Spec.GitHub.Org,
			decofile.Spec.GitHub.Repo,
			decofile.Spec.GitHub.Commit,
			decofile.Spec.GitHub.Path,
		)
		if err != nil {
			log.Error(err, "Failed to download from GitHub")
			return ctrl.Result{}, fmt.Errorf("failed to download from github: %w", err)
		}

		// Convert bytes to strings
		configMapData = make(map[string]string)
		for filename, content := range files {
			configMapData[filename] = string(content)
		}
		sourceType = sourceTypeGitHub

		log.Info("Successfully downloaded from GitHub", "files", len(files))

	default:
		err := fmt.Errorf("unknown source type: %s (must be '%s' or '%s')", decofile.Spec.Source, sourceTypeInline, sourceTypeGitHub)
		log.Error(err, "Invalid source type")
		return ctrl.Result{}, err
	}

	// Define the desired ConfigMap
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: decofile.Namespace,
		},
		Data: configMapData,
	}

	// Set Decofile instance as the owner of the ConfigMap
	if err := controllerutil.SetControllerReference(decofile, configMap, r.Scheme); err != nil {
		log.Error(err, "Failed to set owner reference on ConfigMap")
		return ctrl.Result{}, err
	}

	// Check if the ConfigMap already exists
	found := &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: decofile.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		log.Info("Creating a new ConfigMap", "ConfigMap.Namespace", configMap.Namespace, "ConfigMap.Name", configMap.Name)
		err = r.Create(ctx, configMap)
		if err != nil {
			log.Error(err, "Failed to create new ConfigMap", "ConfigMap.Namespace", configMap.Namespace, "ConfigMap.Name", configMap.Name)
			return ctrl.Result{}, err
		}
	} else if err != nil {
		log.Error(err, "Failed to get ConfigMap")
		return ctrl.Result{}, err
	} else {
		// ConfigMap exists, update it
		found.Data = configMapData
		err = r.Update(ctx, found)
		if err != nil {
			log.Error(err, "Failed to update ConfigMap", "ConfigMap.Namespace", found.Namespace, "ConfigMap.Name", found.Name)
			return ctrl.Result{}, err
		}
		log.Info("Updated existing ConfigMap", "ConfigMap.Namespace", found.Namespace, "ConfigMap.Name", found.Name)
	}

	// Update Decofile status
	decofile.Status.ConfigMapName = configMapName
	decofile.Status.LastUpdated = metav1.Time{Time: time.Now()}
	decofile.Status.SourceType = sourceType

	// Store GitHub commit if using GitHub source
	if decofile.Spec.Source == sourceTypeGitHub && decofile.Spec.GitHub != nil {
		decofile.Status.GitHubCommit = decofile.Spec.GitHub.Commit
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
	for i, cond := range decofile.Status.Conditions {
		if cond.Type == "Ready" {
			decofile.Status.Conditions[i] = condition
			foundCondition = true
			break
		}
	}
	if !foundCondition {
		decofile.Status.Conditions = append(decofile.Status.Conditions, condition)
	}

	err = r.Status().Update(ctx, decofile)
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

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

package v1

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	servingknativedevv1 "knative.dev/serving/pkg/apis/serving/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
)

const (
	appContainerName       = "app"
	reloadTokenEnvVar      = "DECO_RELEASE_RELOAD_TOKEN"
	decoReleaseEnvVar      = "DECO_RELEASE"
	decofileInjectAnnot    = "deco.sites/decofile-inject"
	decofileMountPathAnnot = "deco.sites/decofile-mount-path"
	deploymentIdLabel      = "app.deco/deploymentId"
)

// nolint:unused
// log is for logging in this package.
var servicelog = logf.Log.WithName("service-resource")

// +kubebuilder:rbac:groups=deco.sites,resources=decofiles,verbs=get;list;watch

// SetupServiceWebhookWithManager registers the webhook for Service in the manager.
func SetupServiceWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&servingknativedevv1.Service{}).
		WithDefaulter(&ServiceCustomDefaulter{Client: mgr.GetClient()}).
		WithValidator(&ServiceCustomValidator{}).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// +kubebuilder:webhook:path=/mutate-serving-knative-dev-v1-service,mutating=true,failurePolicy=fail,sideEffects=None,groups=serving.knative.dev,resources=services,verbs=create;update,versions=v1,name=mservice-v1.kb.io,admissionReviewVersions=v1

// ServiceCustomDefaulter struct is responsible for setting default values on the Service resource
// when it is created or updated.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type ServiceCustomDefaulter struct {
	Client client.Client
}

var _ webhook.CustomDefaulter = &ServiceCustomDefaulter{}

// getDeploymentId extracts deploymentId from Service labels
func (d *ServiceCustomDefaulter) getDeploymentId(service *servingknativedevv1.Service) (string, error) {
	if service.Labels == nil {
		return "", fmt.Errorf("service has deco.sites/decofile-inject annotation but no labels")
	}

	deploymentId, exists := service.Labels[deploymentIdLabel]
	if !exists || deploymentId == "" {
		return "", fmt.Errorf("service has deco.sites/decofile-inject annotation but no app.deco/deploymentId label")
	}

	return deploymentId, nil
}

// findDecofileByDeploymentId finds a Decofile matching the given deploymentId
func (d *ServiceCustomDefaulter) findDecofileByDeploymentId(ctx context.Context, namespace, deploymentId string) (*decositesv1alpha1.Decofile, error) {
	decofileList := &decositesv1alpha1.DecofileList{}
	err := d.Client.List(ctx, decofileList, client.InNamespace(namespace))
	if err != nil {
		return nil, fmt.Errorf("failed to list Decofiles: %w", err)
	}

	for i := range decofileList.Items {
		df := &decofileList.Items[i]
		dfDeploymentId := df.Spec.DeploymentId
		if dfDeploymentId == "" {
			dfDeploymentId = df.Name
		}
		if dfDeploymentId == deploymentId {
			return df, nil
		}
	}

	return nil, fmt.Errorf("no Decofile found with deploymentId %s in namespace %s", deploymentId, namespace)
}

// injectDecofileVolume injects the Decofile ConfigMap as a volume into the Service
func (d *ServiceCustomDefaulter) injectDecofileVolume(ctx context.Context, service *servingknativedevv1.Service, decofile *decositesv1alpha1.Decofile, mountDir string) error {
	// Check if ConfigMap is compressed to set correct file extension
	configMap := &corev1.ConfigMap{}
	err := d.Client.Get(ctx, types.NamespacedName{
		Name:      decofile.Status.ConfigMapName,
		Namespace: service.Namespace,
	}, configMap)

	fileExtension := "json"
	if err == nil {
		if _, hasCompressed := configMap.Data["decofile.bin"]; hasCompressed {
			fileExtension = "bin"
		}
	}

	// Create DECO_RELEASE environment variable
	decoReleaseValue := fmt.Sprintf("file://%s/decofile.%s", mountDir, fileExtension)

	// Ensure volumes array exists
	if service.Spec.Template.Spec.Volumes == nil {
		service.Spec.Template.Spec.Volumes = []corev1.Volume{}
	}

	// Add or update volume
	d.addOrUpdateVolume(service, decofile.Status.ConfigMapName)

	// Find target container and add volumeMount + env vars
	if len(service.Spec.Template.Spec.Containers) == 0 {
		return fmt.Errorf("no containers found in Service spec")
	}

	targetContainerIdx := d.findTargetContainer(service)
	d.addOrUpdateVolumeMount(service, targetContainerIdx, mountDir)
	d.addOrUpdateEnvVars(service, targetContainerIdx, decoReleaseValue)

	return nil
}

// addOrUpdateVolume adds or updates the decofile volume
func (d *ServiceCustomDefaulter) addOrUpdateVolume(service *servingknativedevv1.Service, configMapName string) {
	volumeName := "decofile-config"
	volumeExists := false

	for i, vol := range service.Spec.Template.Spec.Volumes {
		if vol.Name == volumeName {
			service.Spec.Template.Spec.PodSpec.Volumes[i].VolumeSource = corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
				},
			}
			volumeExists = true
			break
		}
	}

	if !volumeExists {
		service.Spec.Template.Spec.Volumes = append(service.Spec.Template.Spec.Volumes, corev1.Volume{
			Name: volumeName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
				},
			},
		})
	}
}

// findTargetContainer finds the "app" container or returns 0
func (d *ServiceCustomDefaulter) findTargetContainer(service *servingknativedevv1.Service) int {
	for i, container := range service.Spec.Template.Spec.Containers {
		if container.Name == appContainerName {
			return i
		}
	}
	return 0
}

// addOrUpdateVolumeMount adds or updates the volume mount
func (d *ServiceCustomDefaulter) addOrUpdateVolumeMount(service *servingknativedevv1.Service, containerIdx int, mountDir string) {
	volumeName := "decofile-config"
	mountExists := false

	for i, mount := range service.Spec.Template.Spec.PodSpec.Containers[containerIdx].VolumeMounts {
		if mount.Name == volumeName {
			service.Spec.Template.Spec.PodSpec.Containers[containerIdx].VolumeMounts[i].MountPath = mountDir
			service.Spec.Template.Spec.PodSpec.Containers[containerIdx].VolumeMounts[i].SubPath = ""
			mountExists = true
			break
		}
	}

	if !mountExists {
		service.Spec.Template.Spec.PodSpec.Containers[containerIdx].VolumeMounts = append(
			service.Spec.Template.Spec.PodSpec.Containers[containerIdx].VolumeMounts,
			corev1.VolumeMount{
				Name:      volumeName,
				MountPath: mountDir,
				ReadOnly:  true,
			},
		)
	}
}

// addOrUpdateEnvVars adds or updates environment variables
func (d *ServiceCustomDefaulter) addOrUpdateEnvVars(service *servingknativedevv1.Service, containerIdx int, decoReleaseValue string) {
	// Add DECO_RELEASE environment variable
	envExists := false
	for i, env := range service.Spec.Template.Spec.PodSpec.Containers[containerIdx].Env {
		if env.Name == decoReleaseEnvVar {
			service.Spec.Template.Spec.PodSpec.Containers[containerIdx].Env[i].Value = decoReleaseValue
			envExists = true
			break
		}
	}

	if !envExists {
		service.Spec.Template.Spec.PodSpec.Containers[containerIdx].Env = append(
			service.Spec.Template.Spec.PodSpec.Containers[containerIdx].Env,
			corev1.EnvVar{Name: decoReleaseEnvVar, Value: decoReleaseValue},
		)
	}

	// Add DECO_RELEASE_RELOAD_TOKEN environment variable
	reloadToken := uuid.New().String()
	tokenEnvExists := false
	for i, env := range service.Spec.Template.Spec.PodSpec.Containers[containerIdx].Env {
		if env.Name == reloadTokenEnvVar {
			service.Spec.Template.Spec.PodSpec.Containers[containerIdx].Env[i].Value = reloadToken
			tokenEnvExists = true
			break
		}
	}

	if !tokenEnvExists {
		service.Spec.Template.Spec.PodSpec.Containers[containerIdx].Env = append(
			service.Spec.Template.Spec.PodSpec.Containers[containerIdx].Env,
			corev1.EnvVar{Name: reloadTokenEnvVar, Value: reloadToken},
		)
	}
}

// Default implements webhook.CustomDefaulter so a webhook will be registered for the type Service.
func (d *ServiceCustomDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	service, ok := obj.(*servingknativedevv1.Service)
	if !ok {
		return nil // do nothing
	}
	servicelog.Info("Mutating Service", "name", service.GetName())

	// Check for deco.sites/decofile-inject annotation (boolean)
	if service.Annotations == nil {
		return nil
	}

	injectAnnotation, exists := service.Annotations[decofileInjectAnnot]
	if !exists || injectAnnotation != "true" {
		return nil
	}

	// Get deploymentId from Service labels
	deploymentId, err := d.getDeploymentId(service)
	if err != nil {
		return err
	}

	// Find matching Decofile (non-blocking - allow Service creation even if not found)
	decofile, err := d.findDecofileByDeploymentId(ctx, service.Namespace, deploymentId)
	if err != nil {
		servicelog.Info("Decofile not found, skipping injection (Service will be created without Decofile)",
			"service", service.Name, "deploymentId", deploymentId, "reason", err.Error())
		return nil // Allow Service creation
	}

	// Check if ConfigMap is ready (non-blocking)
	if decofile.Status.ConfigMapName == "" {
		servicelog.Info("Decofile ConfigMap not ready yet, skipping injection",
			"service", service.Name, "decofile", decofile.Name)
		return nil // Allow Service creation
	}

	// Get mount path from annotation or use default directory
	mountDir := "/app/decofile"
	if customPath, exists := service.Annotations[decofileMountPathAnnot]; exists {
		mountDir = customPath
	}

	// Inject Decofile volume and env vars
	if err := d.injectDecofileVolume(ctx, service, decofile, mountDir); err != nil {
		return err
	}

	// Explicitly add deploymentId label to pod template for notification
	// (Don't rely on Knative label propagation)
	if service.Spec.Template.Labels == nil {
		service.Spec.Template.Labels = make(map[string]string)
	}
	service.Spec.Template.Labels[deploymentIdLabel] = deploymentId

	servicelog.Info("Successfully injected Decofile into Service", "service", service.Name, "deploymentId", deploymentId, "configmap", decofile.Status.ConfigMapName)

	return nil
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
// NOTE: The 'path' attribute must follow a specific pattern and should not be modified directly here.
// Modifying the path for an invalid path can cause API server errors; failing to locate the webhook.
// +kubebuilder:webhook:path=/validate-serving-knative-dev-v1-service,mutating=false,failurePolicy=fail,sideEffects=None,groups=serving.knative.dev,resources=services,verbs=create;update,versions=v1,name=vservice-v1.kb.io,admissionReviewVersions=v1

// ServiceCustomValidator struct is responsible for validating the Service resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type ServiceCustomValidator struct {
	// TODO(user): Add more fields as needed for validation
}

var _ webhook.CustomValidator = &ServiceCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type Service.
func (v *ServiceCustomValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	service, ok := obj.(*servingknativedevv1.Service)
	if !ok {
		return nil, fmt.Errorf("expected a Service object but got %T", obj)
	}
	servicelog.Info("Validation for Service upon creation", "name", service.GetName())

	// TODO(user): fill in your validation logic upon object creation.

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type Service.
func (v *ServiceCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	service, ok := newObj.(*servingknativedevv1.Service)
	if !ok {
		return nil, fmt.Errorf("expected a Service object for the newObj but got %T", newObj)
	}
	servicelog.Info("Validation for Service upon update", "name", service.GetName())

	// TODO(user): fill in your validation logic upon object update.

	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type Service.
func (v *ServiceCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	service, ok := obj.(*servingknativedevv1.Service)
	if !ok {
		return nil, fmt.Errorf("expected a Service object but got %T", obj)
	}
	servicelog.Info("Validation for Service upon deletion", "name", service.GetName())

	// TODO(user): fill in your validation logic upon object deletion.

	return nil, nil
}

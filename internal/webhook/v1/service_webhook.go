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

// nolint:unused
// log is for logging in this package.
var servicelog = logf.Log.WithName("service-resource")

// +kubebuilder:rbac:groups=deco.sites.deco.sites,resources=decofiles,verbs=get;list;watch

// SetupServiceWebhookWithManager registers the webhook for Service in the manager.
func SetupServiceWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&servingknativedevv1.Service{}).
		WithDefaulter(&ServiceCustomDefaulter{Client: mgr.GetClient()}).
		WithValidator(&ServiceCustomValidator{}).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// +kubebuilder:webhook:path=/mutate-serving-knative-dev-serving-knative-dev-v1-service,mutating=true,failurePolicy=fail,sideEffects=None,groups=serving.knative.dev.serving.knative.dev,resources=services,verbs=create;update,versions=v1,name=mservice-v1.kb.io,admissionReviewVersions=v1

// ServiceCustomDefaulter struct is responsible for setting default values on the Service resource
// when it is created or updated.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type ServiceCustomDefaulter struct {
	Client client.Client
}

var _ webhook.CustomDefaulter = &ServiceCustomDefaulter{}

// Default implements webhook.CustomDefaulter so a webhook will be registered for the type Service.
func (d *ServiceCustomDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	service, ok := obj.(*servingknativedevv1.Service)
	if !ok {
		return fmt.Errorf("expected a Service object but got %T", obj)
	}
	servicelog.Info("Mutating Service", "name", service.GetName())

	// Check for deco.sites/decofile-inject annotation
	if service.Annotations == nil {
		return nil
	}

	injectAnnotation, exists := service.Annotations["deco.sites/decofile-inject"]
	if !exists || injectAnnotation == "" {
		return nil
	}

	// Resolve Decofile name
	decofileName := injectAnnotation
	if injectAnnotation == "default" {
		// Get site name from namespace by stripping "sites-" prefix
		namespace := service.Namespace
		if len(namespace) == 0 {
			return fmt.Errorf("cannot resolve default Decofile: service has no namespace")
		}

		// Strip "sites-" prefix to get site name
		const sitesPrefix = "sites-"
		if len(namespace) > len(sitesPrefix) && namespace[:len(sitesPrefix)] == sitesPrefix {
			siteName := namespace[len(sitesPrefix):]
			decofileName = fmt.Sprintf("decofile-%s-main", siteName)
		} else {
			return fmt.Errorf("cannot resolve default Decofile: namespace %s does not start with 'sites-'", namespace)
		}
	}

	// Fetch the Decofile
	decofile := &decositesv1alpha1.Decofile{}
	err := d.Client.Get(ctx, types.NamespacedName{
		Name:      decofileName,
		Namespace: service.Namespace,
	}, decofile)
	if err != nil {
		return fmt.Errorf("failed to get Decofile %s: %w", decofileName, err)
	}

	// Check if ConfigMap is ready
	if decofile.Status.ConfigMapName == "" {
		return fmt.Errorf("decofile %s does not have a ConfigMap created yet", decofileName)
	}

	// Get mount path from annotation or use default
	mountPath := "/app/deco/.deco/blocks"
	if customPath, exists := service.Annotations["deco.sites/decofile-mount-path"]; exists {
		mountPath = customPath
	}

	// Ensure volumes array exists
	if service.Spec.Template.Spec.Volumes == nil {
		service.Spec.Template.Spec.Volumes = []corev1.Volume{}
	}

	// Check if volume already exists
	volumeName := "decofile-config"
	volumeExists := false
	for i, vol := range service.Spec.Template.Spec.Volumes {
		if vol.Name == volumeName {
			// Update existing volume
			service.Spec.Template.Spec.PodSpec.Volumes[i].VolumeSource = corev1.VolumeSource{
				Projected: &corev1.ProjectedVolumeSource{
					Sources: []corev1.VolumeProjection{
						{
							ConfigMap: &corev1.ConfigMapProjection{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: decofile.Status.ConfigMapName,
								},
							},
						},
					},
				},
			}
			volumeExists = true
			break
		}
	}

	if !volumeExists {
		// Add new volume
		service.Spec.Template.Spec.Volumes = append(service.Spec.Template.Spec.Volumes, corev1.Volume{
			Name: volumeName,
			VolumeSource: corev1.VolumeSource{
				Projected: &corev1.ProjectedVolumeSource{
					Sources: []corev1.VolumeProjection{
						{
							ConfigMap: &corev1.ConfigMapProjection{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: decofile.Status.ConfigMapName,
								},
							},
						},
					},
				},
			},
		})
	}

	// Find container and add volumeMount
	if len(service.Spec.Template.Spec.Containers) == 0 {
		return fmt.Errorf("no containers found in Service spec")
	}

	// Find the "app" container or use first container
	var targetContainerIdx int
	for i, container := range service.Spec.Template.Spec.Containers {
		if container.Name == "app" {
			targetContainerIdx = i
			break
		}
	}

	// Add volumeMount
	mountExists := false
	for i, mount := range service.Spec.Template.Spec.PodSpec.Containers[targetContainerIdx].VolumeMounts {
		if mount.Name == volumeName {
			// Update existing mount
			service.Spec.Template.Spec.PodSpec.Containers[targetContainerIdx].VolumeMounts[i].MountPath = mountPath
			mountExists = true
			break
		}
	}

	if !mountExists {
		service.Spec.Template.Spec.PodSpec.Containers[targetContainerIdx].VolumeMounts = append(
			service.Spec.Template.Spec.PodSpec.Containers[targetContainerIdx].VolumeMounts,
			corev1.VolumeMount{
				Name:      volumeName,
				MountPath: mountPath,
				ReadOnly:  true,
			},
		)
	}

	servicelog.Info("Successfully injected Decofile into Service", "service", service.Name, "decofile", decofileName, "configmap", decofile.Status.ConfigMapName)
	return nil
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
// NOTE: The 'path' attribute must follow a specific pattern and should not be modified directly here.
// Modifying the path for an invalid path can cause API server errors; failing to locate the webhook.
// +kubebuilder:webhook:path=/validate-serving-knative-dev-serving-knative-dev-v1-service,mutating=false,failurePolicy=fail,sideEffects=None,groups=serving.knative.dev.serving.knative.dev,resources=services,verbs=create;update,versions=v1,name=vservice-v1.kb.io,admissionReviewVersions=v1

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

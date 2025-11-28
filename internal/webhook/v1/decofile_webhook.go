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

	"k8s.io/apimachinery/pkg/runtime"
	servingknativedevv1 "knative.dev/serving/pkg/apis/serving/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
)

// nolint:unused
var decofilelog = logf.Log.WithName("decofile-resource")

// +kubebuilder:rbac:groups=serving.knative.dev,resources=services,verbs=get;list;watch

// SetupDecofileWebhookWithManager registers the webhook for Decofile in the manager.
func SetupDecofileWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&decositesv1alpha1.Decofile{}).
		WithValidator(&DecofileCustomValidator{Client: mgr.GetClient()}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-deco-sites-v1alpha1-decofile,mutating=false,failurePolicy=fail,sideEffects=None,groups=deco.sites,resources=decofiles,verbs=delete,versions=v1alpha1,name=vdecofile.kb.io,admissionReviewVersions=v1

// DecofileCustomValidator struct is responsible for validating the Decofile resource
// when it is deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type DecofileCustomValidator struct {
	Client client.Client
}

var _ webhook.CustomValidator = &DecofileCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type Decofile.
func (v *DecofileCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	// No validation on create
	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type Decofile.
func (v *DecofileCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	// No validation on update
	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type Decofile.
func (v *DecofileCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	decofile, ok := obj.(*decositesv1alpha1.Decofile)
	if !ok {
		return nil, fmt.Errorf("expected a Decofile object but got %T", obj)
	}

	decofilelog.Info("Validating Decofile deletion", "name", decofile.Name, "namespace", decofile.Namespace)

	// Determine deploymentId for this Decofile
	deploymentId := decofile.Spec.DeploymentId
	if deploymentId == "" {
		deploymentId = decofile.Name
	}

	// Check if any Knative Services are using this Decofile
	serviceList := &servingknativedevv1.ServiceList{}
	err := v.Client.List(ctx, serviceList, client.InNamespace(decofile.Namespace))
	if err != nil {
		// If we can't list services, allow deletion (fail-open to avoid blocking operations)
		decofilelog.Error(err, "Failed to list Services during Decofile validation, allowing deletion")
		return nil, nil
	}

	// Check each Service for matching deploymentId and injection annotation
	var usingServices []string
	for i := range serviceList.Items {
		svc := &serviceList.Items[i]

		// Check if Service has injection enabled
		if svc.Annotations != nil && svc.Annotations[decofileInjectAnnot] == "true" {
			// Check if Service's deploymentId matches this Decofile
			if svc.Labels != nil {
				svcDeploymentId := svc.Labels[deploymentIdLabel]
				if svcDeploymentId == deploymentId {
					usingServices = append(usingServices, svc.Name)
				}
			}
		}
	}

	if len(usingServices) > 0 {
		return admission.Warnings{
				fmt.Sprintf("Decofile %s is currently in use by %d Service(s)", decofile.Name, len(usingServices)),
			},
			fmt.Errorf("cannot delete Decofile %s: still in use by Service(s): %v. Remove deco.sites/decofile-inject annotation or delete the Service(s) first",
				decofile.Name, usingServices)
	}

	decofilelog.Info("Decofile deletion allowed - not in use", "name", decofile.Name)
	return nil, nil
}

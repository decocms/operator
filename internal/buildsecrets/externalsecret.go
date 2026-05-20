/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package buildsecrets owns the operator's interaction with the tenant
// build-time secrets sync. Today it materialises an `ExternalSecret`
// (external-secrets.io/v1) per opted-in site namespace; swapping the
// sync mechanism away from ESO touches only this file.
package buildsecrets

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// SecretName is the K8s Secret materialised by ESO from AWS Secrets
	// Manager. Builders in each site namespace consume it via envFrom with
	// optional:true (admin PR #3201) so its absence is a no-op.
	SecretName = "build-secrets"

	// ManagedByLabel + FeatureLabel let operators bulk-filter the EE for
	// force-sync via `kubectl annotate es -l ... force-sync=$(date +%s)`.
	ManagedByLabel = "deco.sites/managed-by"
	FeatureLabel   = "deco.sites/feature"

	refreshInterval         = "1h"
	clusterSecretStoreName  = "aws-secrets-manager"
	awsSecretsManagerKeyFmt = "sites/%s/build"
)

// GVK is exported so the reconciler can register a Watches() on the same
// type without re-declaring the kind tuple.
var GVK = schema.GroupVersionKind{
	Group:   "external-secrets.io",
	Version: "v1",
	Kind:    "ExternalSecret",
}

// Ensure creates or updates the ExternalSecret pulling `sites/<site>/build`
// from the cluster-local AWS Secrets Manager into the K8s Secret named
// `build-secrets`. Spec drift is corrected on every reconcile.
func Ensure(ctx context.Context, c client.Client, namespace, site string) error {
	desired := newExternalSecret(namespace, site)

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(GVK)
	key := types.NamespacedName{Name: SecretName, Namespace: namespace}
	err := c.Get(ctx, key, existing)
	switch {
	case errors.IsNotFound(err):
		return c.Create(ctx, desired)
	case err != nil:
		return fmt.Errorf("get externalsecret: %w", err)
	}

	existing.Object["spec"] = desired.Object["spec"]
	labels := existing.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	for k, v := range desired.GetLabels() {
		labels[k] = v
	}
	existing.SetLabels(labels)
	if err := c.Update(ctx, existing); err != nil {
		return fmt.Errorf("update externalsecret: %w", err)
	}
	return nil
}

// Remove deletes the ExternalSecret. ESO's `creationPolicy: Owner` makes
// the materialised K8s Secret cascade with it. Idempotent on NotFound.
func Remove(ctx context.Context, c client.Client, namespace string) error {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(GVK)
	obj.SetName(SecretName)
	obj.SetNamespace(namespace)
	if err := c.Delete(ctx, obj); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete externalsecret: %w", err)
	}
	return nil
}

func newExternalSecret(namespace, site string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": GVK.GroupVersion().String(),
			"kind":       GVK.Kind,
			"metadata": map[string]interface{}{
				"name":      SecretName,
				"namespace": namespace,
				"labels": map[string]interface{}{
					ManagedByLabel: "operator",
					FeatureLabel:   "build-secrets",
				},
			},
			"spec": map[string]interface{}{
				"refreshInterval": refreshInterval,
				"secretStoreRef": map[string]interface{}{
					"name": clusterSecretStoreName,
					"kind": "ClusterSecretStore",
				},
				"target": map[string]interface{}{
					"name":           SecretName,
					"creationPolicy": "Owner",
				},
				"dataFrom": []interface{}{
					map[string]interface{}{
						"extract": map[string]interface{}{
							"key": fmt.Sprintf(awsSecretsManagerKeyFmt, site),
						},
					},
				},
			},
		},
	}
}

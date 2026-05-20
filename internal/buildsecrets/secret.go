/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package buildsecrets

import (
	"context"
	"fmt"
	"maps"
	"reflect"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Sync reconciles the K8s Secret `build-secrets` in `namespace` against
// the upstream Source for `site`. Four cases:
//
//  1. Upstream missing + no local Secret: no-op.
//  2. Upstream missing + local Secret owned by operator: delete locally.
//  3. Upstream present + no local Secret: create with operator labels.
//  4. Upstream present + local Secret owned by operator: update if data
//     drifted.
//
// In all cases where a local Secret exists *without* operator labels,
// returns ErrNotOwned and makes no changes.
func Sync(ctx context.Context, c client.Client, namespace, site string, src Source) error {
	data, exists, err := src.Get(ctx, fmt.Sprintf(KeyTemplate, site))
	if err != nil {
		return fmt.Errorf("source.Get: %w", err)
	}

	existing := &corev1.Secret{}
	getErr := c.Get(ctx, types.NamespacedName{Name: SecretName, Namespace: namespace}, existing)
	switch {
	case errors.IsNotFound(getErr):
		existing = nil
	case getErr != nil:
		return fmt.Errorf("get secret: %w", getErr)
	}

	if !exists {
		if existing == nil {
			return nil
		}
		if !isManagedByUs(existing) {
			return ErrNotOwned
		}
		if err := c.Delete(ctx, existing); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("delete secret: %w", err)
		}
		return nil
	}

	desired := newSecret(namespace, data)

	if existing == nil {
		if err := c.Create(ctx, desired); err != nil {
			return fmt.Errorf("create secret: %w", err)
		}
		return nil
	}

	if !isManagedByUs(existing) {
		return ErrNotOwned
	}

	if !dataChanged(existing.Data, desired.Data) {
		return nil
	}

	existing.Data = desired.Data
	existing.StringData = nil
	existing.Labels = maps.Clone(desired.Labels)
	if err := c.Update(ctx, existing); err != nil {
		return fmt.Errorf("update secret: %w", err)
	}
	return nil
}

// Remove deletes the operator-managed K8s Secret in `namespace`.
// Refuses (ErrNotOwned) if the Secret lacks operator labels.
// Idempotent on NotFound.
func Remove(ctx context.Context, c client.Client, namespace string) error {
	existing := &corev1.Secret{}
	err := c.Get(ctx, types.NamespacedName{Name: SecretName, Namespace: namespace}, existing)
	if errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get secret: %w", err)
	}
	if !isManagedByUs(existing) {
		return ErrNotOwned
	}
	if err := c.Delete(ctx, existing); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete secret: %w", err)
	}
	return nil
}

func newSecret(namespace string, data map[string]string) *corev1.Secret {
	out := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SecretName,
			Namespace: namespace,
			Labels: map[string]string{
				ManagedByLabel: "operator",
				FeatureLabel:   "build-secrets",
			},
		},
		Data: make(map[string][]byte, len(data)),
		Type: corev1.SecretTypeOpaque,
	}
	for k, v := range data {
		out.Data[k] = []byte(v)
	}
	return out
}

func isManagedByUs(s *corev1.Secret) bool {
	return s.Labels[ManagedByLabel] == "operator" && s.Labels[FeatureLabel] == "build-secrets"
}

func dataChanged(current, desired map[string][]byte) bool {
	return !reflect.DeepEqual(current, desired)
}

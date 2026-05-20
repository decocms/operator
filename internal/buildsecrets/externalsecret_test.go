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
	"testing"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newClient(t *testing.T) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	return fake.NewClientBuilder().WithScheme(scheme).Build()
}

func getExternalSecret(t *testing.T, c client.Client, namespace string) *unstructured.Unstructured {
	t.Helper()
	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(GVK)
	if err := c.Get(context.Background(), types.NamespacedName{Name: SecretName, Namespace: namespace}, got); err != nil {
		t.Fatalf("Get externalsecret: %v", err)
	}
	return got
}

func TestEnsureCreates(t *testing.T) {
	c := newClient(t)
	if err := Ensure(context.Background(), c, "sites-acme", "acme"); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	got := getExternalSecret(t, c, "sites-acme")
	if v := got.GetLabels()[ManagedByLabel]; v != "operator" {
		t.Fatalf("label %s = %q, want operator", ManagedByLabel, v)
	}
	if v := got.GetLabels()[FeatureLabel]; v != "build-secrets" {
		t.Fatalf("label %s = %q, want build-secrets", FeatureLabel, v)
	}

	dataFrom, _, _ := unstructured.NestedSlice(got.Object, "spec", "dataFrom")
	if len(dataFrom) != 1 {
		t.Fatalf("dataFrom length = %d, want 1", len(dataFrom))
	}
	extract := dataFrom[0].(map[string]interface{})["extract"].(map[string]interface{})
	if got := extract["key"]; got != "sites/acme/build" {
		t.Fatalf("extract.key = %q, want sites/acme/build", got)
	}
	target, _, _ := unstructured.NestedMap(got.Object, "spec", "target")
	if got := target["creationPolicy"]; got != "Owner" {
		t.Fatalf("creationPolicy = %q, want Owner", got)
	}
}

func TestEnsureIsIdempotent(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()
	if err := Ensure(ctx, c, "sites-acme", "acme"); err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	if err := Ensure(ctx, c, "sites-acme", "acme"); err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
}

func TestEnsureCorrectsDrift(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()
	if err := Ensure(ctx, c, "sites-acme", "acme"); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	drifted := getExternalSecret(t, c, "sites-acme")
	if err := unstructured.SetNestedField(drifted.Object, "999h", "spec", "refreshInterval"); err != nil {
		t.Fatalf("SetNestedField: %v", err)
	}
	if err := c.Update(ctx, drifted); err != nil {
		t.Fatalf("Update drift: %v", err)
	}

	if err := Ensure(ctx, c, "sites-acme", "acme"); err != nil {
		t.Fatalf("Ensure after drift: %v", err)
	}
	healed := getExternalSecret(t, c, "sites-acme")
	got, _, _ := unstructured.NestedString(healed.Object, "spec", "refreshInterval")
	if got != refreshInterval {
		t.Fatalf("refreshInterval = %q, want %q (drift not corrected)", got, refreshInterval)
	}
}

func TestRemove(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()
	if err := Remove(ctx, c, "sites-acme"); err != nil {
		t.Fatalf("Remove on empty: %v", err)
	}
	if err := Ensure(ctx, c, "sites-acme", "acme"); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if err := Remove(ctx, c, "sites-acme"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(GVK)
	err := c.Get(ctx, types.NamespacedName{Name: SecretName, Namespace: "sites-acme"}, got)
	if !errors.IsNotFound(err) {
		t.Fatalf("Get after Remove: err = %v, want NotFound", err)
	}
}

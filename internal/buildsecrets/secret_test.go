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
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// mockSource implements Source for tests without touching AWS.
type mockSource struct {
	data   map[string]string
	exists bool
	err    error
}

func (m *mockSource) Get(ctx context.Context, key string) (map[string]string, bool, error) {
	if m.err != nil {
		return nil, false, m.err
	}
	return m.data, m.exists, nil
}

func newClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

const testNamespace = "sites-acme"

func getSecret(t *testing.T, c client.Client) *corev1.Secret {
	t.Helper()
	got := &corev1.Secret{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: SecretName, Namespace: testNamespace}, got); err != nil {
		t.Fatalf("get secret: %v", err)
	}
	return got
}

func TestSyncCreatesWhenUpstreamExists(t *testing.T) {
	c := newClient(t)
	src := &mockSource{exists: true, data: map[string]string{"DENO_AUTH_TOKENS": "github_pat_xxx@raw.githubusercontent.com"}}

	if err := Sync(context.Background(), c, "sites-acme", "acme", src); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	got := getSecret(t, c)
	if string(got.Data["DENO_AUTH_TOKENS"]) != "github_pat_xxx@raw.githubusercontent.com" {
		t.Fatalf("data not written: %v", got.Data)
	}
	if got.Labels[ManagedByLabel] != "operator" || got.Labels[FeatureLabel] != "build-secrets" {
		t.Fatalf("labels missing: %v", got.Labels)
	}
}

func TestSyncNoopWhenUpstreamMissing(t *testing.T) {
	c := newClient(t)
	src := &mockSource{exists: false}

	if err := Sync(context.Background(), c, "sites-acme", "acme", src); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	got := &corev1.Secret{}
	err := c.Get(context.Background(), types.NamespacedName{Name: SecretName, Namespace: "sites-acme"}, got)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestSyncUpdatesOnDrift(t *testing.T) {
	managed := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SecretName,
			Namespace: "sites-acme",
			Labels: map[string]string{
				ManagedByLabel: "operator",
				FeatureLabel:   "build-secrets",
			},
		},
		Data: map[string][]byte{"OLD": []byte("value")},
	}
	c := newClient(t, managed)
	src := &mockSource{exists: true, data: map[string]string{"NEW": "value"}}

	if err := Sync(context.Background(), c, "sites-acme", "acme", src); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	got := getSecret(t, c)
	if _, ok := got.Data["OLD"]; ok {
		t.Fatal("old key not removed")
	}
	if string(got.Data["NEW"]) != "value" {
		t.Fatalf("new key not written: %v", got.Data)
	}
}

func TestSyncIdempotentWhenDataMatches(t *testing.T) {
	managed := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SecretName,
			Namespace: "sites-acme",
			Labels: map[string]string{
				ManagedByLabel: "operator",
				FeatureLabel:   "build-secrets",
			},
			ResourceVersion: "999",
		},
		Data: map[string][]byte{"FOO": []byte("bar")},
	}
	c := newClient(t, managed)
	src := &mockSource{exists: true, data: map[string]string{"FOO": "bar"}}

	if err := Sync(context.Background(), c, "sites-acme", "acme", src); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	got := getSecret(t, c)
	if got.ResourceVersion != "999" {
		t.Fatalf("ResourceVersion changed (write should have been skipped): %s", got.ResourceVersion)
	}
}

func TestSyncDeletesWhenUpstreamRemoved(t *testing.T) {
	managed := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SecretName,
			Namespace: "sites-acme",
			Labels: map[string]string{
				ManagedByLabel: "operator",
				FeatureLabel:   "build-secrets",
			},
		},
		Data: map[string][]byte{"FOO": []byte("bar")},
	}
	c := newClient(t, managed)
	src := &mockSource{exists: false}

	if err := Sync(context.Background(), c, "sites-acme", "acme", src); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	got := &corev1.Secret{}
	err := c.Get(context.Background(), types.NamespacedName{Name: SecretName, Namespace: "sites-acme"}, got)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected Secret deleted, got err = %v", err)
	}
}

func TestSyncRefusesUnownedSecret(t *testing.T) {
	unowned := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SecretName,
			Namespace: "sites-acme",
		},
		Data: map[string][]byte{"USER_TOKEN": []byte("hand-crafted")},
	}
	c := newClient(t, unowned)
	src := &mockSource{exists: true, data: map[string]string{"FROM_SM": "value"}}

	err := Sync(context.Background(), c, "sites-acme", "acme", src)
	if !errors.Is(err, ErrNotOwned) {
		t.Fatalf("expected ErrNotOwned, got %v", err)
	}

	got := getSecret(t, c)
	if string(got.Data["USER_TOKEN"]) != "hand-crafted" {
		t.Fatalf("unowned secret data was mutated: %v", got.Data)
	}
	if _, ok := got.Data["FROM_SM"]; ok {
		t.Fatal("operator wrote into unowned secret")
	}
}

func TestRemoveDeletesManagedSecret(t *testing.T) {
	managed := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SecretName,
			Namespace: "sites-acme",
			Labels: map[string]string{
				ManagedByLabel: "operator",
				FeatureLabel:   "build-secrets",
			},
		},
	}
	c := newClient(t, managed)

	if err := Remove(context.Background(), c, "sites-acme"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	got := &corev1.Secret{}
	err := c.Get(context.Background(), types.NamespacedName{Name: SecretName, Namespace: "sites-acme"}, got)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestRemoveRefusesUnowned(t *testing.T) {
	unowned := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SecretName,
			Namespace: "sites-acme",
		},
	}
	c := newClient(t, unowned)

	err := Remove(context.Background(), c, "sites-acme")
	if !errors.Is(err, ErrNotOwned) {
		t.Fatalf("expected ErrNotOwned, got %v", err)
	}

	// Secret still there
	got := getSecret(t, c)
	if got.Name != SecretName {
		t.Fatal("unowned secret was deleted")
	}
}

func TestRemoveIdempotentOnNotFound(t *testing.T) {
	c := newClient(t)
	if err := Remove(context.Background(), c, "sites-acme"); err != nil {
		t.Fatalf("Remove on empty: %v", err)
	}
}

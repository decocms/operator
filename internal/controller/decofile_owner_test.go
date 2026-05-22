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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	servingv1 "knative.dev/serving/pkg/apis/serving/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
)

func newOwnerTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := decositesv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add decosites scheme: %v", err)
	}
	if err := servingv1.AddToScheme(s); err != nil {
		t.Fatalf("add servingv1 scheme: %v", err)
	}
	return s
}

func makeRevision(name, namespace, deploymentId string, uid types.UID) *servingv1.Revision {
	return &servingv1.Revision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       uid,
			Labels:    map[string]string{deploymentIdLabel: deploymentId},
		},
	}
}

func makeDecofile(name, namespace, deploymentId string) *decositesv1alpha1.Decofile {
	df := &decositesv1alpha1.Decofile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	if deploymentId != "" {
		df.Spec.DeploymentId = deploymentId
	}
	return df
}

func TestSyncRevisionOwnerRefs_AddsOwnerWhenRevisionExists(t *testing.T) {
	ctx := context.Background()
	scheme := newOwnerTestScheme(t)

	df := makeDecofile("mhsygflbgo", "sites-foo", "")
	rev := makeRevision("foo-site-mhsygflbgo", "sites-foo", "mhsygflbgo", "rev-uid-1")

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(df, rev).Build()
	r := &DecofileReconciler{Client: c, Scheme: scheme}

	if err := r.syncRevisionOwnerRefs(ctx, df); err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	got := &decositesv1alpha1.Decofile{}
	if err := c.Get(ctx, client.ObjectKey{Name: "mhsygflbgo", Namespace: "sites-foo"}, got); err != nil {
		t.Fatalf("get decofile: %v", err)
	}
	if len(got.OwnerReferences) != 1 {
		t.Fatalf("want 1 owner, got %d", len(got.OwnerReferences))
	}
	or := got.OwnerReferences[0]
	if or.Kind != "Revision" || or.Name != "foo-site-mhsygflbgo" || or.UID != "rev-uid-1" {
		t.Errorf("unexpected ownerRef: %+v", or)
	}
	if or.Controller == nil || *or.Controller {
		t.Errorf("controller should be false, got %v", or.Controller)
	}
	if or.BlockOwnerDeletion == nil || *or.BlockOwnerDeletion {
		t.Errorf("blockOwnerDeletion should be false, got %v", or.BlockOwnerDeletion)
	}
}

func TestSyncRevisionOwnerRefs_NoMatchingRevisionDoesNothing(t *testing.T) {
	ctx := context.Background()
	scheme := newOwnerTestScheme(t)

	df := makeDecofile("orphan", "sites-foo", "")
	// Revision with DIFFERENT deploymentId — must not be picked up.
	rev := makeRevision("foo-site-other", "sites-foo", "other", "rev-uid-2")

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(df, rev).Build()
	r := &DecofileReconciler{Client: c, Scheme: scheme}

	if err := r.syncRevisionOwnerRefs(ctx, df); err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	got := &decositesv1alpha1.Decofile{}
	if err := c.Get(ctx, client.ObjectKey{Name: "orphan", Namespace: "sites-foo"}, got); err != nil {
		t.Fatalf("get decofile: %v", err)
	}
	if len(got.OwnerReferences) != 0 {
		t.Fatalf("want 0 owners for orphan, got %d", len(got.OwnerReferences))
	}
}

func TestSyncRevisionOwnerRefs_IsIdempotent(t *testing.T) {
	ctx := context.Background()
	scheme := newOwnerTestScheme(t)

	df := makeDecofile("mhsygflbgo", "sites-foo", "")
	rev := makeRevision("foo-site-mhsygflbgo", "sites-foo", "mhsygflbgo", "rev-uid-3")
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(df, rev).Build()
	r := &DecofileReconciler{Client: c, Scheme: scheme}

	for i := 0; i < 3; i++ {
		if err := r.syncRevisionOwnerRefs(ctx, df); err != nil {
			t.Fatalf("sync %d failed: %v", i, err)
		}
		// Refresh local df to mirror what a real reconcile loop would see.
		if err := c.Get(ctx, client.ObjectKey{Name: df.Name, Namespace: df.Namespace}, df); err != nil {
			t.Fatalf("refresh: %v", err)
		}
	}

	if len(df.OwnerReferences) != 1 {
		t.Fatalf("want exactly 1 ownerRef after 3 syncs, got %d", len(df.OwnerReferences))
	}
}

func TestSyncRevisionOwnerRefs_SkipsRevisionBeingDeleted(t *testing.T) {
	ctx := context.Background()
	scheme := newOwnerTestScheme(t)

	df := makeDecofile("mhsygflbgo", "sites-foo", "")
	rev := makeRevision("foo-site-mhsygflbgo", "sites-foo", "mhsygflbgo", "rev-uid-4")
	now := metav1.Now()
	rev.DeletionTimestamp = &now
	rev.Finalizers = []string{"keep-alive-for-test"}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(df, rev).Build()
	r := &DecofileReconciler{Client: c, Scheme: scheme}

	if err := r.syncRevisionOwnerRefs(ctx, df); err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	got := &decositesv1alpha1.Decofile{}
	if err := c.Get(ctx, client.ObjectKey{Name: df.Name, Namespace: df.Namespace}, got); err != nil {
		t.Fatalf("get decofile: %v", err)
	}
	if len(got.OwnerReferences) != 0 {
		t.Fatalf("want 0 owners (terminating Revision skipped), got %d", len(got.OwnerReferences))
	}
}

func TestSyncRevisionOwnerRefs_MultipleRevisionsBecomeMultipleOwners(t *testing.T) {
	ctx := context.Background()
	scheme := newOwnerTestScheme(t)

	df := makeDecofile("mhsygflbgo", "sites-foo", "")
	// Two Revisions with the same deploymentId (rollback scenario).
	rev1 := makeRevision("foo-site-mhsygflbgo-old", "sites-foo", "mhsygflbgo", "uid-old")
	rev2 := makeRevision("foo-site-mhsygflbgo-new", "sites-foo", "mhsygflbgo", "uid-new")

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(df, rev1, rev2).Build()
	r := &DecofileReconciler{Client: c, Scheme: scheme}

	if err := r.syncRevisionOwnerRefs(ctx, df); err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	got := &decositesv1alpha1.Decofile{}
	if err := c.Get(ctx, client.ObjectKey{Name: df.Name, Namespace: df.Namespace}, got); err != nil {
		t.Fatalf("get decofile: %v", err)
	}
	if len(got.OwnerReferences) != 2 {
		t.Fatalf("want 2 ownerRefs (both Revisions keep the Decofile alive), got %d", len(got.OwnerReferences))
	}
	uids := map[types.UID]bool{}
	for _, or := range got.OwnerReferences {
		uids[or.UID] = true
	}
	if !uids["uid-old"] || !uids["uid-new"] {
		t.Errorf("missing one of the expected UIDs: %v", uids)
	}
}

func TestSyncRevisionOwnerRefs_RespectsExplicitDeploymentId(t *testing.T) {
	ctx := context.Background()
	scheme := newOwnerTestScheme(t)

	// Decofile name and explicit deploymentId differ — the explicit one wins.
	df := makeDecofile("any-name", "sites-foo", "explicit-dep")
	rev := makeRevision("rev-by-explicit", "sites-foo", "explicit-dep", "uid-explicit")
	// Decoy Revision with deploymentId matching the Decofile name — must NOT
	// be picked up because spec.deploymentId is set.
	decoy := makeRevision("rev-decoy", "sites-foo", "any-name", "uid-decoy")

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(df, rev, decoy).Build()
	r := &DecofileReconciler{Client: c, Scheme: scheme}

	if err := r.syncRevisionOwnerRefs(ctx, df); err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	got := &decositesv1alpha1.Decofile{}
	if err := c.Get(ctx, client.ObjectKey{Name: df.Name, Namespace: df.Namespace}, got); err != nil {
		t.Fatalf("get decofile: %v", err)
	}
	if len(got.OwnerReferences) != 1 {
		t.Fatalf("want 1 ownerRef, got %d", len(got.OwnerReferences))
	}
	if got.OwnerReferences[0].UID != "uid-explicit" {
		t.Errorf("want uid-explicit, got %s", got.OwnerReferences[0].UID)
	}
}

func TestMapRevisionToDecofile_FindsByDefaultedDeploymentId(t *testing.T) {
	ctx := context.Background()
	scheme := newOwnerTestScheme(t)

	// Decofile uses metadata.name as effective deploymentId (spec.deploymentId empty).
	df := makeDecofile("mhsygflbgo", "sites-foo", "")
	rev := makeRevision("foo-site-mhsygflbgo", "sites-foo", "mhsygflbgo", "uid")

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(df, rev).Build()
	r := &DecofileReconciler{Client: c, Scheme: scheme}

	reqs := r.mapRevisionToDecofile(ctx, rev)
	if len(reqs) != 1 {
		t.Fatalf("want 1 request, got %d", len(reqs))
	}
	want := reconcile.Request{NamespacedName: client.ObjectKey{Namespace: "sites-foo", Name: "mhsygflbgo"}}
	if reqs[0] != want {
		t.Errorf("want %v, got %v", want, reqs[0])
	}
}

func TestMapRevisionToDecofile_IgnoresRevisionWithoutLabel(t *testing.T) {
	ctx := context.Background()
	scheme := newOwnerTestScheme(t)

	df := makeDecofile("mhsygflbgo", "sites-foo", "")
	rev := &servingv1.Revision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "no-label",
			Namespace: "sites-foo",
			UID:       "uid",
			// Note: no deploymentIdLabel.
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(df, rev).Build()
	r := &DecofileReconciler{Client: c, Scheme: scheme}

	reqs := r.mapRevisionToDecofile(ctx, rev)
	if len(reqs) != 0 {
		t.Fatalf("want 0 requests, got %d", len(reqs))
	}
}

func TestMapRevisionToDecofile_FindsByExplicitDeploymentId(t *testing.T) {
	ctx := context.Background()
	scheme := newOwnerTestScheme(t)

	df := makeDecofile("any-name", "sites-foo", "explicit-dep")
	rev := makeRevision("rev", "sites-foo", "explicit-dep", "uid")

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(df, rev).Build()
	r := &DecofileReconciler{Client: c, Scheme: scheme}

	reqs := r.mapRevisionToDecofile(ctx, rev)
	if len(reqs) != 1 {
		t.Fatalf("want 1 request, got %d", len(reqs))
	}
	if reqs[0].Name != "any-name" {
		t.Errorf("want any-name, got %s", reqs[0].Name)
	}
}

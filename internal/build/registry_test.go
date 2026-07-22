package build

import (
	"context"
	"errors"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
)

// Compile-time: BuilderRegistry must satisfy Builder.
var _ Builder = (*BuilderRegistry)(nil)

type stubBuilder struct{ job *batchv1.Job }

func (s *stubBuilder) NewJob(_ context.Context, _ *decositesv1alpha1.Deco, _ string, _ decositesv1alpha1.DecoSpecBuildSource) (*batchv1.Job, error) {
	return s.job, nil
}

func testDeco(servingType, framework string) *decositesv1alpha1.Deco {
	return &decositesv1alpha1.Deco{
		ObjectMeta: metav1.ObjectMeta{Name: "site", Namespace: "default"},
		Spec: decositesv1alpha1.DecoSpec{
			Site:      "site",
			Org:       "org",
			Framework: framework,
			Serving:   &decositesv1alpha1.DecoSpecServing{Type: servingType},
		},
	}
}

func TestRegistry_DispatchesToAgnosticBuilder(t *testing.T) {
	want := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "build-abc"}}
	r := NewBuilderRegistry()
	r.Register("cloudflare-worker", "", &stubBuilder{job: want})

	// framework-agnostic: matches regardless of spec.framework
	got, err := r.NewJob(context.Background(), testDeco("cloudflare-worker", "tanstack"), "build-abc", decositesv1alpha1.DecoSpecBuildSource{CommitSha: "abc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("expected job %p, got %p", want, got)
	}
}

func TestRegistry_DispatchesToStackSpecificBuilder(t *testing.T) {
	tanstack := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "knative-tanstack"}}
	r := NewBuilderRegistry()
	r.Register("knative", "tanstack", &stubBuilder{job: tanstack})

	// knative + tanstack → the stack-specific builder
	got, err := r.NewJob(context.Background(), testDeco("knative", "tanstack"), "job", decositesv1alpha1.DecoSpecBuildSource{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != tanstack {
		t.Errorf("expected knative/tanstack builder, got %p", got)
	}

	// knative + deno → no builder registered for that stack → error
	if _, err := r.NewJob(context.Background(), testDeco("knative", "deno"), "job", decositesv1alpha1.DecoSpecBuildSource{}); err == nil {
		t.Fatal("expected error for knative+deno (no builder registered)")
	}
}

func TestRegistry_ErrorsOnUnknownServingType(t *testing.T) {
	r := NewBuilderRegistry()
	_, err := r.NewJob(context.Background(), testDeco("unknown", ""), "job", decositesv1alpha1.DecoSpecBuildSource{})
	if err == nil {
		t.Fatal("expected error for unregistered serving type")
	}
	if !errors.Is(err, errNoFactory) {
		t.Errorf("expected errNoFactory, got %v", err)
	}
}

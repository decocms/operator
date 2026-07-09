package deploy

import (
	"context"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("add client-go scheme: %v", err)
	}
	if err := decositesv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add decosites scheme: %v", err)
	}
	return s
}

func newDecofile() *decositesv1alpha1.Decofile {
	return &decositesv1alpha1.Decofile{
		ObjectMeta: metav1.ObjectMeta{Name: "fastdeploy-mystore", Namespace: "mystore"},
		Spec: decositesv1alpha1.DecofileSpec{
			Source: decositesv1alpha1.SourceGitHub,
			Target: decositesv1alpha1.TargetTanstackKV,
			GitHub: &decositesv1alpha1.GitHubSource{
				Org:    "deco-sites",
				Repo:   "mystore",
				Commit: "content-commit-sha",
			},
			TanstackKV: &decositesv1alpha1.TanstackKVTarget{KVNamespaceID: "ns-123"},
		},
	}
}

func TestBuildJobCarriesDeploymentID(t *testing.T) {
	d := &tanstackKV{cfg: TanstackKVConfig{SyncerImage: "img:tag", CfAccountId: "acc", CfApiToken: "tok"}}
	job := d.buildJob(newDecofile(), "job-1", "mystore", "gh-token", "live-sha-999")

	envs := job.Spec.Template.Spec.Containers[0].Env
	got := ""
	for _, e := range envs {
		if e.Name == "DEPLOYMENT_ID" {
			got = e.Value
		}
	}
	if got != "live-sha-999" {
		t.Fatalf("DEPLOYMENT_ID env = %q, want %q", got, "live-sha-999")
	}
}

func TestReconcileWaitsWhenNoLivePointer(t *testing.T) {
	scheme := testScheme(t)
	df := newDecofile()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(df).
		WithStatusSubresource(df).
		Build()

	d := &tanstackKV{
		cfg: TanstackKVConfig{SyncerImage: "img:tag"},
		resolveLiveID: func(_ context.Context, _ string) (string, error) {
			return "", nil // index:live absent
		},
	}

	res, err := d.Reconcile(context.Background(), c, scheme, df)
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if res.RequeueAfter == 0 {
		t.Fatalf("expected a requeue while waiting for index:live")
	}

	// No Job should have been created.
	jobs := &batchv1.JobList{}
	if err := c.List(context.Background(), jobs, client.InNamespace("mystore")); err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs.Items) != 0 {
		t.Fatalf("expected no sync Job, got %d", len(jobs.Items))
	}

	// Status should reflect a Waiting (Unknown) Synced condition.
	got := &decositesv1alpha1.Decofile{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(df), got); err != nil {
		t.Fatalf("get decofile: %v", err)
	}
	cond := apimeta.FindStatusCondition(got.Status.Conditions, condSynced)
	if cond == nil || cond.Status != metav1.ConditionUnknown || cond.Reason != "Waiting" {
		t.Fatalf("expected Waiting/Unknown Synced condition, got %+v", cond)
	}
}

func TestReconcileCreatesJobTargetingLiveID(t *testing.T) {
	scheme := testScheme(t)
	df := newDecofile()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(df).
		WithStatusSubresource(df).
		Build()

	d := &tanstackKV{
		cfg: TanstackKVConfig{SyncerImage: "img:tag"},
		resolveLiveID: func(_ context.Context, ns string) (string, error) {
			if ns != "ns-123" {
				t.Fatalf("resolveLiveID got namespace %q", ns)
			}
			return "live-sha-abc", nil
		},
	}

	if _, err := d.Reconcile(context.Background(), c, scheme, df); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	jobs := &batchv1.JobList{}
	if err := c.List(context.Background(), jobs, client.InNamespace("mystore")); err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs.Items) != 1 {
		t.Fatalf("expected exactly one sync Job, got %d", len(jobs.Items))
	}

	got := ""
	for _, e := range jobs.Items[0].Spec.Template.Spec.Containers[0].Env {
		if e.Name == "DEPLOYMENT_ID" {
			got = e.Value
		}
	}
	if got != "live-sha-abc" {
		t.Fatalf("Job DEPLOYMENT_ID = %q, want live-sha-abc", got)
	}
}

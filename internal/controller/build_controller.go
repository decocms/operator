package controller

import (
	"context"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
	"github.com/deco-sites/decofile-operator/internal/build"
)

// BuildReconciler reconciles DecoBuild objects.
// It creates a K8s Job for each build and tracks the Job's outcome back to the DecoBuild status.
type BuildReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	CfApiToken  string
	CfAccountId string
	S3Config    build.S3Config
}

// +kubebuilder:rbac:groups=deco.sites,resources=decobuilds,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=deco.sites,resources=decobuilds/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=deco.sites,resources=decobuilds/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;delete

func (r *BuildReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	decoBuild := &decositesv1alpha1.DecoBuild{}
	if err := r.Get(ctx, req.NamespacedName, decoBuild); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Nothing to do for terminal phases.
	if decoBuild.Status.Phase == decositesv1alpha1.DecoBuildPhaseSucceeded ||
		decoBuild.Status.Phase == decositesv1alpha1.DecoBuildPhaseFailed {
		return ctrl.Result{}, nil
	}

	jobName := build.JobName(decoBuild.Spec.CommitSha)

	existingJob := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: decoBuild.Namespace}, existingJob)
	if errors.IsNotFound(err) {
		log.Info("Creating build job", "job", jobName, "site", decoBuild.Spec.Site)
		return r.createJob(ctx, decoBuild, jobName)
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	return r.syncStatus(ctx, decoBuild, existingJob)
}

func (r *BuildReconciler) createJob(ctx context.Context, decoBuild *decositesv1alpha1.DecoBuild, jobName string) (ctrl.Result, error) {
	githubToken, err := r.resolveGithubToken(ctx, decoBuild)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("resolving github token: %w", err)
	}

	presignedURLs, err := build.GeneratePresignedURLs(ctx, r.S3Config, decoBuild.Spec.Site, jobName)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("generating presigned URLs: %w", err)
	}

	job := build.NewJob(build.JobOpts{
		Build:         decoBuild,
		JobName:       jobName,
		GithubToken:   githubToken,
		CfApiToken:    r.CfApiToken,
		CfAccountId:   r.CfAccountId,
		PresignedURLs: presignedURLs,
	})

	if err := controllerutil.SetControllerReference(decoBuild, job, r.Scheme); err != nil {
		return ctrl.Result{}, fmt.Errorf("setting owner reference: %w", err)
	}

	if err := r.Create(ctx, job); err != nil && !errors.IsAlreadyExists(err) {
		return ctrl.Result{}, fmt.Errorf("creating build job: %w", err)
	}

	now := metav1.Now()
	decoBuild.Status.Phase = decositesv1alpha1.DecoBuildPhaseRunning
	decoBuild.Status.JobName = jobName
	decoBuild.Status.StartTime = &now
	return ctrl.Result{}, r.Status().Update(ctx, decoBuild)
}

// syncStatus maps the K8s Job conditions to the DecoBuild phase.
// Mirrors buildStatusOf() in the admin's build.ts.
func (r *BuildReconciler) syncStatus(ctx context.Context, decoBuild *decositesv1alpha1.DecoBuild, job *batchv1.Job) (ctrl.Result, error) {
	phase := jobPhase(job)
	if phase == decoBuild.Status.Phase {
		return ctrl.Result{}, nil
	}

	decoBuild.Status.Phase = phase
	if phase == decositesv1alpha1.DecoBuildPhaseSucceeded || phase == decositesv1alpha1.DecoBuildPhaseFailed {
		now := metav1.Now()
		decoBuild.Status.CompletionTime = &now
	}
	return ctrl.Result{}, r.Status().Update(ctx, decoBuild)
}

// jobPhase maps batchv1.Job conditions to a DecoBuildPhase.
func jobPhase(job *batchv1.Job) decositesv1alpha1.DecoBuildPhase {
	for _, c := range job.Status.Conditions {
		if c.Status != corev1.ConditionTrue {
			continue
		}
		switch c.Type {
		case batchv1.JobComplete, "SuccessCriteriaMet":
			return decositesv1alpha1.DecoBuildPhaseSucceeded
		case batchv1.JobFailed:
			return decositesv1alpha1.DecoBuildPhaseFailed
		}
	}
	return decositesv1alpha1.DecoBuildPhaseRunning
}

// resolveGithubToken returns the GitHub token from spec or from the referenced Secret.
// GithubTokenSecret takes precedence over the inline GithubToken field.
func (r *BuildReconciler) resolveGithubToken(ctx context.Context, decoBuild *decositesv1alpha1.DecoBuild) (string, error) {
	if secretName := decoBuild.Spec.GithubTokenSecret; secretName != "" {
		secret := &corev1.Secret{}
		if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: decoBuild.Namespace}, secret); err != nil {
			return "", err
		}
		return string(secret.Data["token"]), nil
	}
	return decoBuild.Spec.GithubToken, nil
}

func (r *BuildReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&decositesv1alpha1.DecoBuild{}).
		Owns(&batchv1.Job{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 4}).
		Named("decobuild").
		Complete(r)
}

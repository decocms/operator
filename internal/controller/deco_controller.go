package controller

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
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

const (
	phaseRunning        = "Running"
	phaseSucceeded      = "Succeeded"
	phaseFailed         = "Failed"
	DecoControllerName  = "deco"
)

// DecoReconciler reconciles Deco objects.
type DecoReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	Builder              build.Builder
	BuilderSAAnnotations map[string]string
}

// +kubebuilder:rbac:groups=deco.sites,resources=decos,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=deco.sites,resources=decos/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=deco.sites,resources=decos/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update

func (r *DecoReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	deco := &decositesv1alpha1.Deco{}
	if err := r.Get(ctx, req.NamespacedName, deco); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	base := deco.DeepCopy()
	patch := deco.DeepCopy()
	statusChanged := false

	if deco.Spec.Build != nil && deco.Spec.Build.Source.CommitSha != "" {
		changed, err := r.reconcileProductionBuild(ctx, log, deco, patch)
		if err != nil {
			return ctrl.Result{}, err
		}
		statusChanged = statusChanged || changed
	}

	if deco.Spec.Previews != nil && len(deco.Spec.Previews.Active) > 0 {
		changed, err := r.reconcilePreviewBuilds(ctx, log, deco, patch)
		if err != nil {
			return ctrl.Result{}, err
		}
		statusChanged = statusChanged || changed
	} else if len(deco.Status.Previews) > 0 {
		patch.Status.Previews = nil
		statusChanged = true
	}

	if statusChanged {
		return ctrl.Result{}, r.Status().Patch(ctx, patch, client.MergeFrom(base))
	}
	return ctrl.Result{}, nil
}

func (r *DecoReconciler) reconcileProductionBuild(ctx context.Context, log logr.Logger, deco, patch *decositesv1alpha1.Deco) (bool, error) {
	commitSha := deco.Spec.Build.Source.CommitSha

	if deco.Status.Build != nil && deco.Status.Build.LastBuiltCommit == commitSha {
		return false, nil
	}

	jobName := build.JobName(commitSha, deco.Spec.Site)

	existingJob := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: deco.Namespace}, existingJob)
	if errors.IsNotFound(err) {
		if deco.Spec.Serving == nil {
			return false, fmt.Errorf("spec.serving is required")
		}
		log.Info("Creating production build job", "job", jobName, "site", deco.Spec.Site)
		if err := r.createJob(ctx, deco, jobName, deco.Spec.Build.Source); err != nil {
			return false, err
		}
		now := metav1.Now()
		if patch.Status.Build == nil {
			patch.Status.Build = &decositesv1alpha1.DecoStatusBuild{}
		}
		patch.Status.Build.Phase = phaseRunning
		patch.Status.Build.CommitSha = commitSha
		patch.Status.Build.JobName = jobName
		patch.Status.Build.StartTime = &now
		return true, nil
	}
	if err != nil {
		return false, err
	}

	phase := buildPhaseFromJob(existingJob)
	currentPhase := ""
	if deco.Status.Build != nil {
		currentPhase = deco.Status.Build.Phase
	}
	if currentPhase == phase {
		return false, nil
	}

	if patch.Status.Build == nil {
		patch.Status.Build = &decositesv1alpha1.DecoStatusBuild{}
	}
	patch.Status.Build.Phase = phase
	if phase == phaseSucceeded || phase == phaseFailed {
		now := metav1.Now()
		patch.Status.Build.CompletionTime = &now
		if deco.Status.Build != nil && deco.Status.Build.StartTime != nil {
			duration := now.Sub(deco.Status.Build.StartTime.Time).Seconds()
			RecordBuild(deco.Spec.Site, phase, "production", duration)
		}
	}
	if phase == phaseSucceeded {
		patch.Status.Build.LastBuiltCommit = commitSha
	}
	return true, nil
}

func (r *DecoReconciler) reconcilePreviewBuilds(ctx context.Context, log logr.Logger, deco, patch *decositesv1alpha1.Deco) (bool, error) {
	policy := deco.Spec.Previews
	active := policy.Active

	if policy.MaxActive > 0 && int32(len(active)) > policy.MaxActive {
		active = active[len(active)-int(policy.MaxActive):]
	}

	existingByCommit := map[string]decositesv1alpha1.DecoPreviewStatus{}
	for _, s := range deco.Status.Previews {
		existingByCommit[s.CommitSha] = s
	}

	activeCommits := map[string]bool{}
	for _, p := range active {
		activeCommits[p.CommitSha] = true
	}

	newStatuses := make([]decositesv1alpha1.DecoPreviewStatus, 0, len(active))
	changed := false

	for _, preview := range active {
		jobName := build.JobName(preview.CommitSha, deco.Spec.Site)

		if s, ok := existingByCommit[preview.CommitSha]; ok && s.Phase == phaseSucceeded {
			newStatuses = append(newStatuses, s)
			continue
		}

		existingJob := &batchv1.Job{}
		err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: deco.Namespace}, existingJob)
		if errors.IsNotFound(err) {
			if deco.Spec.Serving == nil {
				return false, fmt.Errorf("spec.serving is required")
			}
			log.Info("Creating preview build job", "job", jobName, "site", deco.Spec.Site, "branchRef", preview.BranchRef)
			source := decositesv1alpha1.DecoSpecBuildSource{
				CommitSha:  preview.CommitSha,
				Production: false,
				BranchRef:  preview.BranchRef,
			}
			if err := r.createJob(ctx, deco, jobName, source); err != nil {
				return false, err
			}
			now := metav1.Now()
			newStatuses = append(newStatuses, decositesv1alpha1.DecoPreviewStatus{
				CommitSha: preview.CommitSha,
				BranchRef: preview.BranchRef,
				PrId:      preview.PrId,
				JobName:   jobName,
				Phase:     "Running",
				StartTime: &now,
			})
			changed = true
			continue
		}
		if err != nil {
			return false, err
		}

		phase := buildPhaseFromJob(existingJob)
		s := decositesv1alpha1.DecoPreviewStatus{
			CommitSha: preview.CommitSha,
			BranchRef: preview.BranchRef,
			PrId:      preview.PrId,
			JobName:   jobName,
			Phase:     phase,
		}
		if existing, ok := existingByCommit[preview.CommitSha]; ok {
			s.StartTime = existing.StartTime
			if (phase == phaseSucceeded || phase == phaseFailed) && existing.CompletionTime == nil {
				now := metav1.Now()
				s.CompletionTime = &now
				if existing.StartTime != nil {
					duration := now.Sub(existing.StartTime.Time).Seconds()
					RecordBuild(deco.Spec.Site, phase, "preview", duration)
				}
			} else {
				s.CompletionTime = existing.CompletionTime
			}
			if existing.Phase != phase {
				changed = true
			}
		} else {
			changed = true
		}
		newStatuses = append(newStatuses, s)
	}

	// Detect removed previews
	for _, s := range deco.Status.Previews {
		if !activeCommits[s.CommitSha] {
			changed = true
			break
		}
	}

	if changed {
		patch.Status.Previews = newStatuses
	}
	return changed, nil
}

// createJob creates a K8s Job for either a production or preview build.
func (r *DecoReconciler) createJob(ctx context.Context, deco *decositesv1alpha1.Deco, jobName string, source decositesv1alpha1.DecoSpecBuildSource) error {
	job, err := r.Builder.NewJob(ctx, deco, jobName, source)
	if err != nil {
		return fmt.Errorf("building job spec: %w", err)
	}

	if sa := job.Spec.Template.Spec.ServiceAccountName; sa != "" {
		if err := ensureServiceAccount(ctx, r.Client, deco.Namespace, sa, r.BuilderSAAnnotations); err != nil {
			return fmt.Errorf("ensuring service account %q: %w", sa, err)
		}
	}

	if err := controllerutil.SetControllerReference(deco, job, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference: %w", err)
	}

	if err := r.Create(ctx, job); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("creating build job: %w", err)
	}
	return nil
}

func buildPhaseFromJob(job *batchv1.Job) string {
	for _, c := range job.Status.Conditions {
		if c.Status != corev1.ConditionTrue {
			continue
		}
		switch c.Type {
		case batchv1.JobComplete, "SuccessCriteriaMet":
			return phaseSucceeded
		case batchv1.JobFailed:
			return phaseFailed
		}
	}
	return phaseRunning
}

func (r *DecoReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&decositesv1alpha1.Deco{}).
		Owns(&batchv1.Job{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 4}).
		Named("deco-build").
		Complete(r)
}

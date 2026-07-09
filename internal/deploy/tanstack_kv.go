package deploy

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
	"github.com/deco-sites/decofile-operator/internal/envparse"
	"github.com/deco-sites/decofile-operator/internal/githubapp"
)

// condSynced is the Decofile condition the tanstack-kv strategy manages.
const condSynced = "Synced"

// requeueWhileRunning is how often the reconciler re-checks an in-flight sync Job.
const requeueWhileRunning = 10 * time.Second

// TanstackKVConfig holds the cluster-level configuration the KV-sync Job needs.
// Per-site values (KV namespace id, site origin, repo, commit) come from the
// Decofile CR; these are the operator-global defaults.
type TanstackKVConfig struct {
	// SyncerImage is the decofile-syncer image (repository:tag).
	SyncerImage string
	// ServiceAccount optionally pins the ServiceAccount for the sync pod. Empty
	// (the default) means the namespace's "default" SA — the sync Job needs no
	// special identity (it clones via a GitHub token and writes KV via a CF
	// token, both from env; no AWS/IRSA, no Kubernetes API). Do NOT default this
	// to the build SA: that only exists in the operator namespace, so it would
	// break sync Jobs created in a site's own namespace.
	ServiceAccount string
	// GithubToken is a static fallback for cloning private site repos, used only
	// when GitHubApp is nil (no GitHub App configured).
	GithubToken string
	// GitHubApp, when set, mints a short-lived repo-scoped installation token per
	// sync (preferred over GithubToken) — same mechanism admin uses.
	GitHubApp *githubapp.App
	// CfAccountId / CfApiToken authenticate the Cloudflare KV REST writes.
	CfAccountId string
	CfApiToken  string
	// PurgeToken is the bearer token for the site's POST /_cache/purge (optional).
	PurgeToken string
	// TTLSeconds controls how long the finished Job is kept before GC.
	TTLSeconds   int32
	NodeSelector map[string]string
	Tolerations  []corev1.Toleration
}

// TanstackKVConfigFromEnv reads TanstackKVConfig from environment variables.
func TanstackKVConfigFromEnv() TanstackKVConfig {
	return TanstackKVConfig{
		SyncerImage:    os.Getenv("DECOFILE_SYNCER_IMAGE"),
		ServiceAccount: os.Getenv("DECOFILE_SYNC_SERVICE_ACCOUNT"),
		GithubToken:    os.Getenv("GITHUB_TOKEN"),
		CfAccountId:    os.Getenv("CLOUDFLARE_ACCOUNT_ID"),
		CfApiToken:     os.Getenv("CLOUDFLARE_KV_API_TOKEN"),
		PurgeToken:     os.Getenv("DECO_PURGE_TOKEN"),
		TTLSeconds:     5 * 60,
		NodeSelector:   envparse.NodeSelector(os.Getenv("BUILD_NODE_SELECTOR")),
		Tolerations:    envparse.Tolerations(os.Getenv("BUILD_TOLERATIONS")),
	}
}

type tanstackKV struct {
	cfg TanstackKVConfig
	// resolveLiveID reads the site's index:live pointer (the deployment id whose
	// content the sync should target). Injectable so tests avoid real HTTP.
	resolveLiveID func(ctx context.Context, namespaceID string) (string, error)
}

// NewTanstackKV returns the FastDeployment for Decofile target = "tanstack-kv".
func NewTanstackKV(cfg TanstackKVConfig) FastDeployment {
	d := &tanstackKV{cfg: cfg}
	d.resolveLiveID = func(ctx context.Context, namespaceID string) (string, error) {
		return fetchKVLiveID(ctx, cfg.CfAccountId, cfg.CfApiToken, namespaceID)
	}
	return d
}

// SyncJobName is the deterministic Job name for a (commit, site) sync. Uses a
// 64-bit hash slice so distinct commits don't collide onto the same Job name
// (which would make a new commit falsely read as already-synced).
func SyncJobName(commit, site string) string {
	h := sha256.Sum256([]byte("decofile-sync-" + commit + "-" + site))
	return fmt.Sprintf("decofile-sync-%x", h[:8])
}

func (d *tanstackKV) Reconcile(ctx context.Context, c client.Client, scheme *runtime.Scheme, df *decositesv1alpha1.Decofile) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if df.Spec.GitHub == nil || df.Spec.GitHub.Commit == "" {
		return ctrl.Result{}, errMisconfigured(decositesv1alpha1.TargetTanstackKV, "spec.github with a commit is required")
	}
	if df.Spec.TanstackKV == nil || df.Spec.TanstackKV.KVNamespaceID == "" {
		return ctrl.Result{}, errMisconfigured(decositesv1alpha1.TargetTanstackKV, "spec.tanstackKV.kvNamespaceId is required")
	}

	commit := df.Spec.GitHub.Commit
	site := df.Spec.GitHub.Repo
	jobName := SyncJobName(commit, site)

	// Already synced this commit → nothing to do (idempotent on re-delivered events).
	if df.Status.GitHubCommit == commit &&
		apimeta.IsStatusConditionTrue(df.Status.Conditions, condSynced) {
		return ctrl.Result{}, nil
	}

	job := &batchv1.Job{}
	err := c.Get(ctx, client.ObjectKey{Name: jobName, Namespace: df.Namespace}, job)
	switch {
	case apierrors.IsNotFound(err):
		// Content is keyed per deployment, so a content-only fast-deploy must
		// target whichever version is currently LIVE. Resolve index:live before
		// creating the Job.
		liveID, lerr := d.resolveLiveID(ctx, df.Spec.TanstackKV.KVNamespaceID)
		if lerr != nil {
			return ctrl.Result{}, fmt.Errorf("resolve live deployment id: %w", lerr)
		}
		if liveID == "" {
			// No code deploy has set index:live yet — there is no live version to
			// fast-deploy onto. Wait; the first code deploy sets the pointer.
			log.Info("index:live not set; waiting for the first code deploy", "kvNamespace", df.Spec.TanstackKV.KVNamespaceID)
			d.setStatus(df, "", commit, metav1.ConditionUnknown, "Waiting", "index:live not set — waiting for the first code deploy to set the live pointer")
			if err := c.Status().Update(ctx, df); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: requeueWhileRunning}, nil
		}

		githubToken, terr := d.resolveGitHubToken(ctx, df.Spec.GitHub.Org, df.Spec.GitHub.Repo)
		if terr != nil {
			return ctrl.Result{}, fmt.Errorf("resolve GitHub token: %w", terr)
		}
		job = d.buildJob(df, jobName, site, githubToken, liveID)
		if err := controllerutil.SetControllerReference(df, job, scheme); err != nil {
			return ctrl.Result{}, fmt.Errorf("set owner ref on sync Job: %w", err)
		}
		if err := c.Create(ctx, job); err != nil {
			if apierrors.IsAlreadyExists(err) {
				return ctrl.Result{RequeueAfter: requeueWhileRunning}, nil
			}
			return ctrl.Result{}, fmt.Errorf("create sync Job: %w", err)
		}
		log.Info("Created decofile KV-sync Job", "job", jobName, "commit", commit, "deploymentId", liveID, "kvNamespace", df.Spec.TanstackKV.KVNamespaceID)
		d.setStatus(df, jobName, commit, metav1.ConditionUnknown, "Syncing", "KV sync Job created")
		if err := c.Status().Update(ctx, df); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: requeueWhileRunning}, nil
	case err != nil:
		return ctrl.Result{}, fmt.Errorf("get sync Job: %w", err)
	}

	// Job exists — check terminal state.
	switch {
	case isJobComplete(job):
		log.Info("decofile KV-sync Job completed", "job", jobName, "commit", commit)
		d.setStatus(df, jobName, commit, metav1.ConditionTrue, "SyncSucceeded", "decofile pushed to Cloudflare KV")
		return ctrl.Result{}, c.Status().Update(ctx, df)
	case isJobFailed(job):
		log.Info("decofile KV-sync Job failed", "job", jobName, "commit", commit)
		d.setStatus(df, jobName, commit, metav1.ConditionFalse, "SyncFailed", "KV-sync Job failed; see Job logs")
		// Don't requeue forever — a new commit yields a new Job name. The failed
		// Job is GC'd by TTLSecondsAfterFinished.
		return ctrl.Result{}, c.Status().Update(ctx, df)
	default:
		// Still running.
		return ctrl.Result{RequeueAfter: requeueWhileRunning}, nil
	}
}

func (d *tanstackKV) setStatus(df *decositesv1alpha1.Decofile, jobName, commit string, status metav1.ConditionStatus, reason, msg string) {
	df.Status.JobName = jobName
	df.Status.SourceType = decositesv1alpha1.SourceGitHub
	df.Status.LastUpdated = metav1.Now()
	if status == metav1.ConditionTrue {
		df.Status.GitHubCommit = commit
	}
	apimeta.SetStatusCondition(&df.Status.Conditions, metav1.Condition{
		Type:    condSynced,
		Status:  status,
		Reason:  reason,
		Message: msg,
	})
}

// resolveGitHubToken mints a short-lived, repo-scoped installation token via the
// GitHub App when configured (preferred), falling back to the static token
// (which may be empty for public repos).
func (d *tanstackKV) resolveGitHubToken(ctx context.Context, owner, repo string) (string, error) {
	if d.cfg.GitHubApp != nil {
		return d.cfg.GitHubApp.InstallationToken(ctx, owner, repo)
	}
	return d.cfg.GithubToken, nil
}

func (d *tanstackKV) buildJob(df *decositesv1alpha1.Decofile, jobName, site, githubToken, deploymentID string) *batchv1.Job {
	gh := df.Spec.GitHub
	env := []corev1.EnvVar{
		{Name: "GIT_REPO", Value: fmt.Sprintf("https://github.com/%s/%s", gh.Org, gh.Repo)},
		{Name: "COMMIT_SHA", Value: gh.Commit},
		{Name: "DECO_SITE_NAME", Value: site},
		{Name: "BUILD_NAME", Value: jobName},
		// The content is written under the LIVE deployment's key, not this
		// content commit's sha — the syncer runs `--deployment-id $DEPLOYMENT_ID`.
		{Name: "DEPLOYMENT_ID", Value: deploymentID},
		{Name: "CF_ACCOUNT_ID", Value: d.cfg.CfAccountId},
		{Name: "CF_API_TOKEN", Value: d.cfg.CfApiToken},
		{Name: "CF_KV_NAMESPACE_ID", Value: df.Spec.TanstackKV.KVNamespaceID},
	}
	if githubToken != "" {
		env = append(env, corev1.EnvVar{Name: "GITHUB_TOKEN", Value: githubToken})
	}
	if origin := df.Spec.TanstackKV.SiteOrigin; origin != "" {
		env = append(env, corev1.EnvVar{Name: "PURGE_URL", Value: origin})
		if d.cfg.PurgeToken != "" {
			env = append(env, corev1.EnvVar{Name: "PURGE_TOKEN", Value: d.cfg.PurgeToken})
		}
	}

	backoffLimit := int32(2)
	ttl := d.cfg.TTLSeconds

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: df.Namespace,
			Labels: map[string]string{
				"app.deco/site":   site,
				"app.deco/target": decositesv1alpha1.TargetTanstackKV,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: d.cfg.ServiceAccount,
					NodeSelector:       d.cfg.NodeSelector,
					Tolerations:        d.cfg.Tolerations,
					Containers: []corev1.Container{
						{
							Name:  "syncer",
							Image: d.cfg.SyncerImage,
							Env:   env,
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceMemory:           resource.MustParse("256Mi"),
									corev1.ResourceCPU:              resource.MustParse("250m"),
									corev1.ResourceEphemeralStorage: resource.MustParse("1Gi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceMemory:           resource.MustParse("1Gi"),
									corev1.ResourceEphemeralStorage: resource.MustParse("2Gi"),
								},
							},
						},
					},
				},
			},
		},
	}
}

func isJobComplete(j *batchv1.Job) bool {
	for _, c := range j.Status.Conditions {
		if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func isJobFailed(j *batchv1.Job) bool {
	for _, c := range j.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

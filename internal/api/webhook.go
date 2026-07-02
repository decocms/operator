package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
	"github.com/deco-sites/decofile-operator/internal/deploy"
)

// maxWebhookBody caps the GitHub payload we read (GitHub push payloads are well
// under this; the cap prevents a malicious unbounded body).
const maxWebhookBody = 8 << 20 // 8 MiB

// WebhookHandlers serves the git webhook that drives fast-deploy. It verifies
// the HMAC signature, resolves the site's Deco CR, and asks the DeploymentTarget
// registry to plan desired-state CRs, which it then applies.
type WebhookHandlers struct {
	client  client.Client
	secret  string
	targets *deploy.TargetRegistry
}

func NewWebhookHandlers(c client.Client, secret string, targets *deploy.TargetRegistry) *WebhookHandlers {
	return &WebhookHandlers{client: c, secret: secret, targets: targets}
}

// Enabled reports whether the webhook is fully configured (secret + targets).
func (h *WebhookHandlers) Enabled() bool {
	return h != nil && h.secret != "" && h.targets != nil
}

type githubPushPayload struct {
	Ref        string `json:"ref"`
	After      string `json:"after"`
	Repository struct {
		Name          string `json:"name"`
		DefaultBranch string `json:"default_branch"`
		Owner         struct {
			Login string `json:"login"`
			Name  string `json:"name"`
		} `json:"owner"`
	} `json:"repository"`
	Commits []struct {
		Added    []string `json:"added"`
		Modified []string `json:"modified"`
		Removed  []string `json:"removed"`
	} `json:"commits"`
}

// handleGitHub is the POST /webhooks/github handler. Auth is the HMAC signature,
// NOT basic auth, so it is mounted outside the basic-auth wrapper.
func (h *WebhookHandlers) handleGitHub(w http.ResponseWriter, r *http.Request) {
	log := logf.FromContext(r.Context())

	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	if !verifySignature(h.secret, body, r.Header.Get("X-Hub-Signature-256")) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	// Acknowledge non-push events (e.g. the "ping" GitHub sends on setup) so the
	// webhook shows green in the GitHub UI.
	if event := r.Header.Get("X-GitHub-Event"); event != "push" {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "ignored event %q", event)
		return
	}

	var payload githubPushPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Only act on pushes to the repo's default branch.
	defaultBranch := payload.Repository.DefaultBranch
	if defaultBranch == "" {
		defaultBranch = "main"
	}
	if payload.Ref != "refs/heads/"+defaultBranch {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ignored non-default-branch push"))
		return
	}

	owner := payload.Repository.Owner.Login
	if owner == "" {
		owner = payload.Repository.Owner.Name
	}
	push := deploy.PushEvent{
		Owner:        owner,
		Repo:         payload.Repository.Name,
		Commit:       payload.After,
		ChangedFiles: changedFiles(payload),
	}

	// Resolve the site's Deco CR (source of truth for serving type + KV config).
	deco, err := h.resolveDeco(r.Context(), push.Owner, push.Repo)
	if err != nil {
		log.Error(err, "failed to resolve Deco for repo", "owner", push.Owner, "repo", push.Repo)
		http.Error(w, "failed to resolve site", http.StatusInternalServerError)
		return
	}
	if deco == nil {
		// No site registered for this repo → nothing to do.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("no matching Deco for repo"))
		return
	}

	objs, err := h.targets.Plan(r.Context(), push, deploy.SiteConfig{Deco: deco})
	if err != nil {
		log.Error(err, "deployment target Plan failed")
		http.Error(w, "plan failed", http.StatusInternalServerError)
		return
	}
	if len(objs) == 0 {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("nothing to deploy"))
		return
	}

	for _, obj := range objs {
		if err := h.apply(r.Context(), obj); err != nil {
			log.Error(err, "failed to apply planned object", "name", obj.GetName())
			http.Error(w, "apply failed", http.StatusInternalServerError)
			return
		}
	}

	log.Info("fast-deploy triggered", "owner", push.Owner, "repo", push.Repo, "commit", push.Commit, "objects", len(objs))
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte("fast-deploy triggered"))
}

// resolveDeco finds the Deco CR for owner/repo by matching spec.org + spec.site.
// Returns nil (not an error) when no site is registered for the repo.
func (h *WebhookHandlers) resolveDeco(ctx context.Context, owner, repo string) (*decositesv1alpha1.Deco, error) {
	list := &decositesv1alpha1.DecoList{}
	if err := h.client.List(ctx, list); err != nil {
		return nil, err
	}
	for i := range list.Items {
		d := &list.Items[i]
		if strings.EqualFold(d.Spec.Org, owner) && strings.EqualFold(d.Spec.Site, repo) {
			return d, nil
		}
	}
	return nil, nil
}

// apply creates the planned object, or updates its spec/labels when it already
// exists. The Decofile name is deterministic per site, so repeated pushes
// converge on one CR.
func (h *WebhookHandlers) apply(ctx context.Context, obj client.Object) error {
	df, ok := obj.(*decositesv1alpha1.Decofile)
	if !ok {
		return fmt.Errorf("unsupported planned object type %T", obj)
	}
	existing := &decositesv1alpha1.Decofile{}
	err := h.client.Get(ctx, client.ObjectKeyFromObject(df), existing)
	if apierrors.IsNotFound(err) {
		return h.client.Create(ctx, df)
	}
	if err != nil {
		return err
	}
	existing.Spec = df.Spec
	existing.Labels = df.Labels
	return h.client.Update(ctx, existing)
}

// changedFiles is the union of added/modified/removed paths across all commits
// in the push.
func changedFiles(p githubPushPayload) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(paths []string) {
		for _, f := range paths {
			if _, ok := seen[f]; ok {
				continue
			}
			seen[f] = struct{}{}
			out = append(out, f)
		}
	}
	for _, c := range p.Commits {
		add(c.Added)
		add(c.Modified)
		add(c.Removed)
	}
	return out
}

// verifySignature checks the GitHub HMAC-SHA256 signature header
// ("sha256=<hex>") against the raw body using the shared secret.
func verifySignature(secret string, body []byte, header string) bool {
	if secret == "" || header == "" {
		return false
	}
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := mac.Sum(nil)
	got, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return false
	}
	return hmac.Equal(got, want)
}

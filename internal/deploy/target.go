// Package deploy holds the two pluggable seams of the git-driven fast-deploy
// flow:
//
//	DeploymentTarget  — maps a git push to desired-state CR(s)            (webhook side)
//	FastDeployment    — drives a Decofile CR to its effect (e.g. KV sync) (watcher side)
//
// Each is a small interface with a name-keyed registry so new deploy targets
// and execution strategies plug in without touching the webhook or reconciler.
// The first (and currently only) implementations are cloudflare-workers
// (DeploymentTarget) and tanstack-kv (FastDeployment).
package deploy

import (
	"context"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
)

// Content-only definition: a push is a fast-deploy candidate when EVERY changed
// file matches one of these paths. Studio content commits touch two places —
// the decofile blocks themselves and the regenerated bundled snapshot it keeps
// in lockstep for HMR.
const (
	// blocksPrefix is the repo-relative directory holding the decofile blocks.
	blocksPrefix = ".deco/blocks/"
	// blocksGenFile is the bundled snapshot regenerated alongside a content
	// commit (used for HMR). It is derived from .deco/blocks, so its presence
	// in a diff carries no code change. 7.x (@decocms/blocks-cli) emits it at
	// .deco/blocks.gen.json.
	blocksGenFile = ".deco/blocks.gen.json"
	// blocksGenFileLegacy is the pre-7.x location (@decocms/start@6, emitted
	// under src/server/cms/). Kept so sites not yet on the split packages still
	// fast-deploy content-only commits.
	blocksGenFileLegacy = "src/server/cms/blocks.gen.json"
)

// PushEvent is the normalized git push the webhook hands to a DeploymentTarget.
type PushEvent struct {
	Owner        string
	Repo         string
	Commit       string
	ChangedFiles []string
}

// SiteConfig is the resolved per-site configuration for a push. The Deco CR is
// the source of truth (site, serving type, and fast-deploy/KV settings).
type SiteConfig struct {
	Deco *decositesv1alpha1.Deco
}

// DeploymentTarget maps a push (+ resolved site config) to the desired-state
// objects to create/apply. It returns an empty slice when there is nothing to
// deploy (e.g. a code-only change, or fast-deploy disabled for the site).
type DeploymentTarget interface {
	Plan(ctx context.Context, push PushEvent, site SiteConfig) ([]client.Object, error)
}

// TargetRegistry dispatches to a DeploymentTarget by the site's serving type
// (deco.spec.serving.type, e.g. "cloudflare-worker"). It is itself a
// DeploymentTarget.
type TargetRegistry struct {
	targets map[string]DeploymentTarget
}

func NewTargetRegistry() *TargetRegistry {
	return &TargetRegistry{targets: map[string]DeploymentTarget{}}
}

func (r *TargetRegistry) Register(servingType string, t DeploymentTarget) {
	r.targets[servingType] = t
}

func (r *TargetRegistry) Plan(ctx context.Context, push PushEvent, site SiteConfig) ([]client.Object, error) {
	serving := ""
	if site.Deco != nil && site.Deco.Spec.Serving != nil {
		serving = site.Deco.Spec.Serving.Type
	}
	t, ok := r.targets[serving]
	if !ok {
		// No target registered for this serving type → nothing to do. Code-only
		// or unsupported sites fall through to the normal build/deploy path.
		return nil, nil
	}
	return t.Plan(ctx, push, site)
}

// isContentOnly reports whether every changed file is a content path — under
// .deco/blocks/ or the regenerated bundled snapshot (blocksGenFile). An empty
// file list is treated as not content-only — we can't prove it's content, so
// we don't fast-deploy it.
func isContentOnly(files []string) bool {
	if len(files) == 0 {
		return false
	}
	for _, f := range files {
		if !isContentPath(f) {
			return false
		}
	}
	return true
}

// isContentPath reports whether a single changed file counts as content.
func isContentPath(f string) bool {
	return strings.HasPrefix(f, blocksPrefix) || f == blocksGenFile || f == blocksGenFileLegacy
}

// maxNameLen is the Kubernetes object-name ceiling (DNS-1123 label).
const maxNameLen = 63

// decofileName is the deterministic Decofile CR name for a site's fast-deploy
// content. Stable per site so repeated pushes update the same CR. The site is
// sanitized to a DNS-1123 label and the whole name capped at 63 chars so any
// repo/site naming variant yields a valid object name.
func decofileName(site string) string {
	name := "fastdeploy-" + sanitizeDNS1123(site)
	if len(name) > maxNameLen {
		name = name[:maxNameLen]
	}
	return strings.Trim(name, "-")
}

// sanitizeDNS1123 lowercases the input and replaces any char outside
// [a-z0-9-] with '-', trimming leading/trailing dashes. Returns "site" if the
// result is empty.
func sanitizeDNS1123(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "site"
	}
	return out
}

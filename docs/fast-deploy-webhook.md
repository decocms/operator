# Fast-Deploy Webhook & Content Sync

The operator turns a content-only git push into a Cloudflare KV content update
("fast deploy"), with **no** code redeploy. Flow:

```
git push (main, content-only: .deco/blocks/** + .deco/blocks.gen.json)
  → GitHub webhook → operator  POST /webhooks/github   (HMAC-verified)
  → DeploymentTarget (cloudflare-workers) resolves the repo's Deco CR,
    creates/updates a Decofile CR (target: tanstack-kv)
  → DecofileReconciler dispatches to the tanstack-kv FastDeployment
  → resolve the site's LIVE deployment id (read index:live from KV)
    (unset → Waiting + requeue; no live version to update yet)
  → self-cleaning Job (decofile-syncer image): clone repo@commit +
    `deco-sync-blocks-to-kv --write --all --deployment-id <liveId>`  →  Cloudflare KV
```

Content is keyed **per deployment** in KV (`decofile:<id>`, `id` = commit sha), so
each running code version reads its own snapshot. A content-only push must update
whichever version is currently **live**, so the reconciler resolves `index:live`
first and passes it to the sync Job as `DEPLOYMENT_ID`. The `index:live` pointer is
set by the site's deploy command (post-activation), not by the operator.

## 1. Operator deployment env

Set on the operator Deployment (provisioning):

| Env | Required | Purpose |
|-----|----------|---------|
| `GITHUB_WEBHOOK_SECRET` | yes (enables the webhook) | HMAC secret you generate; the SAME value goes in the GitHub webhook's "Secret" field (see §3) |
| `DECOFILE_SYNCER_IMAGE` | yes | `ghcr.io/decocms/infra_applications/decofile-syncer:<tag>` |
| `CLOUDFLARE_ACCOUNT_ID` | yes | account id for KV writes (`CF_ACCOUNT_ID` in the Job) AND the operator's `index:live` REST lookup, e.g. `c95fc4cec7fc52453228d9db170c372c` |
| `CLOUDFLARE_KV_API_TOKEN` | yes | CF API token with **Workers KV Storage: Edit** (`CF_API_TOKEN` in the Job); the operator also uses it to read `index:live` before creating a sync Job |
| `GITHUB_APP_ID` + `GITHUB_APP_PRIVATE_KEY` | for private repos (preferred) | GitHub App creds; the operator mints a short-lived, repo-scoped installation token per sync (same as admin). Private key PEM (PKCS#1 or PKCS#8); literal `\n` is unescaped |
| `GITHUB_TOKEN` | private repos (fallback) | static token used only when no GitHub App is configured |
| `DECO_PURGE_TOKEN` | optional | bearer token for the site's `/_cache/purge` |
| `BUILD_SERVICE_ACCOUNT` | optional | ServiceAccount for the sync Job pod |
| `BUILD_NODE_SELECTOR` / `BUILD_TOLERATIONS` | optional | JSON; pins sync pods to a node pool |
| `OPERATOR_API_ADDR` | optional | API/webhook listen addr (default `:9090`) |

The HTTP server starts when either the redirects API (`OPERATOR_API_USER`/
`OPERATOR_API_PASSWORD`) **or** the webhook (`GITHUB_WEBHOOK_SECRET`) is configured.
The webhook authenticates by signature and is independent of the basic-auth API.

**Private repos** use the same mechanism as admin (`utils/loaders/github/tokens.ts`): the
operator signs an RS256 App JWT, looks up the repo's installation, and exchanges it for an
access token scoped to that one repo — injected into the sync Job as `GITHUB_TOKEN`. This is
preferred over a static `GITHUB_TOKEN` (short-lived, least-privilege). Public repos need
neither.

## 2. Per-site config — the `Deco` CR

Add `spec.fastDeploy` to the site's `Deco` CR (source of truth the webhook reads):

```yaml
apiVersion: deco.sites/v1alpha1
kind: Deco
spec:
  site: my-store          # repo name
  org: deco-sites         # repo owner
  serving:
    type: cloudflare-worker
  fastDeploy:
    enabled: true
    kvNamespaceId: <cloudflare KV namespace id for this site>   # one per site
    siteOrigin: https://www.my-store.com                        # optional, for cache purge
```

Content pushes are fast-deployed only when `serving.type=cloudflare-worker` **and**
`fastDeploy.enabled=true`. Otherwise the push takes the normal build/deploy path.

## 3. GitHub webhook (org-level or per repo)

Add the webhook once at the **org** level (`https://github.com/organizations/<org>/settings/hooks`)
— it fires for every repo; the operator ignores pushes with no matching fast-deploy `Deco` CR
or non-content changes — or per repo (Settings → Webhooks). Either way:

- **Payload URL:** `https://<operator-host>/webhooks/github`
- **Content type:** `application/json`
- **Secret:** **you invent this value** (e.g. `openssl rand -hex 32`) and paste it into the
  webhook's "Secret" field. GitHub then HMAC-signs each delivery with it (`X-Hub-Signature-256`),
  and the operator verifies against `GITHUB_WEBHOOK_SECRET` (identical value). You do NOT define
  the payload — GitHub does; you only pick the event and set this shared secret. (This is exactly
  what admin does — `actions/github/webhooks/broker.ts`.)
- **Events:** "Just the push event"

The operator processes only pushes to the repo's default branch whose changed files
are **all content paths**: under `.deco/blocks/**` or the regenerated bundled snapshot
`.deco/blocks.gen.json` (7.x `@decocms/blocks-cli`) / `src/server/cms/blocks.gen.json`
(legacy `@decocms/start@6`) — Studio commits the blocks + the snapshot together. Any
other changed file makes it a code push → normal build path. A `ping` event (sent on
setup) returns `200`.

## 4. Verify

**Prerequisite:** the site must have deployed at least once with fast-deploy so
`index:live` is set. Until then a content push parks the CR in a `Waiting`
(`Synced=Unknown`) state and requeues — no Job is created.

1. Edit a `.deco/blocks/*.json`, push to `main`.
2. `kubectl get decofile -n <site-ns>` → a `fastdeploy-<site>` CR; a
   `decofile-sync-*` Job runs and completes; the CR's `Synced` condition flips True.
   The Job's `DEPLOYMENT_ID` env equals the site's `index:live` id.
3. Hit the live site — the change is visible within ~10s (the worker polls KV's
   `index:revision:<its id>`), no redeploy. Confirm exactly one `decofile refreshed`
   log (no poll loop → the Job's revision matches the runtime's).
4. The Job/pod is GC'd by `ttlSecondsAfterFinished`.

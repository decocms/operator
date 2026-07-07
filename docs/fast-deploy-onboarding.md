# Fast-Deploy — Onboarding a New Client

How to enable **KV-first content fast-deploy** for a site so CMS content edits
(`.deco/blocks/**`) go live in ~10s on a `git push`, **without** a `wrangler deploy`.

> Companion docs: platform/ops setup in [`fast-deploy-webhook.md`](./fast-deploy-webhook.md);
> runtime internals in `@decocms/start` `docs/fast-deploy.md`.

## Mental model (read this first)

Two sides must **both** be wired, per site — miss one and nothing happens:

| Side | Who | What | Effect |
|------|-----|------|--------|
| **Read** | the site's Worker (`@decocms/start`) | `DECO_KV` binding + `DECO_FAST_DEPLOY=1` | worker serves content from KV, polling `index:revision` every ~10s |
| **Write** | the operator (`Deco` CR) | `spec.fastDeploy.enabled + kvNamespaceId` | on a content push, operator syncs `.deco/blocks` → that KV namespace |

The `kvNamespaceId` on the `Deco` CR **must equal** the `DECO_KV` id on the worker — that's the shared channel. One KV namespace **per site** (see [tenancy](#constraints)).

## Prerequisites (one-time, platform-wide — already done)

- Operator ≥ **0.9.0** running on the sites (main) cluster, webhook exposed at `https://operator-serveless.infra.deco.cx/webhooks/github`.
- A **GitHub org webhook** on `deco-sites` (push events) → that URL. It fires for **every** repo; the operator ignores repos with no fast-deploy `Deco` CR — so **no per-repo webhook is needed**.
- AWS SM `hub/deco-operator/env` populated (unified operator secret) incl. `GITHUB_TOKEN`, `CLOUDFLARE_ACCOUNT_ID`, `CLOUDFLARE_KV_API_TOKEN`, `GITHUB_APP_ID/PRIVATE_KEY`, `GITHUB_WEBHOOK_SECRET`.

Quick health check (should return **401 `invalid signature`**, proving the chain is live):
```bash
curl -i -X POST https://operator-serveless.infra.deco.cx/webhooks/github \
  -H "X-GitHub-Event: push" -H "Content-Type: application/json" -d '{}'
```

## Per-client steps

### 1. Create a KV namespace (one per site)
```bash
wrangler kv namespace create DECO_KV        # in Cloudflare account c95fc4cec7fc52453228d9db170c372c
# → note the id, e.g. a205a78ee28c44c3910cf52a3482a65f
```
The namespace **must** live in account `c95fc4cec7fc52453228d9db170c372c` — that's the account the operator's `CLOUDFLARE_KV_API_TOKEN` writes to.

### 2. Wire the site's Worker (site repo `wrangler.toml`/`.jsonc`)
```toml
[[kv_namespaces]]
binding = "DECO_KV"
id = "<namespace id from step 1>"

[vars]
DECO_FAST_DEPLOY = "1"
```
Then **deploy the worker once** (a normal code deploy) so the binding + var take effect. The site must run a `@decocms/start` version with the KV read path.

### 3. Seed the namespace (recommended, once)
So the first cold start serves real content instead of the bundled `blocks.gen` fallback:
```bash
CF_ACCOUNT_ID=c95fc4cec7fc52453228d9db170c372c \
CF_KV_NAMESPACE_ID=<namespace id> \
CF_API_TOKEN=<Workers KV: Edit token> \
  npx -p @decocms/start deco-migrate-blocks-to-kv --write
# verify:
wrangler kv key get "index:revision"  --namespace-id <namespace id>
wrangler kv key get "decofile:current" --namespace-id <namespace id> | head -c 200
```

### 4. Enable fast-deploy on the site's `Deco` CR
The operator reads this to decide whether/where to sync. Set on the site's `Deco`
(namespace `sites-<site>`), e.g. via admin or `kubectl edit`:
```yaml
spec:
  serving:
    type: cloudflare-worker        # required — fast-deploy only handles this serving type
  fastDeploy:
    enabled: true
    kvNamespaceId: "<same id as the worker's DECO_KV>"
    siteOrigin: "https://<site public origin>"   # optional — enables POST /_cache/purge after sync
```
`spec.org` / `spec.site` must equal the repo `owner`/`name` (that's how the webhook finds this CR). `siteOrigin` also requires `DECO_PURGE_TOKEN` in the operator secret for purge to succeed (non-fatal if absent).

### 5. GitHub webhook
Nothing to do — the org-level webhook already covers the repo.

### 6. Test the round trip
Edit a `.deco/blocks/*.json`, commit, push to the default branch, then:
```bash
kubectl -n sites-<site> get decofile          # a "fastdeploy-<site>" Decofile CR appears (target: tanstack-kv)
kubectl -n sites-<site> get jobs -w            # a "decofile-sync-*" Job runs → Complete
kubectl -n sites-<site> get decofile fastdeploy-<site> -o jsonpath='{.status.conditions}'  # Synced=True
```
The live site reflects the change within ~10s (the worker's KV poll). No redeploy.

## Constraints

- **One KV namespace per site.** Isolation is by namespace, not key prefix (keys are fixed: `decofile:current`, `index:revision`). Two sites sharing a namespace would clobber each other. CF caps ~1000 namespaces/account.
- **All fast-deploy sites live in CF account `c95fc4cec7fc52453228d9db170c372c`** (single operator KV token/account).
- **Content-only pushes only.** A push is fast-deployed **only if every changed file is under `.deco/blocks/`**. A commit that also touches code takes the normal build/deploy path (not KV).

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| No `fastdeploy-<site>` CR after push | `Deco.spec.org/site` ≠ repo owner/name; or `serving.type` ≠ `cloudflare-worker`; or `fastDeploy.enabled` not true; or the push touched non-`.deco/blocks` files | fix the CR; make the content commit blocks-only |
| Sync Job fails | wrong `kvNamespaceId`, or `CLOUDFLARE_KV_API_TOKEN` lacks Workers KV: Edit, or private-repo clone lacks GitHub App install | check Job logs: `kubectl -n sites-<site> logs job/decofile-sync-…` |
| Site doesn't update though KV changed | worker missing `DECO_KV` binding or `DECO_FAST_DEPLOY=1` | add both to wrangler, redeploy |
| Worker reloads every ~10s (`decofile refreshed` looping) | `index:revision` ≠ `djb2(JSON.stringify(blocks))` — a non-script writer wrote KV | only write via `deco-sync-blocks-to-kv`; never hand-write the keys |
| GitHub `404` in operator logs | `GITHUB_TOKEN` missing/unauthorized for `deco-sites` | ensure it's in `hub/deco-operator/env`; restart the operator pod (envFrom is start-time only); authorize the PAT for org SSO |

## Disable / rollback

- **Stop new syncs:** set `spec.fastDeploy.enabled: false` on the `Deco` CR (or delete the `fastdeploy-<site>` Decofile CR).
- **Fully revert the site to bundled content:** remove `DECO_FAST_DEPLOY` (or set `0`) from the worker and redeploy — it serves the bundled `blocks.gen` snapshot immediately, no KV involvement. (The Studio `POST /.decofile` write-through path, if used, is independent.)

# Decofile S3 Target — Onboarding a Site

How to move a site's decofile off the etcd ConfigMap and onto S3, for sites
whose decofile is (or is close to) exceeding the **~1MB etcd ConfigMap limit**
(e.g. content-heavy sites with many/large blocks — blog posts with inline
HTML, large collections). Serves the decofile over **HTTP** instead of
mounting a ConfigMap — no size ceiling, same-VPC (no CDN, no internet hop).

> Architecture + sequence diagrams: [`ARCHITECTURE.md`](../ARCHITECTURE.md#decofile-delivery-s3-target-etcd-offload).
> Companion PRs this feature shipped across: `decocms/operator#33` (this repo),
> `deco-sites/admin#3302`, `decocms/infra_applications#216`,
> `decocms/terraform-foundation-infra#27`, `decocms/terraform-eks-cluster#83`.

## Mental model (read this first)

**Nothing changes about how content is edited or published.** A site still
publishes the same way (git commit to `.deco/blocks/**` → admin upserts the
`Decofile` CR). What changes is **delivery**:

| | `configmap` (default) | `s3` |
|---|---|---|
| Where the decofile lives | A ConfigMap key, mounted as a file | An S3 object, fetched over HTTP |
| How a pod gets it at boot | Reads the mounted file (`DECO_RELEASE=file://…`) | `fetch()`s the URL (`DECO_RELEASE=https://…`) |
| How a pod gets a **live update** | `POST /.decofile/reload` (operator pushes) | **identical** — same push, unchanged |
| Size ceiling | ~1MB (etcd) | none |

**The only thing that's different for a running pod is the very first read at
boot.** Every subsequent update — someone edits a block and publishes — reaches
already-running pods exactly the same way regardless of target: the operator
detects the change and pushes a reload notification to `/.decofile/reload` on
every matching pod (see `ARCHITECTURE.md` Flow 4). The pod doesn't care whether
the *next* update source is S3 or a ConfigMap; it only mattered for how the pod
found its *first* copy when it started.

**Nothing is manual per publish.** Once a site is onboarded (below), every
future publish flows through unchanged — the operator uploads the new content
to S3 automatically on each reconcile, exactly like it writes the ConfigMap
today.

## Prerequisites (one-time, platform-wide)

These need to exist before **any** site can use `target: s3`. Track via the PRs
above; do not repeat per site.

1. **Operator ≥ the version shipping `decocms/operator#33`**, deployed with
   `decofileS3.bucket`/`region`/`publicHost` set in the chart values
   (`infra_applications` → `provisioning/deco-operator/main/values.yaml`).
2. **S3 bucket** `new-deco-decofiles` exists (`terraform-foundation-infra#27`),
   with:
   - a policy statement granting the operator's controller **Pod Identity**
     role read/write (`terraform-eks-cluster#83`, module
     `deco_operator_decofiles_pod_identity`)
   - a policy statement granting **anonymous `GetObject`** scoped to the
     eks-serverless VPC's S3 Gateway Endpoint (`aws:sourceVpce` condition) —
     this is how pods read the object without any AWS credentials
3. Both terraform PRs applied with the **real VPC endpoint id** filled in
   (see the `TODO` in `s3-setup/local.tf` — a placeholder until applied).

Quick health check once deployed — create a Decofile with `target: s3` for a
throwaway site and confirm no ConfigMap is created:
```bash
kubectl -n sites-<site> get decofile <name> -o jsonpath='{.status.s3URL}{"\n"}'
kubectl -n sites-<site> get configmap decofile-<name>   # should 404
```

## Per-site steps

### 1. Confirm the site actually needs this
Check the merged decofile's compressed size before flipping the switch — this
target is for sites that need it, not a default:
```bash
# rough check: sum of .deco/blocks/*.json, brotli+base64'd, vs ~1MB
du -sh .deco/blocks/
```
If a site is nowhere near the limit, leave it on `configmap` — no reason to
opt in early.

### 2. Add the site to admin's opt-in list
Admin decides the target per site via the `DECOFILE_S3_SITES` env var
(comma-separated site slugs) on the `admin-env` Secret — see
`deco-sites/admin#3302` (`hosting/kubernetes/actions/decofile/upsert.ts`).
Add the site's slug there. No operator-side per-site config exists; the
`Decofile` CR's `spec.target` is set by admin on every publish.

### 3. Publish
Trigger a normal publish for the site (git push / admin publish action).
Admin's next `Decofile` upsert will carry `spec.target: "s3"`.

### 4. Verify
```bash
kubectl -n sites-<site> get decofile <deploymentId> -o yaml
# spec.target: s3
# status.s3URL: https://new-deco-decofiles.s3.us-west-2.amazonaws.com/decofiles/...
# status.contentHash: <sha256>

kubectl -n sites-<site> get configmap decofile-<deploymentId>   # 404 — no longer created

# a new/rolled pod should show:
kubectl -n sites-<site> get pod <pod> -o jsonpath='{.spec.containers[0].env}' | grep -A1 DECO_RELEASE
# DECO_RELEASE=https://new-deco-decofiles.s3.us-west-2.amazonaws.com/decofiles/...
```
The pod's startup log line (`decofile has been loaded from https://…`, from the
deco runtime's `getProvider()`) confirms it read from S3, not a mounted file.

### 5. Test the live-update path
Edit a block and publish again. The pod should reload **without a restart**
(same `/.decofile/reload` push as the ConfigMap path) — no extra step needed,
this isn't new behavior to configure.

## Constraints

- **Opt-in only, per site.** There is no automatic size-based promotion today —
  a site stays on `configmap` until added to `DECOFILE_S3_SITES`.
- **Reads are unauthenticated, scoped by network path.** Any request reaching
  the bucket via the eks-serverless VPC's S3 Gateway Endpoint can read any
  object in the bucket (the `aws:sourceVpce` condition doesn't scope by key
  prefix). Don't put anything in this bucket that shouldn't be readable by any
  workload inside that VPC.
- **S3 objects are not Kubernetes-garbage-collected.** Deleting a `Decofile`
  CR does not delete its S3 object (unlike the ConfigMap, which has an owner
  reference). Rely on the bucket's lifecycle rule, or clean up manually if a
  site is fully decommissioned.
- **No compression.** Objects are stored as plain JSON. A very large decofile
  (tens of MB) will take proportionally longer to fetch at cold start than a
  brotli'd ConfigMap would — there's no ceiling, but it isn't free either.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Pod stuck without a decofile / `decofile not defined` in logs | `DECOFILE_S3_PUBLIC_HOST` not set on the operator, or the host isn't reachable from the pod (VPC endpoint misconfigured) | check operator env `DECOFILE_S3_PUBLIC_HOST`; confirm the bucket policy's `aws:sourceVpce` matches the *actual* endpoint id (not the placeholder) |
| `AccessDenied` on the operator's `PutObject` | Pod Identity association missing/misconfigured, or bucket policy condition doesn't match the role | `kubectl -n deco-system describe pod <controller-manager-pod>` — check for an AWS credential env/mount; verify `terraform-eks-cluster#83` applied for `workspace=serverless` |
| Still see a ConfigMap after publish | `spec.target` didn't get set to `s3` | check admin's `DECOFILE_S3_SITES` includes the site slug; check the `Decofile` CR's `spec.target` directly |
| Live edits not reaching the pod | Same failure modes as the ConfigMap path — this isn't s3-specific | see `ARCHITECTURE.md` notification troubleshooting; check `PodsNotified` condition on the `Decofile` |
| `authority ... is not allowed to be fetched from` in pod logs | Webhook didn't merge the S3 host into `DECO_ALLOWED_AUTHORITIES` (e.g. Service predates the webhook version) | re-roll the Knative Revision so the webhook re-runs; check the pod's `DECO_ALLOWED_AUTHORITIES` env includes the S3 host |

## Disable / rollback

Remove the site's slug from `DECOFILE_S3_SITES` and publish again. The next
`Decofile` upsert sets `spec.target: "configmap"`; the operator creates the
ConfigMap and the webhook mounts it on the next pod roll. The stale S3 object
is harmless (unauthenticated read-only, no owner reference — see Constraints)
but isn't automatically cleaned up.

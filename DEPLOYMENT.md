# Deployment Guide

## Quick Deployment with Helm

### From GitHub Repository

```bash
# Clone the repository
git clone https://github.com/decocms/operator.git
cd operator

# Install with Helm
helm upgrade --install decofile-operator chart/ \
  --namespace operator-system \
  --create-namespace \
  --set image.tag=latest \
  --wait
```

### With GitHub Token (for private repos)

```bash
helm upgrade --install decofile-operator chart/ \
  --namespace operator-system \
  --create-namespace \
  --set image.tag=latest \
  --set github.token=ghp_your_token_here \
  --wait
```

### Custom Configuration

Create `values.yaml`:

```yaml
image:
  repository: ghcr.io/decocms/operator
  tag: v1.0.0

replicaCount: 2

github:
  token: "ghp_xxx"  # Optional

resources:
  limits:
    cpu: 1000m
    memory: 512Mi
  requests:
    cpu: 100m
    memory: 256Mi
```

Deploy:

```bash
helm upgrade --install decofile-operator chart/ \
  --namespace operator-system \
  --create-namespace \
  -f values.yaml \
  --wait
```

## CI/CD Flow

The GitHub Actions workflow (`build-and-push.yaml`) automatically:

1. ✅ Verifies Helm chart is in sync with Kustomize manifests
2. ✅ Builds multi-platform image (amd64 + arm64)
3. ✅ Pushes to `ghcr.io/decocms/operator`

### Trigger Builds

```bash
# Push to main (builds :latest)
git push origin main

# Tag a release (builds versioned tags)
git tag v1.0.0
git push origin v1.0.0
```

### Required Secrets

- `GITHUB_TOKEN` - Automatically provided by GitHub Actions
- `GH_TOKEN` - (Optional) Personal Access Token for operator to access private repos

## Manual Deployment Steps

### 1. Pull Latest Image

```bash
# Pull specific version
docker pull ghcr.io/decocms/operator:v1.0.0

# Or latest
docker pull ghcr.io/decocms/operator:latest
```

### 2. Deploy with Helm

```bash
# Clone repo to get Helm chart
git clone https://github.com/decocms/operator.git
cd operator

# Deploy
helm upgrade --install decofile-operator chart/ \
  --namespace operator-system \
  --create-namespace \
  --set image.tag=v1.0.0 \
  --set github.token="${GITHUB_TOKEN}" \
  --wait --timeout=5m
```

### 3. Verify Deployment

```bash
# Check pods
kubectl get pods -n operator-system

# Check CRDs
kubectl get crd decofiles.deco.sites

# Check operator logs
kubectl logs -n operator-system -l control-plane=controller-manager -f
```

## Upgrade

```bash
# Upgrade to new version
helm upgrade decofile-operator chart/ \
  --namespace operator-system \
  --set image.tag=v1.1.0 \
  --reuse-values
```

## Uninstall

```bash
# Remove operator
helm uninstall decofile-operator --namespace operator-system

# Optionally remove namespace
kubectl delete namespace operator-system
```

## Development Workflow

### Make Changes

```bash
# 1. Modify code
vim internal/controller/decofile_controller.go

# 2. Update tests
make test

# 3. Regenerate manifests and Helm
make manifests generate

# 4. Verify Helm sync
make helm-verify

# 5. Commit changes (include generated Helm templates!)
git add .
git commit -m "feat: your changes"

# 6. Push
git push origin main
```

### Local Testing (Kind)

```bash
# Setup Kind cluster
make kind-setup

# Deploy to Kind
make kind-deploy IMG=decofile-operator:dev

# Run e2e tests
make kind-test

# Or use Helm in Kind
make helm
helm upgrade --install decofile-operator chart/ \
  --namespace operator-system \
  --create-namespace \
  --set image.repository=decofile-operator \
  --set image.tag=dev
```

## Production Checklist

Before deploying to production:

- [ ] Run `make test` - All tests passing
- [ ] Run `make lint-fix` - No linter errors
- [ ] Run `make helm-verify` - Helm chart in sync
- [ ] Tag a release version
- [ ] Wait for CI to build and push image
- [ ] Deploy with specific version tag (not `latest`)
- [ ] Verify in staging first
- [ ] Monitor logs after deployment

## Troubleshooting

### Helm Chart Out of Sync

```bash
# Regenerate
make helm

# Commit
git add chart/
git commit -m "chore: regenerate helm chart"
```

### Image Pull Errors

```bash
# Verify image exists
docker pull ghcr.io/decocms/operator:your-tag

# Check image pull policy
helm upgrade decofile-operator chart/ \
  --set image.pullPolicy=Always \
  --reuse-values
```

### Webhook Certificate Issues

```bash
# Verify cert-manager is running
kubectl get pods -n cert-manager

# Check certificates
kubectl get certificates -n operator-system

# Restart operator to reload certs
kubectl rollout restart deployment -n operator-system
```

## Notes

- Helm chart is auto-generated from Kustomize (single source of truth)
- Always run `make helm` after modifying Kustomize files
- CI verifies Helm sync on every push
- Templates are committed to enable GitHub URL installation
- Use specific version tags in production, not `latest`



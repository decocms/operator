# Helm Chart Installation Guide

The Decofile Operator can be installed using Helm for easy configuration and upgrades.

## Prerequisites

- Kubernetes cluster (1.16+)
- Helm 3.x
- cert-manager installed (for webhooks)
- Knative Serving installed (if using injection features)

## Installation

### Option 1: Install from GitHub Release (Recommended)

```bash
# Install from a specific release
helm install deco \
  https://github.com/decocms/operator/releases/download/v0.1.0/deco-operator-0.1.0.tgz \
  --namespace operator-system \
  --create-namespace \
  --wait

# Always use the latest release
helm install deco \
  https://github.com/decocms/operator/releases/latest/download/deco-operator-0.1.0.tgz \
  --namespace operator-system \
  --create-namespace \
  --wait
```

### Option 2: Install from Source

```bash
# Clone and install
git clone https://github.com/decocms/operator.git
cd operator
helm install deco chart/ \
  --namespace operator-system \
  --create-namespace \
  --wait
```

### Option 2: Install from Local

```bash
# Generate Helm chart from Kustomize
make helm

# Install
helm install decofile-operator chart/ \
  --namespace operator-system \
  --create-namespace
```

## Configuration

### Basic Configuration

```bash
helm install decofile-operator chart/ \
  --namespace operator-system \
  --create-namespace \
  --set image.repository=ghcr.io/decocms/operator \
  --set image.tag=v1.0.0 \
  --set replicaCount=2
```

### With GitHub Token (for private repositories)

```bash
helm install decofile-operator chart/ \
  --namespace operator-system \
  --create-namespace \
  --set github.token=ghp_your_token_here
```

### Using values.yaml

Create a `custom-values.yaml`:

```yaml
image:
  repository: ghcr.io/decocms/operator
  tag: v1.0.0

replicaCount: 2

github:
  token: "ghp_your_github_token"

resources:
  limits:
    cpu: 1000m
    memory: 256Mi
  requests:
    cpu: 100m
    memory: 128Mi

certManager:
  enabled: true

webhook:
  enabled: true
```

Install with custom values:

```bash
helm install decofile-operator chart/ \
  --namespace operator-system \
  --create-namespace \
  -f custom-values.yaml
```

## Available Values

| Parameter | Description | Default |
|-----------|-------------|---------|
| `image.repository` | Operator image repository | `ghcr.io/decocms/operator` |
| `image.tag` | Operator image tag | `latest` |
| `image.pullPolicy` | Image pull policy | `IfNotPresent` |
| `replicaCount` | Number of replicas | `1` |
| `github.token` | GitHub token for private repos | `""` (optional) |
| `resources.limits.cpu` | CPU limit | `500m` |
| `resources.limits.memory` | Memory limit | `128Mi` |
| `resources.requests.cpu` | CPU request | `10m` |
| `resources.requests.memory` | Memory request | `64Mi` |
| `certManager.enabled` | Enable cert-manager integration | `true` |
| `webhook.enabled` | Enable admission webhooks | `true` |
| `leaderElection.enabled` | Enable leader election | `true` |
| `serviceAccount.create` | Create service account | `true` |
| `serviceAccount.name` | Service account name | Auto-generated |
| `nodeSelector` | Node selector | `{}` |
| `tolerations` | Tolerations | `[]` |
| `affinity` | Affinity rules | `{}` |

## Upgrade

```bash
# Upgrade to new version
helm upgrade decofile-operator chart/ \
  --namespace operator-system \
  --set image.tag=v1.1.0
```

## Uninstall

```bash
helm uninstall decofile-operator --namespace operator-system
```

## Usage Examples

### Example 1: Production Deployment

```bash
helm install decofile-operator chart/ \
  --namespace operator-system \
  --create-namespace \
  --set image.tag=v1.0.0 \
  --set replicaCount=3 \
  --set github.token="${GITHUB_TOKEN}" \
  --set resources.limits.cpu=1000m \
  --set resources.limits.memory=512Mi
```

### Example 2: Development with Custom Image

```bash
helm install decofile-operator chart/ \
  --namespace operator-system \
  --create-namespace \
  --set image.repository=myregistry/operator \
  --set image.tag=dev \
  --set image.pullPolicy=Always
```

### Example 3: Minimal Install (No Webhooks)

```bash
helm install decofile-operator chart/ \
  --namespace operator-system \
  --create-namespace \
  --set webhook.enabled=false \
  --set certManager.enabled=false
```

## Development

### Regenerate Helm Chart

After modifying Kustomize manifests, regenerate the Helm chart:

```bash
make helm
```

This will:
1. Generate Kustomize manifests
2. Convert to Helm templates
3. Apply templating and conditionals
4. Add GITHUB_TOKEN support

### Verify Chart is Up-to-Date

```bash
make helm-verify
```

This ensures you haven't forgotten to run `make helm` after changing manifests.

### Lint Chart

```bash
make helm-lint
```

### Template (Dry Run)

```bash
make helm-template
```

### Package Chart

```bash
make helm-package
```

This creates a `.tgz` file in `dist/` that can be shared or uploaded to a chart repository.

## GitHub URL Installation

Once templates are committed to the repository, users can install directly:

```bash
# From specific version/tag
helm install decofile-operator \
  oci://ghcr.io/decocms/charts/decofile-operator \
  --version v1.0.0 \
  --namespace operator-system \
  --create-namespace

# Or from raw GitHub URL
helm install decofile-operator \
  https://raw.githubusercontent.com/decocms/operator/main/chart \
  --namespace operator-system \
  --create-namespace
```

## Troubleshooting

### Chart Generation Fails

```bash
# Ensure kustomize is installed
make kustomize

# Regenerate manifests first
make manifests

# Then generate helm
make helm
```

### Deployment Name Too Long

The default Helm release name + chart name might exceed 63 characters. Use shorter names:

```bash
helm install deco chart/ --namespace operator-system
```

### Verify Installation

```bash
# Check all resources
helm list -n operator-system
kubectl get all -n operator-system
kubectl get crd decofiles.deco.sites
```

## CI/CD Integration

The Helm chart is automatically verified in CI to ensure it's kept in sync with Kustomize manifests.

See `.github/workflows/test.yaml` for the verification job that runs on every PR.

## Notes

- Helm chart templates are **auto-generated** from Kustomize manifests
- Always run `make helm` after modifying Kustomize files
- Templates are committed to git for GitHub URL installation
- CI will fail if Helm chart is out of sync



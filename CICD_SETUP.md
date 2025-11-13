# CI/CD Setup Guide

## Overview

Automated CI/CD pipeline using GitHub Actions for building, testing, and deploying the Decofile operator.

## Workflows

### 1. `test.yaml` - Continuous Integration

Runs on every PR and push to main:

- ✅ **Unit Tests** - Runs `make test`
- ✅ **Linting** - Runs golangci-lint
- ✅ **Build** - Compiles the binary
- ✅ **Coverage** - Uploads to Codecov

### 2. `build-and-deploy.yaml` - Continuous Deployment

Runs on push to main or version tags:

**Step 1: Build Multi-Platform Image**
- Platforms: `linux/amd64`, `linux/arm64`
- Registry: `ghcr.io/decocms/operator`
- Tags: `latest`, `main`, `v1.0.0`, etc.

**Step 2: Deploy to Kubernetes**
- Uses kubeconfig from secrets
- Deploys with `make deploy`
- Waits for rollout
- Verifies installation

## Setup Instructions

### 1. Enable GitHub Packages

Go to repository settings and ensure GitHub Packages is enabled.

### 2. Create Kubeconfig Secret

**Option A: Using Service Account (Recommended)**

```bash
# Create service account
kubectl create serviceaccount github-actions -n decofile-operator-system

# Grant cluster-admin (or create more restrictive role)
kubectl create clusterrolebinding github-actions-admin \
  --clusterrole=cluster-admin \
  --serviceaccount=decofile-operator-system:github-actions

# Get token (valid for 10 years)
TOKEN=$(kubectl create token github-actions \
  -n decofile-operator-system \
  --duration=87600h)

# Get cluster info
CLUSTER_SERVER=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')
CLUSTER_CA=$(kubectl config view --raw --minify -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')

# Create kubeconfig
cat > github-kubeconfig.yaml <<EOF
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: $CLUSTER_SERVER
    certificate-authority-data: $CLUSTER_CA
  name: github-cluster
contexts:
- context:
    cluster: github-cluster
    user: github-actions
  name: github-context
current-context: github-context
users:
- name: github-actions
  user:
    token: $TOKEN
EOF

# Base64 encode
cat github-kubeconfig.yaml | base64 | tr -d '\n' > kubeconfig-base64.txt

# Copy contents of kubeconfig-base64.txt
cat kubeconfig-base64.txt

# Clean up
rm github-kubeconfig.yaml kubeconfig-base64.txt
```

**Option B: Using Existing Kubeconfig**

```bash
# Encode your existing kubeconfig
cat ~/.kube/config | base64 | tr -d '\n'
```

**Add to GitHub:**

1. Go to: **Repository → Settings → Secrets and variables → Actions**
2. Click **New repository secret**
3. Name: `KUBE_CONFIG`
4. Value: Paste the base64-encoded kubeconfig
5. Click **Add secret**

### 3. Test the Setup

**Create a test commit:**

```bash
echo "# Test" >> README.md
git add README.md
git commit -m "Test CI/CD pipeline"
git push origin main
```

**Check workflow:**

1. Go to **Actions** tab
2. You should see two workflows running:
   - ✅ Test (runs tests and lint)
   - ✅ Build and Deploy (builds image and deploys)

## Image Registry

### GitHub Container Registry (ghcr.io)

**Image URL:** `ghcr.io/decocms/operator`

**Visibility:**

For public access:
1. Go to **Packages** (in repository or organization)
2. Find `operator` package
3. Click **Package settings**
4. Change visibility to **Public**

**Pull image:**

```bash
# Public images (no auth needed)
docker pull ghcr.io/decocms/operator:latest

# Private images
echo $GITHUB_TOKEN | docker login ghcr.io -u USERNAME --password-stdin
docker pull ghcr.io/decocms/operator:latest
```

## Deployment

### Automatic Deployment

Deployment happens automatically on:

✅ **Push to `main` branch**
```bash
git push origin main
```

✅ **Push version tag**
```bash
git tag v1.0.0
git push origin v1.0.0
```

### Manual Deployment

1. Go to **Actions** tab
2. Select **Build and Deploy** workflow
3. Click **Run workflow**
4. Select branch/tag
5. Click **Run workflow** button

### Deployment Process

1. **Build multi-platform image**
   - Build for amd64 and arm64
   - Tag with version/branch/SHA
   - Push to ghcr.io

2. **Deploy to Kubernetes**
   - Decode kubeconfig from secret
   - Run `make deploy IMG=ghcr.io/decocms/operator:tag`
   - Wait for rollout (5 minute timeout)
   - Verify pods are running

3. **Cleanup**
   - Remove temporary kubeconfig file

## Monitoring

### Workflow Status

Check status badge (add to README.md):

```markdown
[![Build and Deploy](https://github.com/decocms/operator/actions/workflows/build-and-deploy.yaml/badge.svg)](https://github.com/decocms/operator/actions/workflows/build-and-deploy.yaml)
[![Tests](https://github.com/decocms/operator/actions/workflows/test.yaml/badge.svg)](https://github.com/decocms/operator/actions/workflows/test.yaml)
```

### View Logs

After deployment, check operator logs:

```bash
kubectl logs -n decofile-operator-system \
  deployment/decofile-operator-controller-manager \
  -f
```

### Verify Deployment

```bash
# Check pods
kubectl get pods -n decofile-operator-system

# Check image version
kubectl get deployment decofile-operator-controller-manager \
  -n decofile-operator-system \
  -o jsonpath='{.spec.template.spec.containers[0].image}'
```

## Rollback

### Rollback to Previous Version

```bash
# Via kubectl
kubectl rollout undo deployment/decofile-operator-controller-manager \
  -n decofile-operator-system

# Or deploy specific version
make deploy IMG=ghcr.io/decocms/operator:v1.0.0
```

### Rollback via Git

```bash
# Revert to previous commit
git revert HEAD
git push origin main
# Workflow will deploy the reverted version
```

## Environment Variables

Available in workflows:

| Variable | Description | Example |
|----------|-------------|---------|
| `REGISTRY` | Container registry | `ghcr.io` |
| `IMAGE_NAME` | Image name | `decocms/operator` |
| `IMG` | Full image reference | `ghcr.io/decocms/operator:main` |
| `KUBECONFIG` | Path to kubeconfig | `/tmp/kubeconfig` |

## Security Best Practices

### 1. Limit Kubeconfig Permissions

Create a dedicated service account with minimum permissions:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: github-actions
  namespace: decofile-operator-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: github-actions-deployer
rules:
  - apiGroups: ["apps"]
    resources: [deployments]
    verbs: [get, list, update, patch]
  - apiGroups: [""]
    resources: [pods]
    verbs: [get, list]
  - apiGroups: [apiextensions.k8s.io]
    resources: [customresourcedefinitions]
    verbs: [get, list, create, update, patch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: github-actions-deployer
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: github-actions-deployer
subjects:
  - kind: ServiceAccount
    name: github-actions
    namespace: decofile-operator-system
```

### 2. Protect Secrets

- Never commit kubeconfig to Git
- Rotate tokens regularly
- Use different secrets for different environments
- Enable secret scanning in repository

### 3. Branch Protection

Enable on `main` branch:
- Require pull request reviews
- Require status checks to pass
- Require branches to be up to date

## Cost Optimization

### Cache Strategy

The workflow uses GitHub Actions cache for:
- Go modules
- Docker layer cache
- Build artifacts

### Skip Deployment

To skip deployment, add `[skip deploy]` to commit message:

```bash
git commit -m "Update docs [skip deploy]"
```

Then update workflow:

```yaml
deploy:
  if: |
    (github.ref == 'refs/heads/main' || startsWith(github.ref, 'refs/tags/v')) &&
    !contains(github.event.head_commit.message, '[skip deploy]')
```

## Next Steps

1. ✅ Workflows created
2. ⚠️  Add `KUBE_CONFIG` secret to GitHub
3. ⚠️  Make repository public or configure package access
4. ⚠️  Push to main to trigger first workflow
5. ⚠️  Verify deployment in cluster

## Checklist

Before using CI/CD:

- [ ] GitHub Packages enabled
- [ ] `KUBE_CONFIG` secret added
- [ ] Service account created in cluster
- [ ] Cluster is accessible from GitHub Actions
- [ ] cert-manager installed in cluster
- [ ] Knative Serving installed (if using webhooks)

---

**All workflows ready!** 🚀 Push to main to trigger your first automated deployment.


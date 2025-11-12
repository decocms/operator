# GitHub Actions Workflows

## Overview

This directory contains GitHub Actions workflows for CI/CD of the Decofile operator.

## Workflows

### 1. `test.yaml` - Continuous Integration

**Triggers:**
- Pull requests to `main`
- Pushes to `main`

**Jobs:**
- **test**: Runs unit tests with coverage
- **lint**: Runs golangci-lint
- **build**: Builds the binary

**Purpose:** Ensure code quality on every PR and commit

### 2. `build-and-deploy.yaml` - Build and Deploy

**Triggers:**
- Push to `main` branch
- Push of version tags (e.g., `v1.0.0`)
- Manual workflow dispatch

**Jobs:**

#### Job 1: Build and Push
- Builds multi-platform Docker image (linux/amd64, linux/arm64)
- Pushes to GitHub Container Registry (ghcr.io/decocms/operator)
- Uses Docker buildx for multi-arch support
- Caches layers for faster builds

#### Job 2: Deploy
- Deploys to Kubernetes cluster
- Uses kubeconfig from GitHub Secrets
- Waits for deployment rollout
- Verifies installation

## Secrets Required

Add these secrets to your GitHub repository settings:

### `KUBE_CONFIG`

Base64-encoded kubeconfig file for your Kubernetes cluster.

**How to create:**

```bash
# Encode your kubeconfig
cat ~/.kube/config | base64 | tr -d '\n' > kubeconfig-base64.txt

# Copy the contents and add to GitHub Secrets
# Go to: Repository Settings → Secrets and variables → Actions → New repository secret
# Name: KUBE_CONFIG
# Value: <paste the base64 encoded kubeconfig>
```

**Alternative with specific context:**

```bash
# If you have multiple contexts, export just one
kubectl config view --context=my-cluster --minify --flatten | base64 | tr -d '\n'
```

### `GITHUB_TOKEN`

This is automatically provided by GitHub Actions. No setup needed.

## Image Tags

The workflow creates these image tags:

| Trigger | Tags Created |
|---------|-------------|
| Push to `main` | `main`, `latest`, `main-<sha>` |
| Push tag `v1.2.3` | `v1.2.3`, `v1.2`, `v1`, `latest` |
| Pull request | `pr-<number>` |

**Examples:**
- `ghcr.io/decocms/operator:latest`
- `ghcr.io/decocms/operator:main`
- `ghcr.io/decocms/operator:v1.0.0`
- `ghcr.io/decocms/operator:main-a1b2c3d`

## Usage

### Automatic Deployment

Push to main:
```bash
git add .
git commit -m "Update operator"
git push origin main
```

This will:
1. Run tests
2. Build multi-platform image
3. Push to ghcr.io
4. Deploy to cluster

### Tagged Release

Create and push a tag:
```bash
git tag v1.0.0
git push origin v1.0.0
```

This will:
1. Run tests
2. Build multi-platform image with version tags
3. Push to ghcr.io
4. Deploy to cluster

### Manual Deployment

Go to Actions tab → Build and Deploy → Run workflow

## Platform Support

The image is built for:
- **linux/amd64** - Intel/AMD 64-bit (most cloud providers)
- **linux/arm64** - ARM 64-bit (Apple Silicon, AWS Graviton)

## Deployment Flow

```
┌─────────────┐
│  Git Push   │
│   to main   │
└──────┬──────┘
       │
       ▼
┌─────────────────┐
│   Run Tests     │
│   - Unit tests  │
│   - Lint        │
└──────┬──────────┘
       │
       ▼
┌─────────────────────┐
│   Build Image       │
│   - Multi-platform  │
│   - amd64 + arm64   │
└──────┬──────────────┘
       │
       ▼
┌─────────────────────┐
│   Push to ghcr.io   │
│   - Tag with SHA    │
│   - Tag with latest │
└──────┬──────────────┘
       │
       ▼
┌─────────────────────┐
│   Deploy to K8s     │
│   - Update image    │
│   - Apply manifests │
│   - Wait for ready  │
└─────────────────────┘
```

## Customization

### Change Image Registry

Edit `.github/workflows/build-and-deploy.yaml`:

```yaml
env:
  REGISTRY: your-registry.io  # Change from ghcr.io
  IMAGE_NAME: your-org/operator  # Change from decocms/operator
```

### Add More Platforms

Edit the build step:

```yaml
- name: Build and push Docker image
  uses: docker/build-push-action@v6
  with:
    platforms: linux/amd64,linux/arm64,linux/s390x,linux/ppc64le
```

### Deploy to Multiple Clusters

Add more deployment jobs:

```yaml
deploy-prod:
  needs: build-and-push
  runs-on: ubuntu-latest
  steps:
    - name: Set up Kubeconfig (Production)
      run: |
        echo "${{ secrets.KUBE_CONFIG_PROD }}" | base64 -d > /tmp/kubeconfig
    # ... deploy steps

deploy-staging:
  needs: build-and-push
  runs-on: ubuntu-latest
  steps:
    - name: Set up Kubeconfig (Staging)
      run: |
        echo "${{ secrets.KUBE_CONFIG_STAGING }}" | base64 -d > /tmp/kubeconfig
    # ... deploy steps
```

### Skip Deployment on PR

Deployment only runs on:
- Push to `main`
- Version tags (`v*`)

PRs will only run tests and build the image.

## Monitoring

### Check Workflow Status

Go to: Repository → Actions tab

### View Logs

Click on any workflow run → Select job → View logs

### Failed Deployment

If deployment fails:

1. **Check kubeconfig:**
   ```bash
   echo "$KUBE_CONFIG" | base64 -d | kubectl --kubeconfig=- cluster-info
   ```

2. **Verify secret:**
   - Go to Settings → Secrets and variables → Actions
   - Ensure `KUBE_CONFIG` exists

3. **Test locally:**
   ```bash
   # Decode secret
   echo "$KUBE_CONFIG" | base64 -d > test-kubeconfig
   
   # Test connection
   kubectl --kubeconfig=test-kubeconfig get pods
   
   # Clean up
   rm test-kubeconfig
   ```

## Security

### Kubeconfig Protection

- ✅ Stored encrypted in GitHub Secrets
- ✅ Base64 encoded for safe transport
- ✅ Temporary file deleted after use
- ✅ Not exposed in logs

### Image Registry

- ✅ Uses GitHub Container Registry (free for public repos)
- ✅ Automatic token authentication
- ✅ Multi-arch support

### Best Practices

1. **Use service account** in kubeconfig (not user credentials)
2. **Limit permissions** (only what operator needs)
3. **Rotate kubeconfig** regularly
4. **Use different secrets** for staging/production
5. **Enable branch protection** on main

## Troubleshooting

### Build Fails

**Error:** `failed to solve: process "/bin/sh -c go build..."`

**Solution:** Check Dockerfile and go.mod are correct

### Push Fails

**Error:** `denied: permission_denied`

**Solution:** 
1. Enable GitHub Packages in repository settings
2. Make repository public or configure package visibility
3. Verify GITHUB_TOKEN permissions

### Deploy Fails

**Error:** `error: You must be logged in to the server (Unauthorized)`

**Solution:**
1. Verify kubeconfig is valid
2. Check it's properly base64 encoded
3. Ensure kubeconfig has required permissions

### Platform Build Fails

**Error:** `no match for platform in manifest`

**Solution:**
- Ensure QEMU is set up correctly
- Check Dockerfile has multi-platform support
- Verify base images support target platforms

## Example: Create Kubeconfig Secret

```bash
# 1. Create a service account for GitHub Actions
kubectl create serviceaccount github-actions -n decofile-operator-system

# 2. Create ClusterRoleBinding
kubectl create clusterrolebinding github-actions-admin \
  --clusterrole=cluster-admin \
  --serviceaccount=decofile-operator-system:github-actions

# 3. Get the token
TOKEN=$(kubectl create token github-actions -n decofile-operator-system --duration=87600h)

# 4. Create kubeconfig
cat > github-kubeconfig.yaml <<EOF
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://your-cluster-api-server:6443
    certificate-authority-data: $(kubectl config view --raw -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')
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

# 5. Base64 encode
cat github-kubeconfig.yaml | base64 | tr -d '\n' > github-kubeconfig-base64.txt

# 6. Add to GitHub Secrets as KUBE_CONFIG
# Copy contents of github-kubeconfig-base64.txt

# 7. Clean up
rm github-kubeconfig.yaml github-kubeconfig-base64.txt
```

## Manual Workflow Trigger

You can manually trigger the workflow from GitHub:

1. Go to Actions tab
2. Select "Build and Deploy"
3. Click "Run workflow"
4. Select branch
5. Click "Run workflow"

---

**Ready to use!** Push to main or create a tag to trigger the workflow.


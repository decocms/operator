# Deco CMS Operator

A Kubernetes operator for Deco CMS that manages configuration resources and automatically injects them into Knative Services.

## Description

The Deco CMS operator manages **Decofile** custom resources, which represent configuration files for your Deco applications. The operator enables you to:
- Define JSON configuration data as Kubernetes custom resources (Decofiles)
- Automatically create ConfigMaps from Decofile resources
- Inject ConfigMaps into Knative Services via annotations using a mutating webhook

## Features

### Decofile Management
- ✅ **Dual Source Support**: Inline JSON or GitHub repository sources
- ✅ **Automated ConfigMap Generation**: Creates/updates ConfigMaps from Decofile resources
- ✅ **Unified Format**: All sources produce consistent `decofile.json` format
- ✅ **Special Filename Support**: Preserves filenames with `%`, spaces, and special characters
- ✅ **Owner References**: Automatic cleanup when Decofiles are deleted

### Knative Integration
- ✅ **Webhook-based Injection**: Automatically injects ConfigMaps into Knative Services
- ✅ **File Mounting**: Mounts as `/app/decofile/decofile.json` by default
- ✅ **DECO_RELEASE Env Var**: Auto-injected for application discovery
- ✅ **Custom Mount Paths**: Configurable via annotations
- ✅ **Label-Based Tracking**: Pods labeled for easy discovery

### Change Notifications
- ✅ **Automatic Reload**: Notifies pods when ConfigMaps change
- ✅ **Smart Delay**: 60-second wait for kubelet sync
- ✅ **Retry Logic**: Exponential backoff with 5 attempts
- ✅ **Direct Pod Communication**: HTTP calls to reload endpoints

### Production Ready
- ✅ **Multi-Instance Ready**: Built-in leader election for high availability
- ✅ **Helm Support**: Install with Helm for easy configuration
- ✅ **CI/CD Pipeline**: Automated builds and validation
- ✅ **Complete Testing**: Unit, integration, and e2e tests

## Quick Start

### Installation with Helm

**From GitHub Release (Recommended)**:

```bash
# Install from release
helm install deco \
  https://github.com/decocms/operator/releases/download/v0.1.0/deco-operator-0.1.0.tgz \
  --namespace operator-system \
  --create-namespace \
  --wait

# With GitHub token for private repos
helm install deco \
  https://github.com/decocms/operator/releases/download/v0.1.0/deco-operator-0.1.0.tgz \
  --namespace operator-system \
  --create-namespace \
  --set github.token=ghp_your_token \
  --wait
```

**From Source**:

```bash
# Clone the repository
git clone https://github.com/decocms/operator.git
cd operator

# Install with Helm
helm install deco chart/ \
  --namespace operator-system \
  --create-namespace \
  --wait
```

### Verify Installation

```bash
# Check operator is running
kubectl get pods -n operator-system

# Check CRD is installed
kubectl get crd decofiles.deco.sites

# View operator logs
kubectl logs -n operator-system -l control-plane=controller-manager -f
```

### Prerequisites

- Kubernetes cluster (1.16+)
- Helm 3.x (for installation)
- cert-manager (for webhook TLS)
- Knative Serving (for injection features)

See [DEPLOYMENT.md](DEPLOYMENT.md) for detailed deployment options and [HELM.md](HELM.md) for complete Helm documentation.

## Usage

### Creating a Decofile

The Decofile operator supports two source types for configuration data:

#### 1. Inline Source

Define JSON configuration directly in the Decofile spec:

```yaml
apiVersion: deco.sites/v1alpha1
kind: Decofile
metadata:
  name: decofile-miess-01-main
  namespace: sites-miess-01
spec:
  source: inline
  inline:
    value:
      config.json:
        key: "value"
        environment: "production"
      data.json:
        foo: "bar"
```

#### 2. GitHub Source

Fetch configuration from a GitHub repository:

```yaml
apiVersion: deco.sites/v1alpha1
kind: Decofile
metadata:
  name: decofile-mysite-main
  namespace: sites-mysite
spec:
  source: github
  github:
    org: deco-sites
    repo: mysite
    commit: main  # or specific commit SHA
    path: .deco/blocks
    secret: github-token
```

**GitHub Secret Setup:**

Create a secret with your GitHub personal access token:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: github-token
  namespace: sites-mysite
type: Opaque
stringData:
  token: ghp_your_github_personal_access_token_here
```

Apply it:

```bash
# For inline source
kubectl apply -f config/samples/deco.sites_v1alpha1_decofile.yaml

# For GitHub source
kubectl apply -f config/samples/github_secret.yaml
kubectl apply -f config/samples/deco.sites_v1alpha1_decofile_github.yaml
```

The operator will automatically create a ConfigMap named `decofile-<name>` with the data.

### Injecting into Knative Services

Add annotations to your Knative Service to automatically inject the Decofile:

```yaml
apiVersion: serving.knative.dev/v1
kind: Service
metadata:
  name: my-site
  namespace: sites-mysite  # Namespace must start with "sites-" for "default" resolution
  annotations:
    deco.sites/decofile-inject: "default"  # Resolves to "decofile-mysite-main"
    # deco.sites/decofile-mount-path: "/custom/path"  # Optional: custom mount directory
spec:
  template:
    spec:
      containers:
        - name: app
          image: your-deco-app:latest
```

**What the webhook does automatically:**
1. ✅ Resolves the Decofile name (e.g., `"default"` → `decofile-mysite-main`)
2. ✅ Fetches the ConfigMap from the Decofile status
3. ✅ Mounts ConfigMap as `/app/decofile/decofile.json` (or custom path)
4. ✅ Injects `DECO_RELEASE=file:///app/decofile/decofile.json` environment variable
5. ✅ Labels pod with `deco.sites/decofile: <name>` for tracking

**Your application receives:**
- `DECO_RELEASE` env var pointing to the config file
- Single JSON file with all configuration: `{"file1.json": {...}, "file2.json": {...}}`
- Auto-updates when Decofile changes (with 60s kubelet sync delay)

## Annotations

### `deco.sites/decofile-inject`

Specifies which Decofile to inject into the Service.

- **Value: `"default"`** - Resolves to `decofile-{site}-main` where `{site}` is extracted from the namespace by stripping the `sites-` prefix
  - Example: namespace `sites-miess-01` → site `miess-01` → decofile `decofile-miess-01-main`
- **Value: `"<name>"`** - Uses the specified Decofile name directly

**Required:** Namespace must start with `sites-` when using `"default"`

### `deco.sites/decofile-mount-path`

Optional annotation to customize the mount path for the ConfigMap.

- **Default:** `/app/deco/.deco/blocks`
- **Example:** `/custom/config/path`

## Source Types

### Inline Source

Best for:
- Small configuration files
- Quick testing and development
- Static configurations

**Pros:**
- Simple and straightforward
- No external dependencies
- Immediate updates

**Cons:**
- Configuration stored in Kubernetes
- Harder to version control
- Manual updates required

### GitHub Source

Best for:
- GitOps workflows
- Versioned configurations
- Shared configurations across teams
- Large configuration files

**Pros:**
- Version control via Git
- Audit trail in Git history
- Easy rollbacks (change commit SHA)
- Centralized configuration management
- Supports private repositories

**Cons:**
- Requires GitHub access token
- Network dependency
- Slightly more complex setup

**How it works:**

1. Controller fetches GitHub credentials from Kubernetes secret
2. Downloads repository ZIP from `https://codeload.github.com/{org}/{repo}/zip/{commit}`
3. Extracts files from specified path
4. Creates ConfigMap with file contents

**Security:**
- Tokens stored in Kubernetes secrets
- Use read-only tokens (minimum required permissions)
- Supports private repositories

## Architecture

The Deco CMS Operator consists of three main components:

### 1. Decofile Controller

Manages the lifecycle of Decofile custom resources:

**Responsibilities:**
- Watches Decofile resources for changes
- Retrieves configuration from inline or GitHub sources  
- Creates/updates ConfigMaps with unified `decofile.json` format
- Detects ConfigMap changes and notifies affected pods
- Updates status with conditions and metadata

**Source Implementations:**
- `InlineSource` - Parses inline JSON values
- `GitHubSource` - Downloads from GitHub repositories
- Strategy pattern for easy extensibility

**Change Notifications:**
- Discovers pods via `deco.sites/decofile` label
- Calls `/.decofile/reload?delay=60000` on each pod
- Waits for pods to confirm reload before completing reconciliation

### 2. Mutating Webhook

Intercepts Knative Service CREATE/UPDATE operations:

**Injection Process:**
1. Checks for `deco.sites/decofile-inject` annotation
2. Resolves Decofile name ("default" or explicit)
3. Fetches ConfigMap name from Decofile status
4. Mounts ConfigMap as `/app/decofile/` directory
5. Injects `DECO_RELEASE` environment variable
6. Labels pods with `deco.sites/decofile` for tracking

**Features:**
- Supports custom mount paths via annotation
- Handles "default" resolution from namespace
- TLS secured via cert-manager

### 3. Pod Notifier

Notifies pods about configuration changes:

**Flow:**
- Triggered when ConfigMap data changes
- Queries pods by Decofile label
- HTTP GET with `?delay=60000` parameter
- Pods wait for kubelet sync before reading file
- Retries with exponential backoff

### High Availability

- ✅ **Leader Election**: Only one controller instance reconciles
- ✅ **Stateless Webhooks**: All instances handle requests
- ✅ **Horizontal Scaling**: Configure via `replicaCount` in Helm
- ✅ **Automatic Failover**: Built into controller-runtime

## Development

### Prerequisites

- Go 1.21+
- Docker
- kubectl
- operator-sdk

### Running Locally

1. Install the CRDs:

```bash
make install
```

2. Run the controller:

```bash
make run
```

### Testing

```bash
# Run unit tests
make test

# Run with coverage
make test-coverage
```

### Building

```bash
# Build binary
make build

# Build Docker image
make docker-build IMG=<your-registry>/decofile-operator:tag

# Push Docker image
make docker-push IMG=<your-registry>/decofile-operator:tag
```

## Configuration

### RBAC Permissions

The operator requires:
- Full access to Decofiles (our CRD)
- Full access to ConfigMaps
- Read access to Knative Services (for webhook)

### Webhook Configuration

The webhook is automatically configured with:
- TLS certificates via cert-manager
- Mutating webhook for Knative Services
- Validating webhook (optional, currently disabled)

## Troubleshooting

### Webhook Not Working

1. Check cert-manager is installed and running
2. Verify the webhook configuration:

```bash
kubectl get mutatingwebhookconfiguration
kubectl get certificate -n decofile-operator-system
```

3. Check webhook logs:

```bash
kubectl logs -n decofile-operator-system deployment/decofile-operator-controller-manager
```

### ConfigMap Not Created

1. Check the Decofile resource:

```bash
kubectl get decofile -n <namespace> <name> -o yaml
```

2. Check controller logs:

```bash
kubectl logs -n decofile-operator-system deployment/decofile-operator-controller-manager
```

### Decofile Not Creating ConfigMap

**For inline source:**
- Check the YAML syntax is valid
- Ensure `source: inline` is set
- Verify `inline.value` contains valid JSON

**For GitHub source:**
- Verify the secret exists and contains valid token
- Check GitHub credentials: `kubectl get secret github-token -n <namespace> -o yaml`
- Ensure org, repo, commit, and path are correct
- Check if repository is private (requires valid token)
- Verify network connectivity from cluster to GitHub

### Webhook Denying Service Creation

Common errors:
- **"Decofile X not found"**: Ensure the Decofile exists in the same namespace
- **"namespace does not start with 'sites-'"**: When using "default", the namespace must start with `sites-` prefix
- **"does not have a ConfigMap created yet"**: Wait for controller to reconcile the Decofile
- **"failed to get Decofile"**: Check the Decofile name and namespace

### GitHub Download Failures

Common errors:
- **"failed to download: status 404"**: Repository not found or token lacks access
- **"failed to get secret"**: Secret doesn't exist in the namespace
- **"secret does not contain 'token' key"**: Secret must have a `token` field
- **"failed to read zip"**: Invalid ZIP file or network issue

**Debugging:**
```bash
# Check controller logs
kubectl logs -n decofile-operator-system deployment/decofile-operator-controller-manager

# Check Decofile status
kubectl get decofile <name> -n <namespace> -o yaml

# Verify secret
kubectl get secret github-token -n <namespace> -o jsonpath='{.data.token}' | base64 -d

# Test GitHub access manually
curl -H "Authorization: token YOUR_TOKEN" https://codeload.github.com/org/repo/zip/main
```

## Uninstall

To uninstall the operator:

```bash
make undeploy
```

This will remove:
- The operator deployment
- Webhook configurations
- RBAC resources
- CRDs (and all Decofile resources)

## Contributing

Contributions are welcome! Please:
1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests
5. Submit a pull request

## License

Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

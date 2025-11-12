# Decofile Operator

A Kubernetes operator that manages Decofile custom resources and automatically injects ConfigMaps into Knative Services.

## Description

The Decofile operator enables you to:
- Define JSON configuration data as Kubernetes custom resources (Decofiles)
- Automatically create ConfigMaps from Decofile resources
- Inject ConfigMaps into Knative Services via annotations using a mutating webhook

## Features

- ✅ **Automated ConfigMap Management**: Automatically creates and updates ConfigMaps from Decofile resources
- ✅ **Webhook-based Injection**: Mutating webhook automatically injects ConfigMaps into Knative Services
- ✅ **Flexible Resolution**: Support for "default" annotation to resolve decofile names from labels
- ✅ **Custom Mount Paths**: Configure mount paths via annotations
- ✅ **Owner References**: Automatic cleanup when Decofiles are deleted
- ✅ **Status Tracking**: Comprehensive status reporting with conditions
- ✅ **Multi-Instance Ready**: Built-in leader election for high availability

## Getting Started

### Prerequisites

- Kubernetes cluster (1.16+)
- kubectl installed and configured
- cert-manager installed (for webhook TLS certificates)
- Knative Serving (if using webhook injection)
- Go 1.21+ (for development)

### Installation

1. Install cert-manager (if not already installed):

```bash
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.13.0/cert-manager.yaml
```

2. Build and push your operator image:

```bash
make docker-build docker-push IMG=<your-registry>/decofile-operator:tag
```

3. Deploy the operator:

```bash
make deploy IMG=<your-registry>/decofile-operator:tag
```

4. Verify installation:

```bash
kubectl get pods -n decofile-operator-system
kubectl get crd decofiles.deco.sites.deco.sites
```

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

Add annotations to your Knative Service to inject the Decofile:

```yaml
apiVersion: serving.knative.dev/v1
kind: Service
metadata:
  name: miess-01-site
  namespace: sites-miess-01  # Site name extracted from namespace (strips "sites-" prefix)
  annotations:
    deco.sites/decofile-inject: "default"  # or specify decofile name
    # deco.sites/decofile-mount-path: "/custom/path"  # optional
spec:
  template:
    spec:
      containers:
        - name: app
          image: your-image:tag
```

The webhook will automatically:
1. Resolve the Decofile name (if "default", extracts site from namespace and uses `decofile-{site}-main`)
2. Get the ConfigMap name from the Decofile status
3. Inject a projected volume with the ConfigMap
4. Add a volumeMount to the container at `/app/deco/.deco/blocks` (or custom path)

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

### Controller

The Decofile controller watches Decofile resources and:
1. Checks the source type (inline or github)
2. For inline: extracts JSON from spec
3. For GitHub: downloads ZIP, extracts specified path
4. Creates/updates a ConfigMap named `decofile-{decofile-name}`
5. Sets owner references for automatic cleanup
6. Updates the Decofile status with ConfigMap name, source type, and conditions

### Webhook

The mutating webhook intercepts CREATE/UPDATE operations on Knative Services and:
1. Checks for the `deco.sites/decofile-inject` annotation
2. Resolves the Decofile name (handles "default" resolution)
3. Queries the Decofile to get the ConfigMap name
4. Injects a projected volume mounting the ConfigMap
5. Adds a volumeMount to the container

### Multi-Instance Support

The operator supports running multiple replicas for high availability:
- Leader election ensures only one active reconciler
- Webhooks are stateless (all instances handle requests)
- Configure replicas in the deployment manifest

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

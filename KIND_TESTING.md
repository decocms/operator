# Testing Decofile Operator in Kind

This guide explains how to test the operator locally in a Kind cluster without publishing to any registry.

## Prerequisites

- **Kind cluster running**: `kind create cluster` (if not already done)
- **kubectl** configured to point to Kind cluster
- **Docker** running locally

## Quick Start

### 1. Setup Kind Environment

Install cert-manager and Knative Serving in your Kind cluster:

```bash
make kind-setup
```

This will:
- ✅ Install cert-manager v1.14.2
- ✅ Install Knative Serving v1.12.0
- ✅ Configure Kourier networking for Kind
- ✅ Setup DNS with sslip.io

### 2. Build, Load & Deploy Operator

Build the operator image locally and deploy to Kind:

```bash
# Option A: All-in-one (build + load + deploy)
make kind-deploy IMG=decofile-operator:dev

# Option B: Step by step
make docker-build IMG=decofile-operator:dev
make kind-load IMG=decofile-operator:dev
make deploy IMG=decofile-operator:dev
```

### 3. Run Basic Tests

Test the operator with sample resources:

```bash
make kind-test
```

Or manually:

```bash
# Create test namespace
kubectl create namespace sites-test

# Test inline Decofile
kubectl apply -f config/samples/deco.sites_v1alpha1_decofile.yaml -n sites-test

# Check Decofile status
kubectl get decofiles -n sites-test
kubectl describe decofile decofile-miess-01-main -n sites-test

# Check ConfigMap was created
kubectl get configmaps -n sites-test
kubectl get configmap decofile-decofile-miess-01-main -n sites-test -o yaml
```

### 4. View Operator Logs

```bash
make kind-logs
```

Or:

```bash
kubectl logs -n operator-system -l control-plane=controller-manager -f
```

## Testing Features

### Test 1: Inline Decofile

```bash
kubectl apply -f - <<EOF
apiVersion: deco.sites/v1alpha1
kind: Decofile
metadata:
  name: test-inline
  namespace: sites-test
spec:
  source: inline
  inline:
    value:
      config.json:
        environment: "test"
        apiUrl: "http://test-api"
EOF

# Verify ConfigMap created
kubectl get configmap decofile-test-inline -n sites-test -o yaml
```

### Test 2: GitHub Decofile (with GITHUB_TOKEN env var)

First, set the `GITHUB_TOKEN` environment variable in the operator deployment:

```bash
kubectl set env deployment/operator-controller-manager \
  GITHUB_TOKEN=ghp_your_token_here \
  -n operator-system
```

Then create a GitHub Decofile:

```bash
kubectl apply -f config/samples/deco.sites_v1alpha1_decofile_github_env.yaml -n sites-test

# Check status
kubectl describe decofile decofile-mysite-env -n sites-test
```

### Test 3: Knative Service Injection

Create a Knative Service that uses a Decofile:

```bash
kubectl apply -f - <<EOF
apiVersion: serving.knative.dev/v1
kind: Service
metadata:
  name: test-service
  namespace: sites-test
  annotations:
    deco.sites/decofile-inject: "test-inline"
spec:
  template:
    spec:
      containers:
        - name: app
          image: nginx:latest
          ports:
            - containerPort: 80
EOF

# Verify injection
kubectl get ksvc test-service -n sites-test -o yaml | grep -A 20 volumes
```

### Test 4: ConfigMap Change Notification

This requires a pod that responds to the reload endpoint:

```bash
# Update the Decofile
kubectl patch decofile test-inline -n sites-test \
  --type merge \
  -p '{"spec":{"inline":{"value":{"config.json":{"environment":"production","apiUrl":"http://prod-api"}}}}}'

# Watch operator logs for notification attempts
make kind-logs
```

Expected log output:
```
ConfigMap data changed, notifying pods
Notifying services: [test-service]
Successfully notified pod test-service-xxx
```

## Available Make Targets

```bash
make kind-setup      # Setup cert-manager & Knative
make kind-load       # Build & load image into Kind
make kind-deploy     # Build, load & deploy operator
make kind-test       # Run basic integration tests
make kind-logs       # Stream operator logs
make kind-clean      # Remove operator from cluster
```

## Troubleshooting

### Operator not starting

```bash
# Check deployment
kubectl get deploy -n operator-system

# Check pods
kubectl get pods -n operator-system

# Check events
kubectl get events -n operator-system --sort-by='.lastTimestamp'
```

### Webhooks not working

```bash
# Verify cert-manager is running
kubectl get pods -n cert-manager

# Check certificates
kubectl get certificates -n operator-system

# Check webhook configuration
kubectl get mutatingwebhookconfigurations
kubectl get validatingwebhookconfigurations
```

### Image not found in Kind

```bash
# List images in Kind
docker exec -it kind-control-plane crictl images | grep decofile

# Reload image if needed
make kind-load IMG=decofile-operator:dev
```

## Cleanup

Remove the operator:

```bash
make kind-clean
```

Or delete the entire cluster:

```bash
kind delete cluster
```

## Development Workflow

Recommended workflow for rapid iteration:

```bash
# 1. Make code changes
vim internal/controller/decofile_controller.go

# 2. Rebuild and reload
make kind-deploy IMG=decofile-operator:dev

# 3. Test changes
kubectl apply -f config/samples/deco.sites_v1alpha1_decofile.yaml -n sites-test

# 4. Watch logs
make kind-logs

# 5. Iterate!
```

## Notes

- **No registry needed**: Images are built locally and loaded directly into Kind
- **Fast iteration**: Rebuild and reload takes ~30 seconds
- **Isolated testing**: Kind cluster is separate from production
- **Full feature testing**: All features work exactly as in production

## Next Steps

Once testing is complete and you're ready for production:

1. Tag a release: `git tag v0.1.0`
2. Push to GitHub: `git push origin v0.1.0`
3. Build and push to registry:
   ```bash
   make docker-build docker-push IMG=ghcr.io/decocms/operator:v0.1.0
   ```
4. Deploy to production cluster:
   ```bash
   make deploy IMG=ghcr.io/decocms/operator:v0.1.0
   ```


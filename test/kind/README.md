# Kind E2E Testing

This directory contains end-to-end tests for the Decofile Operator running in Kind.

## Structure

```
test/kind/
├── app/
│   ├── main.ts       - Deno test application with reload endpoint
│   └── Dockerfile    - Container image for test app
├── manifests/
│   ├── decofile-inline.yaml   - Inline Decofile test case
│   ├── decofile-github.yaml   - GitHub Decofile test case
│   └── test-service.yaml      - Knative Service with injection
└── README.md         - This file
```

## Test Application

The test app is a simple Deno HTTP server that:
- Responds to `/health` for health checks
- Responds to `/.decofile/reload` to read and log ConfigMap files
- Logs all file contents from `/app/deco/.deco/blocks`

## Running Tests

### Quick Start

```bash
# Run all tests (builds app, deploys operator, runs tests)
make kind-test
```

### Manual Steps

```bash
# 1. Build test application
make kind-build-test-app

# 2. Load test app into Kind
make kind-load-test-app

# 3. Ensure operator is deployed
make kind-deploy IMG=decofile-operator:dev

# 4. Run E2E tests
bash hack/test-kind-e2e.sh
```

## What Gets Tested

1. ✅ **Inline Decofile** - Creates ConfigMap from inline JSON
2. ✅ **ConfigMap Creation** - Verifies ConfigMap with correct content
3. ✅ **Webhook Injection** - Injects ConfigMap into Knative Service
4. ✅ **Volume Mounting** - ConfigMap mounted at correct path
5. ✅ **Reload Endpoint** - Pod reads and logs ConfigMap files
6. ✅ **Change Notification** - Updates trigger pod notifications
7. ✅ **Content Verification** - Pod receives and processes new content

## Test Output

Successful test run:
```
=========================================
Decofile Operator E2E Tests
=========================================

✓ Operator is deployed
✓ Namespace ready

Test 1: Inline Decofile
========================
✓ ConfigMap created
✓ ConfigMap contains expected data

Test 2: Knative Service Injection
===================================
✓ Service is ready
✓ Pod is running

Test 3: ConfigMap Injection Verification
==========================================
✓ ConfigMap is mounted in pod

Test 4: Reload Endpoint
========================
✓ Reload endpoint responded successfully
✓ Pod logged reload request
✓ Pod logs show config file content

Test 5: ConfigMap Update & Notification
=========================================
✓ Operator detected ConfigMap change
✓ Operator successfully notified pods
✓ Pod reloaded with updated content!

=========================================
All Tests Passed! ✅
=========================================
```

## Debugging

### View operator logs
```bash
make kind-logs
```

### View test app logs
```bash
kubectl logs -n sites-test -l serving.knative.dev/service=test -f
```

### Inspect resources
```bash
kubectl get all -n sites-test
kubectl get decofiles -n sites-test
kubectl get configmaps -n sites-test
```

### Manual cleanup
```bash
kubectl delete namespace sites-test
```

## GitHub Source Testing

The `decofile-github.yaml` manifest uses the public `deco-cx/apps` repository.
To test with a different repository, edit the manifest or set environment variables
in the operator deployment:

```bash
kubectl set env deployment/operator-controller-manager \
  GITHUB_TOKEN=ghp_your_token \
  -n operator-system
```

## Adding New Tests

To add new test cases:

1. Create manifest in `manifests/`
2. Add test logic to `hack/test-kind-e2e.sh`
3. Update this README

## Notes

- Test app uses Deno 2.x with Alpine Linux base
- Knative Service uses "default" injection (namespace-based resolution)
- Tests are idempotent - can be run multiple times
- Cleanup is optional - namespace can be reused for debugging


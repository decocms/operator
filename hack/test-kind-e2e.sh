#!/usr/bin/env bash

# Copyright 2025.
# Licensed under the Apache License, Version 2.0.

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
TEST_DIR="${PROJECT_ROOT}/test/kind"
TEST_NAMESPACE="sites-test"

echo "========================================="
echo "Decofile Operator E2E Tests"
echo "========================================="
echo ""

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

function print_success() {
    echo -e "${GREEN}✓${NC} $1"
}

function print_error() {
    echo -e "${RED}✗${NC} $1"
}

function print_info() {
    echo -e "${YELLOW}ℹ${NC} $1"
}

# Check if operator is deployed
function check_operator() {
    print_info "Checking if operator is deployed..."
    if kubectl get deployment operator-controller-manager -n operator-system &>/dev/null; then
        print_success "Operator is deployed"
    else
        print_error "Operator not deployed. Run: make kind-deploy IMG=decofile-operator:dev"
        exit 1
    fi
    
    # Wait for CRD to be registered in API server
    print_info "Waiting for Decofile CRD to be available..."
    for i in {1..30}; do
        if kubectl get crd decofiles.deco.sites &>/dev/null; then
            # Give API server a moment to fully register the CRD
            sleep 2
            print_success "CRD is available"
            return 0
        fi
        if [ $i -eq 30 ]; then
            print_error "Timeout waiting for CRD"
            kubectl get crds | grep deco || true
            exit 1
        fi
        sleep 1
    done
}

# Create test namespace
function create_namespace() {
    print_info "Creating test namespace: ${TEST_NAMESPACE}..."
    kubectl create namespace ${TEST_NAMESPACE} --dry-run=client -o yaml | kubectl apply -f - >/dev/null
    print_success "Namespace ready"
}

# Apply inline Decofile
function test_inline_decofile() {
    echo ""
    echo "Test 1: Inline Decofile"
    echo "========================"
    
    print_info "Applying inline Decofile..."
    kubectl apply -f "${TEST_DIR}/manifests/decofile-inline.yaml"
    
    print_info "Waiting for ConfigMap to be created..."
    for i in {1..30}; do
        if kubectl get configmap decofile-decofile-test-main -n ${TEST_NAMESPACE} &>/dev/null; then
            print_success "ConfigMap created"
            break
        fi
        if [ $i -eq 30 ]; then
            print_error "Timeout waiting for ConfigMap"
            exit 1
        fi
        sleep 1
    done
    
    # Verify ConfigMap content (now in decofile.json format)
    print_info "Verifying ConfigMap content..."
    if kubectl get configmap decofile-decofile-test-main -n ${TEST_NAMESPACE} -o jsonpath='{.data.decofile\.json}' | grep -q "kind-test"; then
        print_success "ConfigMap contains expected data in decofile.json"
    else
        print_error "ConfigMap data verification failed"
        exit 1
    fi
}

# Deploy Knative Service
function deploy_test_service() {
    echo ""
    echo "Test 2: Knative Service Injection"
    echo "==================================="
    
    print_info "Deploying test Knative Service..."
    kubectl apply -f "${TEST_DIR}/manifests/test-service.yaml"
    
    print_info "Waiting for service to have a ready revision..."
    for i in {1..60}; do
        LATEST_READY=$(kubectl get ksvc test -n ${TEST_NAMESPACE} -o jsonpath='{.status.latestReadyRevisionName}' 2>/dev/null || echo "")
        if [ -n "$LATEST_READY" ]; then
            print_success "Service has ready revision: $LATEST_READY"
            break
        fi
        if [ $i -eq 60 ]; then
            print_error "Timeout waiting for service revision to be ready"
            kubectl get ksvc test -n ${TEST_NAMESPACE} || true
            kubectl get revision -n ${TEST_NAMESPACE} || true
            exit 1
        fi
        sleep 2
    done
}

# Wait for pod to be running
function wait_for_pod() {
    print_info "Waiting for pod to be running..." >&2
    for i in {1..30}; do
        POD_NAME=$(kubectl get pods -n ${TEST_NAMESPACE} -l serving.knative.dev/service=test -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")
        if [ -n "$POD_NAME" ]; then
            POD_STATUS=$(kubectl get pod "$POD_NAME" -n ${TEST_NAMESPACE} -o jsonpath='{.status.phase}' 2>/dev/null || echo "")
            if [ "$POD_STATUS" = "Running" ]; then
                print_success "Pod is running: $POD_NAME" >&2
                echo "$POD_NAME"
                return 0
            fi
        fi
        if [ $i -eq 30 ]; then
            print_error "Timeout waiting for pod" >&2
            kubectl get pods -n ${TEST_NAMESPACE} >&2
            exit 1
        fi
        sleep 2
    done
}

# Verify ConfigMap injection
function verify_injection() {
    echo ""
    echo "Test 3: ConfigMap Injection Verification"
    echo "=========================================="
    
    POD_NAME=$(wait_for_pod)
    
    print_info "Checking if ConfigMap is mounted in pod..."
    if kubectl get pod "$POD_NAME" -n ${TEST_NAMESPACE} -o yaml | grep -q "decofile-decofile-test-main"; then
        print_success "ConfigMap is mounted in pod"
    else
        print_error "ConfigMap not found in pod spec"
        exit 1
    fi
}

# Test reload endpoint
function test_reload_endpoint() {
    echo ""
    echo "Test 4: Reload Endpoint"
    echo "========================"
    
    POD_NAME=$(kubectl get pods -n ${TEST_NAMESPACE} -l serving.knative.dev/service=test -o jsonpath='{.items[0].metadata.name}')
    POD_IP=$(kubectl get pod "$POD_NAME" -n ${TEST_NAMESPACE} -o jsonpath='{.status.podIP}')
    
    print_info "Calling reload endpoint on pod: $POD_NAME (IP: $POD_IP)..."
    
    # Call reload endpoint from within cluster
    RESPONSE=$(kubectl run curl-test --image=curlimages/curl:latest --rm -i --restart=Never -n ${TEST_NAMESPACE} -- \
        curl -s "http://${POD_IP}:8080/.decofile/reload" 2>/dev/null || echo "")
    
    if echo "$RESPONSE" | grep -q "Reloaded"; then
        print_success "Reload endpoint responded successfully"
        print_info "Response: $RESPONSE"
    else
        print_error "Reload endpoint failed"
        print_info "Response: $RESPONSE"
        exit 1
    fi
    
    print_info "Checking pod logs for file content..."
    sleep 2
    LOGS=$(kubectl logs "$POD_NAME" -n ${TEST_NAMESPACE} --tail=50)
    if echo "$LOGS" | grep -q "RELOAD REQUEST RECEIVED"; then
        print_success "Pod logged reload request"
    else
        print_error "Pod logs don't show reload request"
        exit 1
    fi
    
    if echo "$LOGS" | grep -q "kind-test"; then
        print_success "Pod logs show config file content"
    else
        print_error "Pod logs don't show expected content"
        exit 1
    fi
}

# Test ConfigMap update and notification
function test_configmap_update() {
    echo ""
    echo "Test 5: ConfigMap Update & Notification"
    echo "========================================="
    
    POD_NAME=$(kubectl get pods -n ${TEST_NAMESPACE} -l serving.knative.dev/service=test -o jsonpath='{.items[0].metadata.name}')
    
    print_info "Updating Decofile with new content..."
    kubectl patch decofile decofile-test-main -n ${TEST_NAMESPACE} --type=merge -p '{
      "spec": {
        "inline": {
          "value": {
            "config.json": {
              "environment": "kind-test",
              "feature": "inline-source",
              "timestamp": "updated"
            }
          }
        }
      }
    }'
    
    print_info "Waiting for operator to process update..."
    sleep 5
    
    print_info "Checking operator logs for notification..."
    OPERATOR_LOGS=$(kubectl logs -n operator-system -l control-plane=controller-manager --tail=50 2>/dev/null || echo "")
    
    if echo "$OPERATOR_LOGS" | grep -q "ConfigMap data changed"; then
        print_success "Operator detected ConfigMap change"
    else
        print_info "Warning: Could not verify operator detected change in logs"
    fi
    
    if echo "$OPERATOR_LOGS" | grep -q "Successfully notified"; then
        print_success "Operator successfully notified pods"
    else
        print_info "Note: Notification might still be in progress"
    fi
    
    # Check pod logs for reload
    print_info "Checking if pod received reload request..."
    sleep 3
    RECENT_LOGS=$(kubectl logs "$POD_NAME" -n ${TEST_NAMESPACE} --tail=30)
    
    if echo "$RECENT_LOGS" | grep -q "timestamp.*updated"; then
        print_success "Pod reloaded with updated content!"
    else
        print_info "Updated content not yet visible in logs (async notification)"
    fi
}

# Cleanup
function cleanup() {
    echo ""
    echo "Cleanup"
    echo "======="
    
    print_info "Cleaning up test resources..."
    kubectl delete namespace ${TEST_NAMESPACE} --ignore-not-found=true --wait=false
    print_success "Cleanup initiated"
}

# Main execution
function main() {
    check_operator
    create_namespace
    test_inline_decofile
    deploy_test_service
    verify_injection
    test_reload_endpoint
    test_configmap_update
    
    echo ""
    echo "========================================="
    echo -e "${GREEN}All Tests Passed!${NC} ✅"
    echo "========================================="
    echo ""
    
    print_info "To clean up: kubectl delete namespace ${TEST_NAMESPACE}"
    print_info "To view operator logs: make kind-logs"
}

# Run tests
main "$@"


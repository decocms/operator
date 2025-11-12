#!/usr/bin/env bash

# Copyright 2025.
# Licensed under the Apache License, Version 2.0.

set -o errexit
set -o nounset
set -o pipefail

CLUSTER_NAME="${KIND_CLUSTER_NAME:-kind}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "========================================="
echo "Setting up Kind cluster: ${CLUSTER_NAME}"
echo "========================================="

# Check if cluster exists
if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
    echo "✓ Kind cluster '${CLUSTER_NAME}' already exists"
    
    # Switch context
    kubectl config use-context "kind-${CLUSTER_NAME}"
else
    echo "✗ Kind cluster '${CLUSTER_NAME}' not found"
    echo "Please create the cluster first with: kind create cluster --name ${CLUSTER_NAME}"
    exit 1
fi

# Install cert-manager
echo ""
echo "========================================="
echo "Installing cert-manager..."
echo "========================================="
bash "${SCRIPT_DIR}/install-cert-manager.sh"

# Install Knative
echo ""
echo "========================================="
echo "Installing Knative Serving..."
echo "========================================="
bash "${SCRIPT_DIR}/install-knative.sh"

echo ""
echo "========================================="
echo "✓ Kind cluster setup complete!"
echo "========================================="
echo ""
echo "Cluster: ${CLUSTER_NAME}"
echo "Context: kind-${CLUSTER_NAME}"
echo ""
echo "Next steps:"
echo "  1. Build and load operator image:"
echo "     make kind-load IMG=decofile-operator:dev"
echo ""
echo "  2. Deploy operator:"
echo "     make deploy IMG=decofile-operator:dev"
echo ""
echo "  3. Test with samples:"
echo "     kubectl apply -f config/samples/"
echo ""


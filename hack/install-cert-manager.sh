#!/usr/bin/env bash

# Copyright 2025.
# Licensed under the Apache License, Version 2.0.

set -o errexit
set -o nounset
set -o pipefail

CERT_MANAGER_VERSION="${CERT_MANAGER_VERSION:-v1.14.2}"

echo "Installing cert-manager ${CERT_MANAGER_VERSION}..."

# Check if cert-manager is already installed
if kubectl get namespace cert-manager >/dev/null 2>&1; then
    echo "cert-manager namespace already exists, checking if it's running..."
    if kubectl get pods -n cert-manager -l app=cert-manager -o jsonpath='{.items[0].status.phase}' 2>/dev/null | grep -q Running; then
        echo "✓ cert-manager is already installed and running"
        exit 0
    fi
fi

# Install cert-manager
kubectl apply -f "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml"

echo "Waiting for cert-manager to be ready..."
kubectl wait --for=condition=Available --timeout=300s \
    deployment/cert-manager \
    deployment/cert-manager-cainjector \
    deployment/cert-manager-webhook \
    -n cert-manager

echo "✓ cert-manager installed successfully"


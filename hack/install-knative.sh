#!/usr/bin/env bash

# Copyright 2025.
# Licensed under the Apache License, Version 2.0.

set -o errexit
set -o nounset
set -o pipefail

KNATIVE_VERSION="${KNATIVE_VERSION:-v1.12.0}"

echo "Installing Knative Serving ${KNATIVE_VERSION}..."

# Check if Knative is already installed
if kubectl get namespace knative-serving >/dev/null 2>&1; then
    echo "knative-serving namespace already exists, checking if it's running..."
    if kubectl get pods -n knative-serving -l app=controller -o jsonpath='{.items[0].status.phase}' 2>/dev/null | grep -q Running; then
        echo "✓ Knative Serving is already installed and running"
        exit 0
    fi
fi

# Install Knative Serving CRDs
echo "Installing Knative Serving CRDs..."
kubectl apply -f "https://github.com/knative/serving/releases/download/knative-${KNATIVE_VERSION}/serving-crds.yaml"

# Install Knative Serving core components
echo "Installing Knative Serving core components..."
kubectl apply -f "https://github.com/knative/serving/releases/download/knative-${KNATIVE_VERSION}/serving-core.yaml"

# Wait for Knative to be ready
echo "Waiting for Knative Serving to be ready..."
kubectl wait --for=condition=Available --timeout=300s \
    deployment/activator \
    deployment/autoscaler \
    deployment/controller \
    deployment/webhook \
    -n knative-serving

# Install Kourier as networking layer for Kind
echo "Installing Kourier networking layer..."
kubectl apply -f "https://github.com/knative/net-kourier/releases/download/knative-${KNATIVE_VERSION}/kourier.yaml"

# Wait for Kourier to be ready
kubectl wait --for=condition=Available --timeout=300s \
    deployment/net-kourier-controller \
    -n knative-serving

kubectl wait --for=condition=Available --timeout=300s \
    deployment/3scale-kourier-gateway \
    -n kourier-system

# Configure Knative to use Kourier
kubectl patch configmap/config-network \
    --namespace knative-serving \
    --type merge \
    --patch '{"data":{"ingress-class":"kourier.ingress.networking.knative.dev"}}'

# Configure DNS for Kind (using sslip.io)
kubectl patch configmap/config-domain \
    --namespace knative-serving \
    --type merge \
    --patch '{"data":{"127.0.0.1.sslip.io":""}}'

# Configure Knative to skip tag resolving for localhost registry (for Kind testing)
kubectl patch configmap/config-deployment \
    --namespace knative-serving \
    --type merge \
    --patch '{"data":{"registriesSkippingTagResolving":"localhost:5000"}}'

echo "✓ Knative Serving installed successfully"


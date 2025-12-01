#!/bin/bash

if [ -z "$GITHUB_TOKEN" ]; then
    echo "GITHUB_TOKEN is not set"
    exit 1
fi

helm upgrade --install decocms-operator chart/ --namespace decocms-operator --set github.token=$GITHUB_TOKEN  --set image.repository=ghcr.io/decocms/operator --set image.tag=latest --set replicaCount=1 --wait
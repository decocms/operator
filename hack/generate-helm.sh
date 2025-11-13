#!/usr/bin/env bash

# Copyright 2025.
# Licensed under the Apache License, Version 2.0.

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

echo "Building Helm generator..."
cd "${SCRIPT_DIR}/helm-generator"
go build -o "${PROJECT_ROOT}/bin/helm-generator" .

echo "Running Helm generator..."
cd "${PROJECT_ROOT}"
export PROJECT_ROOT
"${PROJECT_ROOT}/bin/helm-generator"

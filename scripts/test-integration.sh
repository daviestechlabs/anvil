#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
: "${KUBEBUILDER_ASSETS:?Set KUBEBUILDER_ASSETS to local envtest binaries}"
ANVIL_ENVTEST=1 go test -race -count=1 ./internal/controller -run TestKubernetesAPI

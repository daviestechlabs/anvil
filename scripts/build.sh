#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
: "${REGISTRY:=anvil.local}"
: "${IMAGE:=anvil-operator}"
: "${TAG:=dev-$(git rev-parse --short HEAD)}"
: "${PLATFORMS:=linux/amd64}"
: "${PUSH:=0}"
args=(buildx build --platform "${PLATFORMS}" -f Dockerfile -t "${REGISTRY}/${IMAGE}:${TAG}")
if [[ -n "${EXTRA_TAGS:-}" ]]; then
  for extra_tag in ${EXTRA_TAGS}; do
    args+=(-t "${REGISTRY}/${IMAGE}:${extra_tag}")
  done
fi
args+=(--label "org.opencontainers.image.revision=$(git rev-parse HEAD)"
  --label "org.opencontainers.image.licenses=MIT")
if [[ "${PUSH}" == "1" ]]; then
  args+=(--push)
else
  args+=(--load)
fi
docker "${args[@]}" .

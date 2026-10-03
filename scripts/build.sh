#!/usr/bin/env bash
# Build the multi-architecture container image.
#
#   ./scripts/build.sh              # build both architectures into the local cache
#   PUSH=1 ./scripts/build.sh       # build and push to the registry
#   PLATFORMS=linux/arm64 ./scripts/build.sh   # single architecture, --load into docker
set -euo pipefail

cd "$(dirname "$0")/.."

IMAGE="${IMAGE:-wuzm219/updatesite}"
TAG="${TAG:-$(cat VERSION 2>/dev/null || echo dev)}"
PLATFORMS="${PLATFORMS:-linux/amd64,linux/arm64}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

# --load only works for a single platform, --push is required for manifests.
if [ -n "${PUSH:-}" ]; then
  OUTPUT=(--push)
elif [ "$PLATFORMS" = "linux/amd64" ] || [ "$PLATFORMS" = "linux/arm64" ]; then
  OUTPUT=(--load)
else
  OUTPUT=()
fi

set -x
docker buildx build \
  --platform "$PLATFORMS" \
  --build-arg "VERSION=$TAG" \
  --build-arg "COMMIT=$COMMIT" \
  --build-arg "BUILD_DATE=$BUILD_DATE" \
  -t "$IMAGE:$TAG" \
  -t "$IMAGE:latest" \
  "${OUTPUT[@]}" \
  .

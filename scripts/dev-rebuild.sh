#!/usr/bin/env bash
# Rebuild the image from the working tree and restart the stack.
#
#   ./scripts/dev-rebuild.sh
#   VERSION=1.2.0 ./scripts/dev-rebuild.sh
#
# Version, commit and build time come from .env or the environment; they are
# what the footer and /api/v1/health report.
#
# This uses `docker build` rather than `docker compose build` on purpose. When
# Docker Desktop switches the default buildx builder to a docker-container one
# (it names them after a random pair of words), compose leaves the result in
# that builder's cache instead of the local image store, then sees no change and
# keeps the old container running — the site silently serves a stale build.
# `docker build` always lands in the local store.
set -euo pipefail
cd "$(dirname "$0")/.."

if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a
fi

VERSION="${VERSION:-dev}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo none)}"
BUILD_DATE="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
IMAGE="${IMAGE:-mti/updatesite:latest}"

echo "building $IMAGE"
echo "  version   $VERSION"
echo "  commit    $COMMIT"
echo "  built at  $BUILD_DATE"

docker build \
  --build-arg "VERSION=$VERSION" \
  --build-arg "COMMIT=$COMMIT" \
  --build-arg "BUILD_DATE=$BUILD_DATE" \
  -t "$IMAGE" .

# The image changed underneath the container, so force a recreate; a plain
# `up -d` would decide nothing had changed.
docker compose up -d --force-recreate

echo
echo "waiting for the health check"
for _ in $(seq 1 30); do
  status="$(docker inspect "$(docker compose ps -q)" --format '{{.State.Health.Status}}' 2>/dev/null || echo unknown)"
  if [ "$status" = "healthy" ]; then
    echo "healthy"
    exit 0
  fi
  sleep 2
done

echo "still $status after 60s; recent logs:" >&2
docker compose logs --tail 20 >&2
exit 1

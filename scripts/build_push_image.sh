#!/usr/bin/env bash
# build_push_image.sh — build and push a single service image to GHCR.
#
# For manual/local test builds (e.g. testing a branch before CI publishes
# it). CI (.github/workflows/ci.yml) builds with the plain `docker build` on
# GitHub's native amd64 runners, so it never needed --platform. Building
# locally on Apple Silicon does: without it, `docker build` produces an
# arm64-only image, and pulling it on an amd64 target (ECS Express is
# x86-only — see CLAUDE.md) fails with "no match for platform in manifest".
# This always builds via buildx with an explicit --platform for that reason.
set -euo pipefail

usage() {
  echo "Usage: $0 <tag> <api|ingestion|migrate|dashboard> [--production] [--platform linux/amd64]" >&2
  exit 1
}

[ $# -ge 2 ] || usage

TAG="$1"
SERVICE="$2"
shift 2

PLATFORM="linux/amd64"
BUILD_TAGS=""

while [ $# -gt 0 ]; do
  case "$1" in
    --production) BUILD_TAGS="production" ;;
    --platform) shift; PLATFORM="$1" ;;
    *) usage ;;
  esac
  shift
done

case "$SERVICE" in
  api|ingestion|migrate|dashboard) ;;
  *) usage ;;
esac

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

IMAGE_BASE="ghcr.io/ahmd-soliman/axiaops"
IMAGE="${IMAGE_BASE}/${SERVICE}:${TAG}"
APP_VERSION="${TAG}"
APP_COMMIT_SHA="$(git rev-parse --short HEAD 2>/dev/null || echo local)"

echo "Building ${IMAGE} for ${PLATFORM}..."

case "$SERVICE" in
  api|ingestion)
    docker buildx build --platform "$PLATFORM" --push \
      -t "$IMAGE" \
      -f "services/${SERVICE}/Dockerfile" \
      --build-arg BUILD_TAGS="$BUILD_TAGS" \
      --build-arg APP_VERSION="$APP_VERSION" \
      --build-arg APP_COMMIT_SHA="$APP_COMMIT_SHA" \
      .
    ;;
  migrate)
    docker buildx build --platform "$PLATFORM" --push \
      -t "$IMAGE" \
      -f services/migrate/Dockerfile \
      --build-arg APP_VERSION="$APP_VERSION" \
      --build-arg APP_COMMIT_SHA="$APP_COMMIT_SHA" \
      .
    ;;
  dashboard)
    docker buildx build --platform "$PLATFORM" --push \
      -t "$IMAGE" \
      -f services/dashboard/Dockerfile \
      services/dashboard
    ;;
esac

echo "Pushed ${IMAGE}"

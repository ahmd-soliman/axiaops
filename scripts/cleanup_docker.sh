#!/bin/bash
set -euo pipefail

# cleanup_docker.sh - Clean up AxiaOps Docker resources
#
# Usage:
#   ./scripts/cleanup_docker.sh [--all]
#
# Options:
#   --all    Also clean up non-AxiaOps containers and networks (more aggressive)

CLEANUP_ALL=false
if [[ "${1:-}" == "--all" ]]; then
    CLEANUP_ALL=true
fi

echo "🧹 Cleaning up AxiaOps Docker resources..."

# Stop and remove AxiaOps containers
echo "Stopping AxiaOps containers..."
docker ps -q --filter "name=axiaops" | xargs -r docker stop 2>/dev/null || true
docker ps -aq --filter "name=axiaops" | xargs -r docker rm 2>/dev/null || true

# Remove integration test containers specifically
echo "Cleaning integration test containers..."
docker ps -aq --filter "label=com.docker.compose.project=axiaops-test-infra" | xargs -r docker rm -f 2>/dev/null || true

# Remove dev Redis container
echo "Removing dev Redis container..."
docker rm -f axiaops-dev-redis 2>/dev/null || true

# Clean up networks
echo "Cleaning up networks..."
docker network ls -q --filter "name=axiaops" | xargs -r docker network rm 2>/dev/null || true
docker network ls -q --filter "label=com.docker.compose.project=axiaops-test-infra" | xargs -r docker network rm 2>/dev/null || true

# Clean up volumes
echo "Cleaning up volumes..."
docker volume ls -q --filter "name=axiaops" | xargs -r docker volume rm 2>/dev/null || true
docker volume ls -q --filter "label=com.docker.compose.project=axiaops-test-infra" | xargs -r docker volume rm 2>/dev/null || true

# Remove dangling images
echo "Removing dangling images..."
docker image prune -f 2>/dev/null || true

if [[ "$CLEANUP_ALL" == "true" ]]; then
    echo "🔥 Aggressive cleanup mode - removing all unused Docker resources..."
    docker system prune -af --volumes 2>/dev/null || true
    echo "✅ Aggressive cleanup complete"
else
    echo "✅ AxiaOps Docker cleanup complete"
    echo "💡 Run with --all flag for more aggressive cleanup of all unused Docker resources"
fi
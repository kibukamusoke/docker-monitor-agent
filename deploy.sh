#!/bin/bash

# Docker Monitor Agent - Build, Push & Deploy Script
# Usage:
#   ./deploy.sh          - Deploy only (pull from Docker Hub)
#   ./deploy.sh build    - Build image locally
#   ./deploy.sh push     - Build and push to Docker Hub
#   ./deploy.sh all      - Build, push, and deploy

set -e

AGENT_NAME="docker-monitor-agent"
AGENT_PORT="${AGENT_PORT:-9876}"
AGENT_IMAGE="appleberryd/dockermonitor"
AGENT_TAG="${AGENT_TAG:-latest}"
FULL_IMAGE="${AGENT_IMAGE}:${AGENT_TAG}"
AGENT_ALLOWED_ORIGIN="${AGENT_ALLOWED_ORIGIN:-}"
AGENT_AUTH_TOKEN="${AGENT_AUTH_TOKEN:-}"

# Generate a token if not provided (secure-by-default deploy).
if [ -z "$AGENT_AUTH_TOKEN" ]; then
    if command -v openssl &> /dev/null; then
        AGENT_AUTH_TOKEN="$(openssl rand -hex 32)"
    else
        AGENT_AUTH_TOKEN="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
    fi
    echo "Generated AGENT_AUTH_TOKEN for this deployment."
fi

# Parse command
COMMAND="${1:-deploy}"

# Build function
build_image() {
    echo "=== Building Docker Monitor Agent ==="
    docker build -t "$FULL_IMAGE" -t "${AGENT_IMAGE}:latest" .
    echo "✅ Built: $FULL_IMAGE"
}

# Push function
push_image() {
    echo "=== Pushing to Docker Hub ==="
    docker push "$FULL_IMAGE"
    if [ "$AGENT_TAG" != "latest" ]; then
        docker push "${AGENT_IMAGE}:latest"
        docker buildx build --platform linux/amd64,linux/arm64 -t "${AGENT_IMAGE}:0.1.1" --push .
    fi
    echo "✅ Pushed: $FULL_IMAGE"
}

# Handle commands
case "$COMMAND" in
    build)
        build_image
        exit 0
        ;;
    push)
        build_image
        push_image
        exit 0
        ;;
    all)
        build_image
        push_image
        # Continue to deploy
        ;;
    deploy)
        # Continue to deploy
        ;;
    *)
        echo "Unknown command: $COMMAND"
        echo "Usage: ./deploy.sh [build|push|deploy|all]"
        exit 1
        ;;
esac

echo "=== Docker Monitor Agent Deployment ==="

# Check if Docker is available
if ! command -v docker &> /dev/null; then
    echo "Error: Docker is not installed or not in PATH"
    exit 1
fi

# Check if Docker daemon is running
if ! docker info &> /dev/null; then
    echo "Error: Docker daemon is not running"
    exit 1
fi

# Stop and remove existing container if present
if docker ps -a --format '{{.Names}}' | grep -q "^${AGENT_NAME}$"; then
    echo "Stopping and removing existing agent container..."
    docker stop "$AGENT_NAME" 2>/dev/null || true
    docker rm "$AGENT_NAME" 2>/dev/null || true
fi

# Pull latest image
echo "Pulling latest agent image: $FULL_IMAGE"
docker pull "$FULL_IMAGE" || {
    echo "Could not pull image. Building locally..."
    # If we can't pull, try to build from local Dockerfile
    if [ -f "Dockerfile" ]; then
        docker build -t "$FULL_IMAGE" .
    else
        echo "Error: No Dockerfile found and image pull failed"
        exit 1
    fi
}

# Run the agent
echo "Starting Docker Monitor Agent on port $AGENT_PORT..."
docker run -d \
    --name "$AGENT_NAME" \
    --restart unless-stopped \
    -p "${AGENT_PORT}:9876" \
    -e "AGENT_AUTH_TOKEN=${AGENT_AUTH_TOKEN}" \
    -e "AGENT_ALLOWED_ORIGIN=${AGENT_ALLOWED_ORIGIN}" \
    -v /var/run/docker.sock:/var/run/docker.sock:ro \
    -v /:/host:ro \
    --security-opt no-new-privileges:true \
    --read-only \
    --tmpfs /tmp \
    --memory 128m \
    --cpus 0.5 \
    --health-cmd "wget --no-verbose --tries=1 --spider http://localhost:9876/agent/health || exit 1" \
    --health-interval 30s \
    --health-timeout 10s \
    --health-retries 3 \
    --health-start-period 10s \
    --label "com.docker-monitor.agent=true" \
    "$FULL_IMAGE"

# Wait for health check
echo "Waiting for agent to become healthy..."
for i in {1..30}; do
    if docker inspect --format='{{.State.Health.Status}}' "$AGENT_NAME" 2>/dev/null | grep -q "healthy"; then
        echo ""
        echo "=== Agent deployed successfully! ==="
        echo "Agent URL: http://$(hostname -I | awk '{print $1}'):${AGENT_PORT}"
        echo "Health check: http://localhost:${AGENT_PORT}/agent/health"
        echo "Agent token: ${AGENT_AUTH_TOKEN}"
        echo ""
        echo "To view logs: docker logs -f $AGENT_NAME"
        echo "To stop: docker stop $AGENT_NAME"
        exit 0
    fi
    echo -n "."
    sleep 1
done

echo ""
echo "Warning: Agent started but health check not yet passing"
echo "Check logs with: docker logs $AGENT_NAME"

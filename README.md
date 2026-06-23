# Docker Monitor Agent

A lightweight Docker API proxy agent that provides token-authenticated access to Docker monitoring and container management endpoints.

Docker Monitor Agent is the server-side component used by the Docker Monitor mobile app. It runs as a normal container on your Docker host, reads Docker through the local Unix socket, and exposes only the endpoints the app needs for mobile monitoring and container operations. There is no cloud relay and no Docker daemon TCP configuration required.

## Trust Model

- **Local Docker access only:** the agent talks to `/var/run/docker.sock` inside the host.
- **Bearer-token auth:** every Docker and stats endpoint requires `Authorization: Bearer <AGENT_AUTH_TOKEN>`.
- **Narrow unauthenticated surface:** only `/agent/health` is public for liveness checks.
- **No cloud dependency:** the agent does not call Docker Monitor servers.
- **No telemetry:** the agent does not collect or transmit analytics.
- **Hardened recommended deployment:** the image keeps a root-compatible default for existing users, while the recommended deployment uses `--user 65532:65532`, `--group-add` for Docker socket access, `--read-only`, `--security-opt no-new-privileges:true`, memory/CPU limits, and a read-only Docker socket mount.

Read [SECURITY.md](SECURITY.md) before exposing the agent outside a trusted network.

## Current Release (March 2026)

- Current published image tag: `appleberryd/dockermonitor-agent:0.1.2`
- Default listen port: `9876`
- Auth mode: `AGENT_AUTH_TOKEN` required by default
- Public endpoint without auth: `/agent/health`
- Protected endpoints (bearer auth required): Docker API proxy routes and `/agent/stats`

Notes:

1. Use the pinned `0.1.2` tag for production stability.
2. Do not assume `latest` exists in the registry.

## Overview

This agent runs as a container that:

- **Requires no Docker daemon configuration changes**
- Accesses Docker via the Unix socket (already available)
- Exposes only the specific API endpoints needed
- Provides additional system metrics (CPU, memory, disk)
- Easy to deploy, remove, or update without touching Docker itself

```
┌─────────────────────────────────────────────────────────────┐
│                     Docker Host                              │
│                                                              │
│  ┌─────────────────┐      ┌─────────────────────────────┐   │
│  │  Docker Agent   │      │                             │   │
│  │   Container     │◄────►│  /var/run/docker.sock       │   │
│  │                 │      │  (Docker Unix Socket)       │   │
│  │  Port 9876 ─────┼──────┼──────────────────────────►  │   │
│  └─────────────────┘      └─────────────────────────────┘   │
│           ▲                                                  │
└───────────┼──────────────────────────────────────────────────┘
            │
            │ HTTP API (port 9876)
            │
    ┌───────┴───────┐
    │ Docker Monitor │
    │     App        │
    └───────────────┘
```

## Quick Start

### Option 1: Pre-built Image (Recommended)

```bash
TOKEN="$(openssl rand -hex 32)"
SOCK_GID="$(stat -c '%g' /var/run/docker.sock)"
IMAGE="appleberryd/dockermonitor-agent:0.1.2"

docker rm -f docker-monitor-agent >/dev/null 2>&1 || true
docker run -d \
  --name docker-monitor-agent \
  --restart unless-stopped \
  -p 9876:9876 \
  -e AGENT_AUTH_TOKEN="$TOKEN" \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v /:/host:ro \
  --user 65532:65532 \
  --group-add "$SOCK_GID" \
  --security-opt no-new-privileges:true \
  --read-only \
  --tmpfs /tmp \
  --memory 128m \
  --cpus 0.5 \
  "$IMAGE"

echo "AGENT_AUTH_TOKEN=$TOKEN"
```

Use the printed token in Docker Monitor when adding the server.

### Option 2: Docker Compose

```bash
git clone <repo>
cd docker-agent
export AGENT_AUTH_TOKEN="$(openssl rand -hex 32)"
export DOCKER_SOCKET_GID="$(stat -c '%g' /var/run/docker.sock)"
docker compose up -d
```

### Option 3: Docker Build & Run

```bash
# Build the image
docker build -t docker-monitor-agent .

# Run the container
docker run -d \
  --name docker-monitor-agent \
  --restart unless-stopped \
  -p 9876:9876 \
  -e AGENT_AUTH_TOKEN="<your-random-token>" \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v /:/host:ro \
  --user 65532:65532 \
  --group-add "$(stat -c '%g' /var/run/docker.sock)" \
  --security-opt no-new-privileges:true \
  --read-only \
  --tmpfs /tmp \
  --memory 128m \
  --cpus 0.5 \
  docker-monitor-agent
```

### Option 4: One-Line Deploy Script

```bash
# Run the repo deploy script from this directory
export AGENT_AUTH_TOKEN="$(openssl rand -hex 32)"
./deploy.sh deploy
```

### Option 5: Pre-built Image (expanded)

```bash
docker run -d \
  --name docker-monitor-agent \
  --restart unless-stopped \
  -p 9876:9876 \
  -e AGENT_AUTH_TOKEN="<your-random-token>" \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v /:/host:ro \
  --user 65532:65532 \
  --group-add "$(stat -c '%g' /var/run/docker.sock)" \
  --security-opt no-new-privileges:true \
  --read-only \
  --tmpfs /tmp \
  --memory 128m \
  --cpus 0.5 \
  appleberryd/dockermonitor-agent:0.1.2
```

## Verify It's Running

```bash
# Check container status
docker ps | grep docker-monitor-agent

# Test health endpoint
curl http://localhost:9876/agent/health

# Test Docker connection
curl -H "Authorization: Bearer <AGENT_AUTH_TOKEN>" http://localhost:9876/version

# Auth check (expected: 401 Unauthorized)
curl http://localhost:9876/version
```

## Required Mounts

| Mount | Mode | Why it exists |
|---|---:|---|
| `/var/run/docker.sock:/var/run/docker.sock:ro` | read-only | Allows the agent to talk to Docker Engine without enabling Docker TCP. Docker still authorizes container operations through this socket, so protect agent access carefully. |
| `/:/host:ro` | read-only | Allows `/agent/stats` to report host disk and filesystem metrics. Remove this mount if you only need Docker API proxying and not host-level disk stats. |

## Network Guidance

The safest deployment is one of:

1. Keep port `9876` reachable only on a private network or VPN.
2. Do not publish the port publicly; connect through Docker Monitor's SSH tunnel support.
3. If public exposure is unavoidable, put the agent behind HTTPS and firewall source IPs where possible.

Never run without `AGENT_AUTH_TOKEN` outside local testing.

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `AGENT_PORT` | `9876` | Port the agent listens on |
| `AGENT_AUTH_TOKEN` | *(required, no default)* | Bearer token required for all API endpoints except `/agent/health` |
| `AGENT_ALLOWED_ORIGIN` | *(empty)* | Optional CORS allowlist origin (for browser clients) |
| `AGENT_ALLOW_NO_AUTH` | `false` | Set to `true` only for explicitly insecure local/testing setups |

### Changing the Port

```bash
docker run -d \
  --name docker-monitor-agent \
  -p 8080:8080 \
  -e AGENT_PORT=8080 \
  -e AGENT_AUTH_TOKEN="<your-random-token>" \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  docker-monitor-agent
```

## API Reference

The agent provides a REST API that mirrors the Docker Engine API for the endpoints needed by Docker Monitor.

### Container Operations

| Method | Endpoint | Description | Query Parameters |
|--------|----------|-------------|------------------|
| `GET` | `/containers/json` | List containers | `all=true` for all containers |
| `GET` | `/containers/{id}/json` | Inspect container | - |
| `POST` | `/containers/{id}/start` | Start container | - |
| `POST` | `/containers/{id}/stop` | Stop container | - |
| `POST` | `/containers/{id}/restart` | Restart container | - |
| `POST` | `/containers/{id}/rename` | Rename container | `name=new-name` |
| `DELETE` | `/containers/{id}` | Remove container | `force=true` to force remove |
| `GET` | `/containers/{id}/logs` | Get container logs | `tail=100`, `stdout=true`, `stderr=true`, `timestamps=true` |
| `GET` | `/containers/{id}/stats` | Get container stats | `stream=false` for single snapshot |
| `POST` | `/containers/create` | Create container | Request body with config, optional `name=my-container` |

### Image Operations

| Method | Endpoint | Description | Query Parameters |
|--------|----------|-------------|------------------|
| `GET` | `/images/json` | List all images | - |
| `POST` | `/images/create` | Pull an image | `fromImage=nginx`, `tag=latest` |
| `DELETE` | `/images/{id}` | Remove an image | `force=true` to force remove |

### System Operations

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/version` | Docker version info |
| `GET` | `/info` | Docker system info |
| `GET` | `/networks` | List all networks |
| `GET` | `/volumes` | List all volumes |

### Agent-Specific Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/agent/health` | Health check endpoint |
| `GET` | `/agent/stats` | Comprehensive system statistics |

## System Stats Endpoint

The `/agent/stats` endpoint provides comprehensive host system metrics for dashboard and health monitoring:

```bash
curl -H "Authorization: Bearer <AGENT_AUTH_TOKEN>" http://localhost:9876/agent/stats | jq
```

Response:
```json
{
  "timestamp": "2024-01-15T10:30:00Z",
  "cpu": {
    "usage_percent": 25.5,
    "cores": 8,
    "per_core": [20.1, 30.2, 25.0, 26.8, 24.5, 28.0, 22.3, 27.1]
  },
  "memory": {
    "total": 17179869184,
    "used": 8589934592,
    "available": 8589934592,
    "used_percent": 50.0
  },
  "disk": {
    "total": 500107862016,
    "used": 250053931008,
    "free": 250053931008,
    "used_percent": 50.0,
    "path": "/var/lib/docker"
  },
  "docker": {
    "containers_running": 5,
    "containers_paused": 0,
    "containers_stopped": 3,
    "images_total": 12
  },
  "host": {
    "os": "linux",
    "arch": "amd64",
    "hostname": "docker-host"
  }
}
```

## Connecting from Docker Monitor App

In the Docker Monitor Flutter app:

1. Add a new server
2. Enter the Docker host IP address
3. Set port to `9876` (or your configured port)
4. Leave "Secure" unchecked (unless using a reverse proxy with TLS)
5. Paste the same `AGENT_AUTH_TOKEN` into "Agent Access Token"
6. Test connection

### With SSH Tunnel

The app supports SSH tunneling for secure remote access:

1. Configure an SSH profile in the app
2. Associate it with the server
3. The app will tunnel through SSH to reach port 9876 on the Docker host

## Security Considerations

### What the Agent CAN Do

The agent has access to the Docker socket, which means it can:
- List, start, stop, restart, and remove containers
- Pull and remove images
- View logs and stats
- Create new containers

### Security Measures Implemented

1. **Read-only socket mount**: The socket is mounted with `:ro` where possible
2. **No new privileges**: Container runs with `--security-opt no-new-privileges:true`
3. **Read-only filesystem**: Container filesystem is read-only
4. **Resource limits**: 128MB RAM, 0.5 CPU max
5. **Health checks**: Built-in health monitoring
6. **No shell**: Minimal Alpine image with no unnecessary tools

### Additional Security Recommendations

#### 1. Bind to localhost only
```bash
docker run -d -p 127.0.0.1:9876:9876 ...
```
Then use SSH tunneling to access remotely.

#### 2. Use a reverse proxy with authentication
```nginx
# nginx.conf
server {
    listen 443 ssl;

    ssl_certificate /path/to/cert.pem;
    ssl_certificate_key /path/to/key.pem;

    auth_basic "Docker Monitor";
    auth_basic_user_file /etc/nginx/.htpasswd;

    location / {
        proxy_pass http://127.0.0.1:9876;
    }
}
```

#### 3. Firewall rules
```bash
# Allow only specific IPs
ufw allow from 192.168.1.0/24 to any port 9876
```

#### 4. Docker network isolation
```bash
# Create isolated network
docker network create --internal docker-monitor-net

# Run agent on isolated network with explicit port binding
docker run -d \
  --name docker-monitor-agent \
  --network docker-monitor-net \
  -p 9876:9876 \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  docker-monitor-agent
```

## Building from Source

### Prerequisites
- Go 1.23 or later
- Docker (for building the image)

### Local Development

```bash
# Clone the repository
git clone <repo>
cd docker-agent

# Install dependencies
go mod tidy

# Run locally (requires Docker socket access)
go run main.go

# Or build binary
go build -o docker-agent .
./docker-agent
```

### Build Docker Image

```bash
# Local test build
docker buildx build --load \
  --provenance=mode=max \
  --sbom=true \
  -t docker-monitor-agent .

# Release build for Docker Hub with full provenance and SBOM attestations
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  --provenance=mode=max \
  --sbom=true \
  -t appleberryd/dockermonitor-agent:0.1.2 \
  -t appleberryd/dockermonitor-agent:latest \
  --push .
```

The pushed registry image is what Docker Scout checks for provenance and SBOM attestations.

## Troubleshooting

### Agent won't start

```bash
# Check logs
docker logs docker-monitor-agent

# Common issues:
# - Docker socket doesn't exist: ls -la /var/run/docker.sock
# - Permission denied: Check socket permissions
```

### Can't connect to agent

```bash
# Verify agent is running
docker ps | grep docker-monitor-agent

# Test locally
curl http://localhost:9876/agent/health

# Check if port is open
netstat -tlnp | grep 9876
```

### Health check failing

```bash
# Check agent logs
docker logs docker-monitor-agent

# Run the built-in healthcheck manually
docker exec docker-monitor-agent /docker-agent healthcheck
```

### Permission denied errors

The agent needs access to the Docker socket. Ensure:

```bash
# Check socket exists and permissions
ls -la /var/run/docker.sock
# Should show: srw-rw---- 1 root docker ...

# Pass the socket group into the recommended non-root container
SOCK_GID="$(stat -c '%g' /var/run/docker.sock)"
docker run ... --user 65532:65532 --group-add "$SOCK_GID" ...

# If using rootless Docker, the socket location may differ
# Check DOCKER_HOST environment variable
```

### Portainer says `unable to find user root`

This was caused by the hardened `scratch` image not having a `root` passwd entry. Newer builds include a minimal passwd/group file for backward compatibility. For the hardened setup, remove the `root` override, use `65532:65532`, and add the Docker socket group in the container groups setting.

### Container stats returning empty

```bash
# Verify containers are running
curl http://localhost:9876/containers/json | jq

# Get stats for specific container
curl "http://localhost:9876/containers/<container_id>/stats?stream=false" | jq
```

## Updating the Agent

```bash
IMAGE="appleberryd/dockermonitor-agent:0.1.2"
TOKEN="<your-existing-token>"
SOCK_GID="$(stat -c '%g' /var/run/docker.sock)"

# Pull target image tag
docker pull "$IMAGE"

# Stop and remove old container
docker stop docker-monitor-agent
docker rm docker-monitor-agent

# Run updated version
docker run -d \
  --name docker-monitor-agent \
  --restart unless-stopped \
  -p 9876:9876 \
  -e AGENT_AUTH_TOKEN="$TOKEN" \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v /:/host:ro \
  --user 65532:65532 \
  --group-add "$SOCK_GID" \
  --security-opt no-new-privileges:true \
  --read-only \
  --tmpfs /tmp \
  --memory 128m \
  --cpus 0.5 \
  "$IMAGE"
```

## Uninstalling

```bash
# Stop and remove container
docker stop docker-monitor-agent
docker rm docker-monitor-agent

# Remove image (optional)
docker rmi docker-monitor-agent
```

Agent removed.

## License

MIT

## Contributing

Contributions welcome! Please open an issue or PR.

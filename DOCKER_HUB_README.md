# Docker Monitor Agent

Token-authenticated Docker monitoring agent for the Docker Monitor mobile app.

Docker Monitor Agent runs as a lightweight container on your Docker host. It connects to Docker through the local Unix socket, exposes a small HTTP API for the mobile app, and adds host-level metrics for CPU, memory, disk, and Docker runtime status. There is no cloud relay and no Docker daemon TCP setup required.

## Quick Start

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

Open Docker Monitor on iOS or Android, add a server, use port `9876`, and paste the printed `AGENT_AUTH_TOKEN`.

## Why This Agent Exists

- Avoids enabling the Docker daemon TCP API.
- Gives Docker Monitor a consistent endpoint across hosts.
- Adds host metrics through `/agent/stats`.
- Supports private hosts through the app's SSH tunnel workflow.
- Keeps Docker credentials and server configuration on your device.

## Security Model

- `/agent/health` is the only unauthenticated endpoint.
- The image keeps a root-compatible default for existing users; recommended deployments use `--user 65532:65532` plus the Docker socket group.
- All Docker and stats endpoints require `Authorization: Bearer <AGENT_AUTH_TOKEN>`.
- Token comparison is constant-time.
- Recommended deployment uses non-root execution, read-only mounts, `--read-only`, `--tmpfs /tmp`, `--security-opt no-new-privileges:true`, and CPU/memory limits.
- Release images are built with Docker BuildKit provenance and SBOM attestations.
- The agent does not send telemetry or call Docker Monitor servers.

The Docker socket is powerful even when mounted read-only. Treat the agent endpoint like infrastructure access: use a strong token, keep it on a private network or behind SSH/VPN, and rotate the token if it is exposed.

## Portainer

If Portainer reports `unable to find user root`, pull the next fixed image. It includes a minimal `root` passwd entry for backward compatibility. For the hardened setup, use `65532:65532` and add the Docker socket group.

## Endpoints

Public:

- `GET /agent/health`

Authenticated:

- `GET /agent/stats`
- `GET /version`
- `GET /info`
- `GET /containers/json`
- `GET /containers/{id}/json`
- `GET /containers/{id}/logs`
- `GET /containers/{id}/stats`
- `POST /containers/{id}/start`
- `POST /containers/{id}/stop`
- `POST /containers/{id}/restart`
- `DELETE /containers/{id}`
- `GET /images/json`
- `POST /images/create`
- `DELETE /images/{id}`
- `GET /networks`
- `GET /volumes`

## Verify

```bash
curl http://localhost:9876/agent/health
curl -H "Authorization: Bearer $TOKEN" http://localhost:9876/version
curl http://localhost:9876/version
```

The final command should return `401 Unauthorized`.

## Configuration

| Variable | Default | Description |
|---|---|---|
| `AGENT_PORT` | `9876` | Port the agent listens on. |
| `AGENT_AUTH_TOKEN` | required | Bearer token for protected endpoints. |
| `AGENT_ALLOWED_ORIGIN` | empty | Optional CORS allowlist origin. |
| `AGENT_ALLOW_NO_AUTH` | `false` | Insecure local/testing mode only. |

## Links

- App website: https://docker-monitor.com
- Docker Monitor app: https://apps.apple.com/us/app/docker-monitor/id6748612857
- Google Play: https://play.google.com/store/apps/details?id=my.appleberry.dockermonitor

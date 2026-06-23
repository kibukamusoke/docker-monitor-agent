# Docker Monitor Agent Security

Docker Monitor Agent is infrastructure software. It is intentionally small, but it sits near the Docker socket, so deployment choices matter.

## Recommended Deployment

```bash
TOKEN="$(openssl rand -hex 32)"
SOCK_GID="$(stat -c '%g' /var/run/docker.sock)"

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
  appleberryd/dockermonitor-agent:0.1.2
```

## Access Control

- Use a random `AGENT_AUTH_TOKEN` with at least 32 bytes of entropy.
- For hardened deployments, run the image as `65532:65532` and add only the Docker socket group with `--group-add`.
- Keep port `9876` on a private network, VPN, or SSH tunnel.
- Prefer Docker Monitor's SSH tunnel flow for private servers.
- Put the agent behind HTTPS if it crosses an untrusted network.
- Rotate `AGENT_AUTH_TOKEN` if a device, backup, or log leaks it.

## Mounts

`/var/run/docker.sock:/var/run/docker.sock:ro`

The agent uses the Docker socket for Docker Engine API calls. A read-only bind mount reduces accidental writes to the socket file path, but Docker API permissions are still controlled by Docker itself. Anyone who can successfully call the agent can perform the agent-supported Docker operations.

`/:/host:ro`

The agent uses this for host-level disk metrics. If you do not need host disk metrics, remove this mount. Docker container stats and basic Docker operations still work through the Docker socket.

## Exposed Endpoints

Unauthenticated:

- `GET /agent/health`

Authenticated:

- Docker proxy endpoints such as `/containers/json`, `/containers/{id}/logs`, `/containers/{id}/start`, `/images/json`, `/version`, and `/info`
- `GET /agent/stats`

## What The Agent Does Not Do

- It does not phone home.
- It does not send analytics.
- It does not store credentials.
- It does not require Docker daemon TCP mode.
- It does not require a Docker Monitor cloud account.

## Hardening Checklist

- Use the pinned image tag, not an implicit `latest`.
- Publish release images with `docker buildx build --provenance=mode=max --sbom=true --push`.
- Keep the agent private or tunneled through SSH/VPN.
- Use a firewall rule for port `9876`.
- Keep `AGENT_ALLOW_NO_AUTH=false`.
- Use `--user 65532:65532` for new hardened deployments. The image still supports root for backward compatibility with existing stacks.
- Run with `--read-only`, `--tmpfs /tmp`, and `--security-opt no-new-privileges:true`.
- Set CPU and memory limits.
- Remove `/:/host:ro` if host disk metrics are not needed.

#!/bin/bash
set -e

cd /Users/trevorsuna/POLARIS/MAVERICK/DockerMonitor/docker-agent
docker buildx build --platform linux/amd64,linux/arm64 \
  --provenance=mode=max \
  --sbom=true \
  -t appleberryd/dockermonitor-agent:0.1.2 \
  -t appleberryd/dockermonitor-agent:latest \
  --push .

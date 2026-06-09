#!/usr/bin/env bash
set -euo pipefail

# Start dockerd + panda-server, then exec Hermes' entrypoint.
DOCKERD_STORAGE_DRIVER="${DOCKERD_STORAGE_DRIVER:-overlay2}"

dockerd \
  --host=unix:///var/run/docker.sock \
  --storage-driver="${DOCKERD_STORAGE_DRIVER}" \
  > /var/log/dockerd.log 2>&1 &

for _ in $(seq 1 30); do
  docker info >/dev/null 2>&1 && break
  sleep 1
done
docker info >/dev/null 2>&1 || { echo "dockerd failed to start"; tail -n 50 /var/log/dockerd.log; exit 1; }

# Pull the sandbox image (cached for next boot).
SANDBOX_IMG="$(yq '.sandbox.image' /opt/data/.config/panda/config.yaml | tr -d '"')"
docker pull "${SANDBOX_IMG}" >> /var/log/dockerd.log 2>&1 || true

PANDA_CONFIG=/opt/data/.config/panda/config.yaml \
  /usr/local/bin/panda-server \
  > /var/log/panda-server.log 2>&1 &

for _ in $(seq 1 30); do
  curl -sf http://127.0.0.1:2480/health >/dev/null && break
  sleep 1
done
curl -sf http://127.0.0.1:2480/health >/dev/null || { echo "panda-server failed to start"; tail -n 50 /var/log/panda-server.log; exit 1; }

exec /opt/hermes/docker/entrypoint.sh "$@"

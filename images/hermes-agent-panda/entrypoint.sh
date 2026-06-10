#!/usr/bin/env bash
set -euo pipefail

# Start dockerd + panda-server, then exec Hermes' entrypoint.
DOCKERD_STORAGE_DRIVER="${DOCKERD_STORAGE_DRIVER:-overlay2}"

DOCKERD_LOG="/opt/data/dockerd.log"
PANDA_SERVER_LOG="/opt/data/panda-server.log"

dockerd \
  --host=unix:///var/run/docker.sock \
  --storage-driver="${DOCKERD_STORAGE_DRIVER}" \
  > "${DOCKERD_LOG}" 2>&1 &

for _ in $(seq 1 30); do
  docker info >/dev/null 2>&1 && break
  sleep 1
done
docker info >/dev/null 2>&1 || { echo "dockerd failed to start"; cat "${DOCKERD_LOG}"; exit 1; }

# Pull the sandbox image (cached for next boot).
SANDBOX_IMG="$(yq '.sandbox.image' /opt/data/.config/panda/config.yaml | tr -d '"')"
docker pull "${SANDBOX_IMG}" >> "${DOCKERD_LOG}" 2>&1 || true

# Auth: panda-server mints proxy tokens itself via client_credentials using
# PANDA_BOT_USERNAME / PANDA_BOT_TOKEN from the environment (see proxy.auth in
# the seeded config) — no credential files anywhere.
PANDA_CONFIG=/opt/data/.config/panda/config.yaml \
  /usr/local/bin/panda-server serve \
  > "${PANDA_SERVER_LOG}" 2>&1 &

for _ in $(seq 1 30); do
  curl -sf http://127.0.0.1:2480/health >/dev/null && break
  sleep 1
done
curl -sf http://127.0.0.1:2480/health >/dev/null || { echo "panda-server failed to start"; cat "${PANDA_SERVER_LOG}"; exit 1; }

exec /opt/hermes/docker/entrypoint.sh "$@"

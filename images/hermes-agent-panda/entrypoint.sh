#!/usr/bin/env bash
set -euo pipefail

# Dispatch entrypoint for the two-container pod shape (panda-chat chart):
#
#   entrypoint.sh panda-stack    dockerd + panda-server, foreground. Runs in
#                                the privileged "panda-server" sidecar — the
#                                ONLY container that sees PANDA_BOT_USERNAME /
#                                PANDA_BOT_TOKEN.
#   entrypoint.sh <anything>     bootstrap + exec `hermes <args>` (default:
#                                "gateway run"). Runs in the unprivileged
#                                "hermes" container; no bot credentials, no
#                                docker socket. Hermes reaches panda-server on
#                                127.0.0.1:2480 via the shared pod netns.
#
# Same image for both containers; the chart sets the args per container.
if [[ "${1:-}" == "panda-stack" ]]; then
  DOCKERD_STORAGE_DRIVER="${DOCKERD_STORAGE_DRIVER:-overlay2}"
  DOCKERD_LOG="/opt/data/dockerd.log"

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
  # PANDA_BOT_USERNAME / PANDA_BOT_TOKEN from the environment (see proxy.auth
  # in the seeded config) — no credential files anywhere. Foreground: this is
  # the container's main process, logs go to stdout.
  export PANDA_CONFIG=/opt/data/.config/panda/config.yaml
  exec /usr/local/bin/panda-server serve
fi

# Hermes path. Upstream v2026.6.5 moved to s6-overlay: docker/entrypoint.sh
# is now a deprecated shim that runs only the cont-init bootstrap and never
# execs the CMD, and the real ENTRYPOINT (/init) needs root plus a writable
# /run — neither exists in the hardened pod (runAsUser 10000, caps dropped).
# Replicate main-wrapper.sh by hand instead: run the bootstrap hook
# best-effort (its UID-remap/chown/s6 steps are root-only no-ops here; the
# useful parts — config seed, skills sync — work as uid 10000, which is the
# image's hermes user), then activate the venv and exec hermes.
/opt/hermes/docker/stage2-hook.sh || true
export HOME=/opt/data
cd /opt/data
# shellcheck disable=SC1091
. /opt/hermes/.venv/bin/activate

if [[ $# -eq 0 ]]; then
  exec hermes
fi
if command -v "$1" >/dev/null 2>&1; then
  exec "$@"
fi
exec hermes "$@"

#!/usr/bin/env bash
set -euo pipefail

# Dispatch entrypoint for the two-container pod shape (panda-chat chart):
#
#   entrypoint.sh panda-stack    panda-server, foreground. Runs in the
#                                unprivileged "panda-server" sidecar — the
#                                ONLY container that sees PANDA_BOT_USERNAME /
#                                PANDA_BOT_TOKEN. The "direct" sandbox backend
#                                executes Python as a subprocess inside the
#                                container; no dockerd, no sandbox image.
#   entrypoint.sh <anything>     bootstrap + exec `hermes <args>` (default:
#                                "gateway run"). Runs in the unprivileged
#                                "hermes" container; no bot credentials.
#                                Hermes reaches panda-server on
#                                127.0.0.1:2480 via the shared pod netns.
#
# Same image for both containers; the chart sets the args per container.
if [[ "${1:-}" == "panda-stack" ]]; then
  export PANDA_CONFIG=/opt/data/.config/panda/config.yaml
  # The `direct` sandbox runs executed code with `python3` off PATH; put the
  # sandbox env (analytics deps + ethpandaops pkg) first so it's the one used.
  export PATH=/opt/sandbox/bin:$PATH
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

# Tool-call visibility: Hermes runs its agent loop server-side and streams tool
# progress on a custom `event: hermes.tool.progress` SSE channel that Open-WebUI
# discards (it only reads `data:` chat chunks). Front Hermes with the toolstream
# proxy, which rewrites those events into standard `delta.tool_calls` chunks OW
# renders natively. The proxy takes the public port; Hermes moves to an internal
# one. Disable with HERMES_TOOLSTREAM_PROXY=0 (Hermes then binds the port direct).
if [[ "${HERMES_TOOLSTREAM_PROXY:-1}" == "1" ]]; then
  public_port="${API_SERVER_PORT:-8642}"
  internal_port="$((public_port + 1))"
  export API_SERVER_PORT="$internal_port"
  export TOOLSTREAM_LISTEN=":${public_port}"
  export TOOLSTREAM_UPSTREAM="http://127.0.0.1:${internal_port}"
  /usr/local/bin/hermes-toolstream-proxy &
fi

if [[ $# -eq 0 ]]; then
  exec hermes
fi
if command -v "$1" >/dev/null 2>&1; then
  exec "$@"
fi
exec hermes "$@"

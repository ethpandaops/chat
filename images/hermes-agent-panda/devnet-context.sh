#!/usr/bin/env bash
# Devnet context briefing (hermes container) — ONE-SHOT, agent-refreshable.
#
# Writes /opt/data/devnet-context.md so the agent starts every session already
# knowing this devnet: identity, fork schedule, tool URLs, node inventory.
# Context is DECLARED by the deployment (env), not discovered by probing:
#   - DEVNET_* URLs and DEVNET_TOOL_URLS ("name=url name=url") come from the
#     chart, which the ansible role renders from what it actually deployed.
#   - Fork schedule comes from the devnet's config service.
#   - Node inventory comes from panda-server (`panda ethnode networks`).
#
# Flow: render static facts immediately → wait (bounded) for the panda-server
# sidecar → render once more with the live inventory → exit. No daemon; the
# agent re-runs this script whenever it wants a fresh briefing:
#   /opt/panda/devnet-context.sh --refresh   # skip the long panda wait
set -uo pipefail

OUT="${DEVNET_CONTEXT_FILE:-/opt/data/devnet-context.md}"
NET="${DEVNET_NETWORK:-unknown}"
PANDA_CONFIG="${PANDA_CONFIG:-/opt/data/.config/panda/config.yaml}"
export PANDA_CONFIG
# Cold panda-server start (dockerd + sandbox pull + EIP embed) can take ~20m.
WAIT_SECS="${DEVNET_CONTEXT_WAIT_SECONDS:-1500}"
[[ "${1:-}" == "--refresh" ]] && WAIT_SECS=30

render() {
  {
    echo "# Devnet context — ${NET}"
    echo "_Generated $(date -u +%FT%TZ). Refresh: \`/opt/panda/devnet-context.sh --refresh\`._"
    echo
    echo "## Identity"
    echo "- network: \`${NET}\`  |  chain id: \`${DEVNET_CHAIN_ID:-?}\`"
    echo
    echo "## Services & tooling"
    [ -n "${DEVNET_RPC_URL:-}" ]      && echo "- rpc: ${DEVNET_RPC_URL}"
    [ -n "${DEVNET_EXPLORER_URL:-}" ] && echo "- explorer (dora): ${DEVNET_EXPLORER_URL}"
    [ -n "${DEVNET_FAUCET_URL:-}" ]   && echo "- faucet: ${DEVNET_FAUCET_URL}"
    [ -n "${DEVNET_CONFIG_URL:-}" ]   && echo "- config service: ${DEVNET_CONFIG_URL}"
    # Deployment-declared tool map: "dora=https://… forky=https://…"
    for kv in ${DEVNET_TOOL_URLS:-}; do
      echo "- ${kv%%=*}: ${kv#*=}"
    done
    echo

    echo "## Fork schedule (from the config service)"
    local cfg
    cfg="$(curl -fsS -m 8 "${DEVNET_CONFIG_URL%/}/cl/config.yaml" 2>/dev/null)"
    if [ -n "$cfg" ]; then
      echo '```'
      echo "$cfg" | grep -iE '^(CONFIG_NAME|.*_FORK_(EPOCH|VERSION)|SECONDS_PER_SLOT|MIN_GENESIS_TIME|GENESIS_DELAY|DEPOSIT_CHAIN_ID)' \
        || echo "(fork keys not found in cl/config.yaml)"
      echo '```'
    else
      echo "_config service unreachable — get the schedule from \`panda resources read networks://${NET}\`._"
    fi
    echo

    echo "## Node inventory (from panda-server)"
    local nodes
    nodes="$(timeout 25 panda ethnode networks "${NET}" 2>/dev/null)"
    if [ -n "$nodes" ]; then
      echo '```'; echo "$nodes"; echo '```'
      echo "_Live state per node: \`panda ethnode {finality|syncing|peers} ${NET} <instance>\`._"
    else
      echo "_panda-server not ready — enumerate with \`panda ethnode networks ${NET}\` once it is._"
    fi
  } > "${OUT}.tmp" 2>/dev/null && mv "${OUT}.tmp" "${OUT}"
}

# 1. Static facts land immediately (identity, URLs, forks).
render

# 2. Bounded wait for the panda-server sidecar, then one final render with the
#    node inventory. Exit either way — this is a one-shot, not a daemon.
deadline=$(( $(date +%s) + WAIT_SECS ))
until curl -fsS -m 3 -o /dev/null http://127.0.0.1:2480/health 2>/dev/null; do
  [ "$(date +%s)" -ge "$deadline" ] && exit 0
  sleep 15
done
render

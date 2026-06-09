# ethpandaops/chat — hermes-agent-panda

The EthPandaOps devnet AI-chat **agent image**: NousResearch Hermes Agent + the
`panda` CLI + `panda-server` + an in-container `dockerd` (for panda's Python
sandbox), with the devnet **skill pack** baked in:

- `skills/panda/` — query devnet analytics (Xatu/Prometheus/Loki/Dora/Ethnode),
  scoped to `$DEVNET_NETWORK`.
- `skills/faucet/` — fund accounts via the devnet powfaucet (`$DEVNET_FAUCET_URL`).
- `skills/join-devnet/` — enodes/bootnodes/genesis/config (`$DEVNET_CONFIG_URL`)
  and a ready-to-run client command.
- `skills/eth-node/` — inspect the user's own node against the devnet.

It also bundles the `langfuse` SDK so Hermes' `observability/langfuse` plugin can
trace turns/LLM calls/tools when enabled (env `HERMES_LANGFUSE_PUBLIC_KEY`,
`HERMES_LANGFUSE_SECRET_KEY`, `HERMES_LANGFUSE_BASE_URL`, `HERMES_LANGFUSE_ENV`).
See `.env.example` for local runs.

This image is consumed by the **`panda-chat`** Helm chart in
[`ethpandaops/ethereum-helm-charts`](https://github.com/ethpandaops/ethereum-helm-charts)
(`image.repository`), which the `bal-devnets` ansible pipeline renders per devnet.

> This repo holds only the EthPandaOps-specific agent image + skills. The generic,
> any-company chat platform (Open-WebUI + Hermes building blocks, multi-tenant org
> model) is a separate lab repo and is **not** required to build or run this image.

## Runtime contract

The chart/entrypoint expect:

- `/opt/data` PVC with `.config/panda/config.yaml` (seeded by the chart's
  `seed-config` initContainer) and `.config/panda/credentials/<hash>.json`
  (seeded by `seed-panda-creds` from the `chat` secret).
- A **privileged** pod (dockerd needs root).
- Env: `DEVNET_NETWORK`, `DEVNET_CHAIN_ID`, `DEVNET_FAUCET_URL`,
  `DEVNET_CONFIG_URL`, `DEVNET_RPC_URL`, `DEVNET_EXPLORER_URL`, `PANDA_SERVER_URL`,
  plus the model key (`$ANTHROPIC_API_KEY` or whatever `llm.apiKeyEnv` names).

The entrypoint starts dockerd → pulls the sandbox image → starts panda-server →
execs Hermes' upstream entrypoint (`gateway run`).

## Build

There is **no published upstream Hermes Agent image** — it's built from source.
So the build is two steps: build a vanilla base from `NousResearch/hermes-agent`,
then build this overlay `FROM` it.

```bash
# 1. vanilla Hermes base from upstream source
git clone --depth 1 --branch v0.11.0 \
  https://github.com/NousResearch/hermes-agent.git hermes-agent
docker build -t hermes-agent-base:0.11.0 ./hermes-agent

# 2. panda overlay (this repo)
docker build \
  --build-arg BASE_IMAGE=hermes-agent-base \
  --build-arg BASE_TAG=0.11.0 \
  --build-arg PANDA_VERSION=0.31.0 \
  -t ghcr.io/ethpandaops/hermes-agent-panda:0.11.0 \
  .
```

CI does both and pushes to GHCR — see [`.github/workflows/build.yml`](.github/workflows/build.yml)
(runs on changes to `Dockerfile`, `entrypoint.sh`, `skills/**`, or via
`workflow_dispatch` to pin the Hermes ref / panda version).

## Docs

- [`docs/panda-bot-setup.md`](docs/panda-bot-setup.md) — provisioning the panda
  bot identity (GitHub bot user + `panda auth login`).
- [`docs/panda-integration-plan.md`](docs/panda-integration-plan.md) — how panda,
  panda-server and the sandbox fit together.

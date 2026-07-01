# ethpandaops/chat — hermes-agent-panda

The EthPandaOps devnet AI-chat **agent image**: NousResearch Hermes Agent + the
`panda` CLI + `panda-server` + `dockerd` (for panda's `docker` sandbox
backend), with the devnet **skill pack** baked in:

- `skills/panda/` — query devnet analytics (Xatu ClickHouse chain events,
  container logs in ClickHouse `otel_logs`, Prometheus, Dora) and live EL/CL
  nodes via the `ethnode` module, scoped to `$DEVNET_NETWORK`.
- `skills/faucet/` — fund accounts via the devnet powfaucet (`$DEVNET_FAUCET_URL`).
- `skills/join-devnet/` — enodes/bootnodes/genesis/config (`$DEVNET_CONFIG_URL`)
  and a ready-to-run client command.

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

The image runs as **two containers from the same image** (panda-chat chart),
dispatched by the entrypoint's first arg:

- `hermes` container — entrypoint args `gateway run` (or anything ≠
  `panda-stack`): execs Hermes' upstream entrypoint. **Unprivileged**
  (uid 10000, caps dropped). Env: `DEVNET_NETWORK`, `DEVNET_CHAIN_ID`,
  `DEVNET_FAUCET_URL`, `DEVNET_CONFIG_URL`, `DEVNET_RPC_URL`,
  `DEVNET_EXPLORER_URL`, `PANDA_SERVER_URL`, plus the model key
  (`$ANTHROPIC_API_KEY` or whatever `llm.apiKeyEnv` names). It must NOT
  receive the bot credential — Hermes executes LLM-driven shell commands.
- `panda-server` container — entrypoint arg `panda-stack`: starts an in-pod
  `dockerd`, pulls the sandbox image, then execs panda-server in the foreground
  with the `docker` sandbox backend (each `panda execute` runs in its own
  sandbox container). **Privileged** (dockerd needs root) and the only container
  with `PANDA_BOT_USERNAME` / `PANDA_BOT_TOKEN` (the Authentik service-account
  identity panda-server mints client_credentials proxy tokens with — no
  credential files on the PVC, and never exposed to the executed code). dockerd
  uses `overlay2` reliably because the chart mounts `/var/lib/docker` on a
  node-local emptyDir (real ext4/xfs) — the kernel forbids stacking overlay2 on
  the pod's overlay rootfs. `DOCKERD_STORAGE_DRIVER=vfs` is a fallback only for
  exotic nodes whose kubelet dir is itself overlayfs/NFS.

Both mount the `/opt/data` PVC with `.config/panda/config.yaml` (seeded by
the chart's `seed-config` initContainer); they share the pod network
namespace, so Hermes reaches panda-server on `127.0.0.1:2480`.

## Build

There is **no published upstream Hermes Agent image** — it's built from source.
So the build is two steps: build a vanilla base from `NousResearch/hermes-agent`,
then build this overlay `FROM` it.

```bash
# 1. vanilla Hermes base from upstream source
git clone --depth 1 --branch v2026.6.5 \
  https://github.com/NousResearch/hermes-agent.git hermes-agent
docker build -t hermes-agent-base:2026.6.5 ./hermes-agent

# 2. panda overlay (this repo). PANDA_REF selects the panda images for the CLI
#    (`:<ref>`) and server (`:server-<ref>`) — a release version (e.g. `0.37.0`)
#    or a panda PR build (`pr-<n>-<sha>`). The sandbox image (`:sandbox-<ref>`)
#    is NOT baked in; dockerd pulls it at runtime, so keep the chart's
#    `panda.sandboxImage` on the same ref.
docker build \
  --build-arg BASE_IMAGE=hermes-agent-base \
  --build-arg BASE_TAG=2026.6.5 \
  --build-arg PANDA_REF=0.37.0 \
  -t ethpandaops/chat:hermes-agent-panda-2026.6.5 \
  images/hermes-agent-panda
```

CI does both and pushes to **Docker Hub** as
`docker.io/ethpandaops/chat:hermes-agent-panda-<source>` (+ `…-latest` on `main`
/ tags); it also pushes to GHCR as a private mirror. See
[`.github/workflows/build-hermes-agent-panda.yml`](../../.github/workflows/build-hermes-agent-panda.yml):
it builds on git tags, on `workflow_dispatch` (pin `hermes_ref` / `base_tag` /
`panda_ref`), and on a PR **labeled `build-docker-image`** — the heavy upstream
base build means there is no plain main-push build. The label build tags the
image by branch name
(`hermes-agent-panda-<branch>`) so a devnet can pull a branch image without a
release. The panda side mirrors this: label panda PR with `build` to publish its
`pr-<n>-<sha>` images, then pass that as `panda_ref`.

## Docs

- [`docs/panda-bot-setup.md`](docs/panda-bot-setup.md) — provisioning the panda
  bot identity (Authentik service account + app password).
- [`docs/identity-and-attribution-plan.md`](docs/identity-and-attribution-plan.md)
  — bot identity at the proxy, per-user attribution at the chat layer.
- [`docs/panda-integration-plan.md`](docs/panda-integration-plan.md) — how panda,
  panda-server and the sandbox fit together.

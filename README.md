# ethpandaops/chat

EthPandaOps devnet AI-chat container images, published to GHCR and consumed by the
[`panda-chat`](https://github.com/ethpandaops/ethereum-helm-charts) Helm chart
(rendered per devnet by the `bal-devnets` ansible pipeline).

| Image | Path | Published to | What it is |
|-------|------|--------------|------------|
| **hermes-agent-panda** | [`images/hermes-agent-panda/`](images/hermes-agent-panda/) | `ghcr.io/ethpandaops/hermes-agent-panda` | NousResearch Hermes Agent + `panda` CLI/server + dockerd + the devnet skill pack (panda/faucet/join-devnet/eth-node). |
| **open-webui-cf** | [`images/open-webui-cf/`](images/open-webui-cf/) | `ghcr.io/ethpandaops/open-webui-cf` | Open-WebUI patched to forward the `Cf-Access-Jwt-Assertion` header to the upstream model endpoint, so Hermes sees the individual Cloudflare-Access user (per-user auth + Langfuse attribution). |

Each image builds and pushes via its own workflow under
[`.github/workflows/`](.github/workflows/) (on changes under its `images/<name>/**`,
or `workflow_dispatch`). See each image's README for build details.

> This repo holds only the EthPandaOps-specific images. The generic, any-company chat
> *platform* (multi-tenant Open-WebUI + Hermes building blocks) is a separate lab repo
> on `git.starflinger.eu` and is not needed to build or run these.

## Docs

- [`docs/panda-bot-setup.md`](docs/panda-bot-setup.md) — provisioning the panda bot identity.
- [`docs/panda-integration-plan.md`](docs/panda-integration-plan.md) — how panda, panda-server and the sandbox fit together.

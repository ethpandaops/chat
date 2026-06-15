# ethpandaops/chat

EthPandaOps devnet AI-chat container images, published to **Docker Hub** (a single
`docker.io/ethpandaops/chat` package, tag-prefixed per image) and consumed by the
[`panda-chat`](https://github.com/ethpandaops/ethereum-helm-charts/tree/master/charts/panda-chat)
Helm chart (rendered per devnet by the `bal-devnets` ansible pipeline). Each is also
pushed to GHCR as a private mirror we don't rely on.

| Image | Path | Pulled from (Docker Hub tags) | What it is |
|-------|------|--------------|------------|
| **hermes-agent-panda** | [`images/hermes-agent-panda/`](images/hermes-agent-panda/) | `ethpandaops/chat:hermes-agent-panda-<base>-<sha>` (+ `…-latest`) | NousResearch Hermes Agent + `panda` CLI/server + dockerd + the devnet skill pack (panda/faucet/join-devnet). |
| **open-webui-cf** | [`images/open-webui-cf/`](images/open-webui-cf/) | `ethpandaops/chat:open-webui-cf-<ow_tag>-<sha>` (+ `…-<ow_tag>`) | Open-WebUI patched to forward the `Cf-Access-Jwt-Assertion` header to the upstream model endpoint, so Hermes sees the individual Cloudflare-Access user (per-user auth + Langfuse attribution). |

Each image builds and pushes via its own workflow under
[`.github/workflows/`](.github/workflows/) (on changes under its `images/<name>/**`,
or `workflow_dispatch`). See each image's README for build details.

> This repo holds only the EthPandaOps-specific images. The generic, any-company chat
> *platform* (multi-tenant Open-WebUI + Hermes building blocks) is a separate lab repo
> on `git.starflinger.eu` and is not needed to build or run these.

## Docs

- [`docs/panda-bot-setup.md`](docs/panda-bot-setup.md) — provisioning the panda bot identity (Authentik service account + app password).
- [`docs/identity-and-attribution-plan.md`](docs/identity-and-attribution-plan.md) — bot identity at the proxy + per-user attribution at the chat layer (the current Authentik model).
- [`docs/panda-integration-plan.md`](docs/panda-integration-plan.md) — how panda, panda-server and the sandbox fit together (older flow marked superseded where Authentik replaced it).

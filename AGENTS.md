# AGENTS.md — ethpandaops/chat

## What this repo is

The **EthPandaOps devnet AI-chat agent image** (`hermes-agent-panda`) and nothing
else. NousResearch Hermes Agent + the `panda` CLI + `panda-server` + an in-container
`dockerd`, with a devnet skill pack baked in. Published to
`ghcr.io/ethpandaops/hermes-agent-panda`.

This repo holds **only** the EthPandaOps-specific image. The generic, any-company
chat *platform* (multi-tenant Open-WebUI + Hermes building blocks) is a separate lab
repo on `git.starflinger.eu` and is **not** needed to build or run this. Do not add
platform/GitOps/Terraform/tenant code here.

## Layout

- `Dockerfile` — the panda overlay (`FROM` a vanilla Hermes base).
- `entrypoint.sh` — starts dockerd → panda-server → execs Hermes (`gateway run`).
- `skills/{panda,faucet,join-devnet,eth-node}/SKILL.md` — Hermes skills, scoped by
  `$DEVNET_*` env vars. This is the actual product surface; edit here.
- `docs/` — panda bot setup + integration notes.
- `.github/workflows/build.yml` — CI build + push to GHCR.

## Building

There is **no published upstream Hermes image** — it is built from source. So the
build is two steps (CI does both):

1. `git clone --branch <ref> github.com/NousResearch/hermes-agent` → `docker build`
   into a local `hermes-agent-base:<tag>`.
2. `docker build` this repo's Dockerfile `--build-arg BASE_IMAGE=hermes-agent-base`.

Upstream is **CalVer** (`v2026.6.5`, …), **not** semver. Pin a real tag — do not
invent `v0.11.0`-style refs (they don't exist upstream and CI will fail on clone).
See the README for the exact commands.

## Conventions

- **Never commit secrets.** `.env` is gitignored; `.env.example` is the template.
- Image target is **`ghcr.io/ethpandaops/hermes-agent-panda`** (GHCR, not docker.io).
- Branch names are prefixed `qu0b/`.
- Keep the image single-purpose; capability changes belong in `skills/`.

## How it's consumed (downstream, other repos)

- `ethpandaops/ethereum-helm-charts` → `charts/panda-chat` references this image as
  `image.repository`.
- `ethpandaops/ansible-collection-general` → `generate_kubernetes_config` renders the
  `panda-chat` chart per devnet (`gen_kubernetes_config_chat_image`).
- `ethpandaops/bal-devnets` → enables it per devnet; ArgoCD deploys.

Runtime contract: privileged pod, `/opt/data` PVC, `DEVNET_*` env, model key
(`$ANTHROPIC_API_KEY` or whatever the chart's `llm.apiKeyEnv` names). See README.

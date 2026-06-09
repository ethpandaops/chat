# AGENTS.md — ethpandaops/chat

## What this repo is

The **EthPandaOps devnet AI-chat container images** and nothing else. Two images,
each under `images/<name>/` with its own workflow, published to GHCR:

- **`hermes-agent-panda`** — NousResearch Hermes Agent + `panda` CLI/server + dockerd
  + the devnet skill pack. `ghcr.io/ethpandaops/hermes-agent-panda`.
- **`open-webui-cf`** — Open-WebUI patched to forward `Cf-Access-Jwt-Assertion`
  upstream to Hermes (per-user CF Access identity). `ghcr.io/ethpandaops/open-webui-cf`.

This repo holds **only** the EthPandaOps-specific images. The generic, any-company
chat *platform* (multi-tenant Open-WebUI + Hermes building blocks) is a separate lab
repo on `git.starflinger.eu` and is **not** needed to build or run these. Do not add
platform/GitOps/Terraform/tenant code here.

## Layout

- `images/hermes-agent-panda/` — `Dockerfile` (panda overlay), `entrypoint.sh`
  (dockerd → panda-server → Hermes `gateway run`), `skills/{panda,faucet,join-devnet,
  eth-node}/SKILL.md` (the product surface; scoped by `$DEVNET_*`), README, `.env.example`.
- `images/open-webui-cf/` — `Dockerfile` (`FROM` upstream OW + `patch.py`), `patch.py`
  (build-time patch of `get_headers_and_cookies()`; asserts uniqueness so a broken OW
  upgrade fails loudly), README.
- `docs/` — panda bot setup + integration notes.
- `.github/workflows/build-<image>.yml` — one build per image, path-scoped to
  `images/<name>/**`.

## Building

**hermes-agent-panda**: there is **no published upstream Hermes image** — it's built
from source, two steps (CI does both):
1. `git clone --branch <ref> github.com/NousResearch/hermes-agent` → `docker build`
   into a local `hermes-agent-base:<tag>`.
2. `docker build images/hermes-agent-panda --build-arg BASE_IMAGE=hermes-agent-base`.
Upstream is **CalVer** (`v2026.6.5`, …), **not** semver. Pin a real tag — do not
invent `v0.11.0`-style refs (they don't exist upstream and CI fails on clone).

**open-webui-cf**: `docker build images/open-webui-cf --build-arg OW_TAG=<tag>`. `OW_TAG`
**must match the open-webui Helm chart appVersion** (chart 14.5.0 → `0.9.5`); bump it
here and in the panda-chat chart's `open-webui.image.tag` together. The `patch.py`
target is fragile across OW upgrades — its `assert` is the guard.

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

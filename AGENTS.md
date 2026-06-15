# AGENTS.md — ethpandaops/chat

## What this repo is

The **EthPandaOps devnet AI-chat container images** and nothing else. Two images,
each under `images/<name>/` with its own workflow. The image that matters is the
**Docker Hub** one — a single `docker.io/ethpandaops/chat` package, tag-prefixed
per image (this is what the chart/devnets actually pull). Each is also pushed to
GHCR as a private mirror we don't rely on.

- **`hermes-agent-panda`** — NousResearch Hermes Agent + `panda` CLI/server + dockerd
  + the devnet skill pack. `docker.io/ethpandaops/chat:hermes-agent-panda-<base>-<sha>`
  (`<base>` = Hermes CalVer, e.g. `2026.6.5`; `<sha>` = this repo's commit).
- **`open-webui-cf`** — Open-WebUI patched to forward `Cf-Access-Jwt-Assertion`
  upstream to Hermes (per-user CF Access identity).
  `docker.io/ethpandaops/chat:open-webui-cf-<ow_tag>-<sha>`.

This repo holds **only** the EthPandaOps-specific images. The generic, any-company
chat *platform* (multi-tenant Open-WebUI + Hermes building blocks) is a separate lab
repo on `git.starflinger.eu` and is **not** needed to build or run these. Do not add
platform/GitOps/Terraform/tenant code here.

## Layout

- `images/hermes-agent-panda/` — `Dockerfile` (panda overlay), `entrypoint.sh`
  (**dispatch** by first arg: `panda-stack` → `panda-server` with the `direct`
  sandbox in the unprivileged sidecar; anything else → Hermes `gateway run`,
  fronted by the toolstream proxy, in the unprivileged main container),
  `toolstream-proxy/` (tiny Go reverse proxy that rewrites Hermes' custom
  tool-progress SSE events into standard OpenAI `delta.tool_calls` so Open-WebUI
  renders them), `skills/{panda,faucet,join-devnet}/SKILL.md` (the product
  surface; scoped by `$DEVNET_*`), README, `.env.example`.
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
- Image target is **`docker.io/ethpandaops/chat`** (one package, tag-prefixed per
  image). GHCR (`ghcr.io/ethpandaops/{hermes-agent-panda,open-webui-cf}`) is an
  incidental private mirror — don't depend on it.
- Branch names are prefixed `qu0b/`.
- Keep the image single-purpose; capability changes belong in `skills/`.

## How it's consumed (downstream, other repos)

This image is one piece of a multi-repo deployment. The pieces and where they live:

- **Helm chart** — [`ethpandaops/ethereum-helm-charts` → `charts/panda-chat`](https://github.com/ethpandaops/ethereum-helm-charts/tree/master/charts/panda-chat)
  (on `master`, **v0.2.0**). Renders the two-container pod: an **unprivileged
  `hermes`** container + a **privileged `panda-server`** sidecar that holds the bot
  credential. `image.repository` / `open-webui.image` point at the Docker Hub image
  above. Bumping `OW_TAG` here ⇄ `open-webui.image.tag` there must stay in lock-step.
- **Ansible renderer** — [`ethpandaops/ansible-collection-general` → `roles/generate_kubernetes_config`](https://github.com/ethpandaops/ansible-collection-general/tree/master/roles/generate_kubernetes_config)
  (chat work on branch `qu0b/chat-panda-bot-token`). `templates/chat.yaml.j2` +
  the `gen_kubernetes_config_chat_*` defaults. Chat is **opt-in** (`chat_enabled`,
  unlike opt-out services); panda is a separate toggle (`chat_panda_enabled`) so a
  devnet can run **plain-LLM chat** before the bot identity exists.
- **Per-devnet enablement** — [`ethpandaops/bal-devnets` → `inventories/devnet-7/.../chat.yaml`](https://github.com/ethpandaops/bal-devnets/blob/master/ansible/inventories/devnet-7/group_vars/all/chat.yaml)
  (the **devnet-7 pilot**, enabled, plain-LLM via OpenRouter for now). The role
  renders `kubernetes/devnet-7/chat/`, ArgoCD deploys it.
- **Bot identity** — [`ethpandaops/platform` → `applications/authentik`](https://github.com/ethpandaops/platform/tree/master/environments/staging/applications/authentik)
  (branch `qu0b/authentik-panda-chat-svc`). An Authentik **service account**
  (`panda-chat`-scoped) in `blueprints-configmap.yaml`'s `panda-proxy-provider`;
  panda-server mints `client_credentials` proxy tokens with it. Token lives in
  SOPS, promoted staging→prod via `./promote.sh`. See [`docs/panda-bot-setup.md`](docs/panda-bot-setup.md).
- **Edge auth** — `ethPandaOps/cloudflare` (private) `terraform/chat.tf`: one
  **wildcard** Cloudflare Access app on `chat.*.ethpandaops.io`, allow = Ethereum
  GitHub org. CF injects `Cf-Access-Authenticated-User-Email` (Open-WebUI
  trusted-header SSO) + `Cf-Access-Jwt-Assertion` (forwarded by `open-webui-cf`).
- **Secrets** — `services.enc.yaml#chat` (vals `/secrets/services/...`):
  `llm_api_key` + langfuse keys always; `panda_bot_username` / `panda_bot_token`
  only once `chat_panda_enabled`.

Runtime contract: see [`images/hermes-agent-panda/README.md`](images/hermes-agent-panda/README.md#runtime-contract)
(two containers from one image, dispatched by entrypoint arg; `/opt/data` PVC;
`DEVNET_*` env; model key via `llm.apiKeyEnv`).

**To turn panda on** (currently off everywhere): promote the platform Authentik
`panda-chat` service account to prod → add `panda_bot_username`/`panda_bot_token`
to `services.enc.yaml#chat` → ship an image with panda ≥ 0.32.0 → set
`chat_panda_enabled: true` in the devnet inventory.

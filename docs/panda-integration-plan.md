# Panda Integration — Implementation Plan

Concrete plan to wire ethpandaops's hosted panda-proxy into the chat
Hermes agent via the panda CLI and a local panda-server, all inside one
container. Targets the org `ethpandaops`; the per-org overlay mechanism
already on `qu0b/per-org-hermes-builds` carries it.

> **Superseded in part by
> [`identity-and-attribution-plan.md`](identity-and-attribution-plan.md):**
> the bot identity is now an Authentik *service account* using the
> `client_credentials` grant (no GitHub bot user, no seeded
> `credentials.json`, no refresh-token rotation). Sections below that
> described the old identity flow (§3.2 config, §5 setup, §6 rotation,
> the §2 diagram, parts of §11) have been updated; the fat-container,
> sandbox, and chart shape all still stand.

---

## 1. Summary

For orgs that opt in, the Hermes container becomes a **fat container**:
it carries Hermes + `panda` CLI + `panda-server` + a Docker daemon for
the panda Python sandbox. The container is privileged. Outbound, it
authenticates to the hosted `panda-proxy.analytics.production.platform.ethpandaops.io`
as **a single bot identity per deployment** — an Authentik service
account whose app password is injected from SOPS as env; panda-server
mints proxy access tokens from it on demand (client_credentials). All
chat users share that identity at the proxy. Per-user attribution stays
at the chat layer.

This is the minimum viable shape. It uses panda as designed (CLI → local
server → hosted proxy). No re-auth cadence: the app password does not
expire and access tokens are re-minted automatically.

---

## 2. Architecture

```
┌─── Hermes Pod (org-ethpandaops namespace) ──────────────────────┐
│                                                                 │
│  ┌──── fat container (privileged) ──────────────────────────┐   │
│  │                                                          │   │
│  │   /opt/hermes/...        hermes-agent (PID 1 via tini)   │   │
│  │   /usr/local/bin/panda          panda CLI                │   │
│  │   /usr/local/bin/panda-server   panda-server (:2480)     │   │
│  │   /usr/local/bin/dockerd        Docker daemon            │   │
│  │   /var/run/docker.sock          → spawns sandbox conts.  │   │
│  │                                                          │   │
│  │   /opt/data (PVC)                                        │   │
│  │     ├── hermes state.db, honcho/                         │   │
│  │     ├── .config/panda/config.yaml                        │   │
│  │     └── panda-storage/  (sandbox outputs, embedding $$)  │   │
│  │                                                          │   │
│  │   env: PANDA_BOT_USERNAME / PANDA_BOT_TOKEN (Secret)     │   │
│  │                                                          │   │
│  └──────────────────────────────────────────────────────────┘   │
│                              │                                  │
│  ┌──── initContainer: seed-config (existing) ──────────────┐    │
│  │   Copy hermes + panda config.yaml from ConfigMap to PVC │    │
│  └─────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────┘
                              │
                              │ HTTPS, Bearer <access token minted via
                              │ client_credentials from Authentik;
                              │ cached in memory, re-minted on expiry>
                              ▼
                hosted panda-proxy (UNCHANGED)
                              │
                              ▼
                Xatu / Prometheus / Loki / Ethnode
```

**Identity model**: the bot is an Authentik **service account**
(`panda-chat-svc`, member of the `panda-chat` group only) with a
non-expiring app password. panda-server mints proxy access tokens from
the Authentik token endpoint via the `client_credentials` grant. The
proxy validates them as it does any other Authentik-issued token; the
bot lands in the external datasource tier (no org-mirror groups). See
[`identity-and-attribution-plan.md`](identity-and-attribution-plan.md).

**Why not per-user identity at the proxy**: the data behind the proxy
is read-only org data with team-level (not user-level) ACLs. Per-user
audit is already provided by Hermes (logs `X-Hermes-Session-Key`) and
LibreChat. The proxy doesn't need to know which chat user triggered a
query.

---

## 3. Components

### 3.1 The fat container image

Built by the existing `build-hermes-agent-orgs.yml` workflow from
`orgs/ethpandaops/image/Dockerfile`. Replaces the current overlay
contents (which only installed the CLI).

Adds to the base `hermes-agent-base`:
- `panda` CLI binary (from `ethpandaops/panda` GitHub release archive)
- `panda-server` binary (separate archive in the same release)
- `dockerd` + `docker` client (from `docker:24` apt package or the
  `docker:dind` image, multi-stage copied)
- A small entrypoint shim that starts dockerd + panda-server before
  exec'ing into Hermes' upstream entrypoint
- The panda SKILL.md (kept from current overlay)

Sandbox image (`ethpandaops/panda:sandbox-<ver>`) is **pulled at first
container start**, not baked. Reasons:
- Keeps the fat image manageably small
- Allows the operator to bump just the sandbox version without
  rebuilding the overlay
- The pull happens once and is cached on the PVC

### 3.2 panda-server config (`/opt/data/.config/panda/config.yaml`)

Rendered by the panda-chat chart's ConfigMap and seeded onto the PVC by
the `seed-config` initContainer. The bot identity comes from env
(`${...}` is panda's config-loader env substitution), so the config
itself is not secret:

```yaml
server:
  host: "127.0.0.1"
  port: 2480
  base_url: "http://127.0.0.1:2480"
  sandbox_url: "http://172.17.0.1:2480"  # dockerd bridge gateway → pod

sandbox:
  backend: docker
  image: ethpandaops/panda:sandbox-<ver>
  network: "bridge"
  timeout: 300
  memory_limit: "1g"
  cpu_limit: 1.0

storage:
  base_dir: "/opt/data/panda-storage"

proxy:
  url: "https://panda-proxy.analytics.production.platform.ethpandaops.io"
  auth:
    mode: "client_credentials"
    issuer_url: "https://authentik.analytics.production.platform.ethpandaops.io/application/o/panda-proxy/"
    client_id: "panda-proxy"
    username: "${PANDA_BOT_USERNAME}"
    password: "${PANDA_BOT_TOKEN}"
```

### 3.3 Container startup

A new entrypoint shim at `/opt/panda/entrypoint.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

# 1. Start dockerd in the background.
#    Storage driver vfs is slow but works without overlayfs in-pod.
#    Switch to overlay2 if the node supports it.
dockerd \
  --host=unix:///var/run/docker.sock \
  --storage-driver=vfs \
  > /var/log/dockerd.log 2>&1 &

# 2. Wait for dockerd to be ready.
for i in {1..30}; do
  docker info >/dev/null 2>&1 && break
  sleep 1
done
docker info >/dev/null 2>&1 || { echo "dockerd failed to start"; exit 1; }

# 3. Pull sandbox image (idempotent; cached on PVC layer for next boot).
SANDBOX_IMG="$(yq -r '.sandbox.image' /opt/data/.config/panda/config.yaml)"
docker pull "${SANDBOX_IMG}" >> /var/log/dockerd.log 2>&1 || true

# 4. Start panda-server in the background.
PANDA_CONFIG=/opt/data/.config/panda/config.yaml \
  /usr/local/bin/panda-server \
  > /var/log/panda-server.log 2>&1 &

# 5. Wait for panda-server.
for i in {1..30}; do
  curl -sf http://127.0.0.1:2480/health >/dev/null && break
  sleep 1
done

# 6. Hand off to Hermes' upstream entrypoint.
exec /opt/hermes/docker/entrypoint.sh "$@"
```

Three background processes in one container. Acceptable because each
has a single owner and we don't need process supervision (pod restart
on failure is enough).

### 3.4 Persistent state on the PVC

```text
/opt/data/
├── state.db                  # hermes — existing
├── honcho/                   # hermes memory — existing
├── config.yaml               # hermes — existing
├── .config/panda/
│   └── config.yaml           # panda-server config (seeded by initContainer;
│                             # no credentials/ — tokens are minted in memory)
└── panda-storage/            # sandbox outputs, embedding cache, sessions
    ├── files/
    └── sessions/
```

PVC default size in `hermes_defaults.persistence.size` is **5Gi**. The
new panda paths add ~100 MiB worst-case (sandbox image cache lives in
Docker's own storage, not on this PVC — see §3.5). Bump to **8Gi** for
orgs with panda enabled.

### 3.5 Docker storage

`dockerd --storage-driver=vfs --data-root=/opt/data/docker-storage`
would put the sandbox image cache on the PVC — slow (VFS is copy-based)
but persistent across restarts. Or use `--data-root=/var/lib/docker`
(default) which is **ephemeral** — sandbox image re-pulls on every pod
restart. Bandwidth cost: ~1 GiB per restart.

Default: ephemeral. Reasoning:
- Pod restarts are rare (RWO PVC + Recreate strategy)
- VFS storage driver on PVC slows sandbox startup
- The 1-2 minute re-pull on cold start is acceptable

Make this a values-level decision if pod restart frequency becomes a
problem.

---

## 4. File-by-file changes in `ethpandaops/chat`

> **Historical** — this section describes the original `orgs/<slug>` /
> org-stack layout and the seeded-credentials flow. The credential
> pieces (§4.3's `seed-panda-creds` initContainer, §4.5's
> `PANDA_CONFIG_YAML` / `PANDA_CREDENTIALS_*` SOPS keys) are
> **superseded** by the service-account flow
> ([`identity-and-attribution-plan.md`](identity-and-attribution-plan.md)):
> the chart renders the panda config itself and the only secrets are
> `PANDA_BOT_USERNAME` / `PANDA_BOT_TOKEN`. The image/entrypoint/chart
> shape otherwise landed as described, in `images/hermes-agent-panda/`
> and the `panda-chat` chart (ethereum-helm-charts).

### 4.1 `orgs/ethpandaops/image/Dockerfile` (replace)

```dockerfile
ARG BASE_TAG
FROM git.starflinger.eu/qu0b/hermes-agent-base:${BASE_TAG}

USER root

# Install Docker daemon + client + sandbox runtime.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      docker.io docker-cli iptables uidmap \
      curl yq && \
    rm -rf /var/lib/apt/lists/*

ARG PANDA_VERSION=0.24.0

# panda CLI
RUN curl -fsSL \
      "https://github.com/ethpandaops/panda/releases/download/v${PANDA_VERSION}/panda_${PANDA_VERSION}_linux_amd64.tar.gz" \
    | tar -xz -C /usr/local/bin panda \
 && chmod +x /usr/local/bin/panda

# panda-server
RUN curl -fsSL \
      "https://github.com/ethpandaops/panda/releases/download/v${PANDA_VERSION}/panda-server_${PANDA_VERSION}_linux_amd64.tar.gz" \
    | tar -xz -C /usr/local/bin panda-server \
 && chmod +x /usr/local/bin/panda-server

# Custom entrypoint that starts dockerd + panda-server before Hermes.
COPY entrypoint.sh /opt/panda/entrypoint.sh
RUN chmod +x /opt/panda/entrypoint.sh

# Skill the agent loads when relevant.
COPY skills/panda /opt/hermes/skills/data/panda

# Hermes' upstream image starts as non-root; the fat container needs to run
# privileged (dockerd) so we let the entrypoint be invoked as root and have
# dockerd manage permission drops itself.
USER root
ENTRYPOINT ["/opt/panda/entrypoint.sh"]
CMD ["gateway", "run"]
```

Plus a new `orgs/ethpandaops/image/entrypoint.sh` with the shim shown in
§3.3.

The current `orgs/ethpandaops/image/config/panda.yaml` is **removed** —
the config now lives in SOPS so it can carry per-environment fields
(issuer URLs etc.) without rebuilds.

### 4.2 `charts/org-stack/values.yaml`

Add a per-agent `panda` block (default disabled), document it under the
existing agent schema:

```yaml
agents:
  - name: general
    ...
    # === Panda integration (config.yaml `extra_config.panda` not used) ===
    # When enabled, this pod runs panda-server + dockerd as additional
    # in-container processes. The pod becomes privileged. Requires the
    # org overlay image to include panda CLI/server binaries + entrypoint.
    panda:
      enabled: false
```

### 4.3 `charts/org-stack/templates/_render-hermes.tpl`

Three places to edit:

1. **Security context** — when `panda.enabled`, the pod runs privileged
   and Hermes' usual `runAsUser: 10000` doesn't apply. Wrap the
   existing `podSecurityContext`/`securityContext` blocks:

   ```yaml
   {{- if $ctx.Values.config.panda.enabled }}
   securityContext:
     privileged: true
     # Cannot drop ALL — dockerd needs CAP_NET_ADMIN, CAP_SYS_ADMIN, etc.
   {{- else }}
   securityContext: {{- toYaml $ctx.Values.securityContext | nindent 12 }}
   {{- end }}
   ```

2. **New initContainer** `seed-panda-creds` runs when `panda.enabled`:

   ```yaml
   {{- if $ctx.Values.config.panda.enabled }}
   - name: seed-panda-creds
     image: busybox:1.37
     command: ["sh","-c"]
     args:
       - |
         set -eu
         mkdir -p /data/.config/panda/credentials /data/panda-storage
         if [ -n "${PANDA_CONFIG_YAML:-}" ]; then
           printf '%s' "$PANDA_CONFIG_YAML" > /data/.config/panda/config.yaml
         fi
         if [ -n "${PANDA_CREDENTIALS_JSON:-}" ]; then
           # Filename derived from issuer/client hash — see panda's
           # store.credentialNamespaceKey. The operator computes this
           # during setup (§5) and provides it via PANDA_CREDENTIALS_FILE.
           printf '%s' "$PANDA_CREDENTIALS_JSON" \
             > "/data/.config/panda/credentials/${PANDA_CREDENTIALS_FILE}"
         fi
     envFrom:
       - secretRef: { name: org-secrets, optional: true }
     volumeMounts:
       - { name: data, mountPath: /data }
   {{- end }}
   ```

3. **`extra_config.panda` not needed** — panda-server reads its own
   config from `/opt/data/.config/panda/config.yaml`. The Hermes
   `config.yaml` is untouched.

### 4.4 `orgs/ethpandaops/values.yaml`

```yaml
agents:
  - name: general
    ...
    panda:
      enabled: true

hermes_defaults:
  image:
    repository: git.starflinger.eu/qu0b/hermes-agent-ethpandaops
    tag: "0.11.0-latest"
  persistence:
    size: 8Gi    # bumped from 5Gi to accommodate sandbox storage
```

### 4.5 `orgs/ethpandaops/sopssecrets/org-secrets.sops.yaml`

Add three new keys (operator fills via `sops edit`):

```yaml
stringData:
  ...existing keys...
  PANDA_CONFIG_YAML: |       # panda-server config — exact content from setup
    server: { ... }
    sandbox: { ... }
    proxy: { ... }
  PANDA_CREDENTIALS_FILE: "<32-hex-char-hash>.json"  # filename only
  PANDA_CREDENTIALS_JSON: |  # tokens — content of credentials/<hash>.json
    {"access_token":"...","refresh_token":"...","expires_at":"..."}
```

### 4.6 `docs/hermes-agent.md`

Add a new section §10 "Panda sidecar for Ethereum analytics" describing
the fat-container shape, the bot-user model, and the refresh cadence.
Strike anything in §8a that contradicts (the previous per-org overlay
docs assumed CLI-only, no server).

### 4.7 `docs/onboarding.md`

Add a subsection under "Adding org-specific binaries" titled "Enabling
panda for an org" with the one-time setup steps from §5 below.

### 4.8 New: `docs/panda-bot-setup.md` (operator runbook)

The step-by-step procedure for the one-time bot-user provisioning and
re-auth-every-30-days rotation. Kept separate so the team-Github-admin
who runs it can be pointed at one short doc.

---

## 5. One-time setup procedure

> Rewritten for the Authentik service-account identity; the GitHub
> bot-user + `panda auth login` procedure that used to live here is
> obsolete. Full runbook: [`panda-bot-setup.md`](panda-bot-setup.md).

Performed by an operator with: (a) Authentik admin access, (b) sops-edit
rights on the devnet secrets.

```text
1. DEPLOY THE BLUEPRINT (platform repo)
   environments/<env>/applications/authentik/.../blueprints-configmap.yaml
   defines: group panda-chat (bound to the panda-proxy application),
   service account panda-chat-svc, non-expiring app-password token
   panda-chat-svc-token.

2. RETRIEVE THE APP PASSWORD
   Authentik UI → Directory → Tokens and App passwords →
   panda-chat-svc-token → copy key.

3. SEED THE SOPS SECRET
   In the devnet's services.enc.yaml under #chat:
     panda_bot_username = panda-chat-svc
     panda_bot_token    = <app password>

4. ENABLE THE AGENT
   Devnet chat values (rendered by ansible) — the chart maps the SOPS
   keys to credentials.panda.botUsername / botToken and renders the
   client_credentials proxy.auth block into panda-config.yaml.

5. COMMIT + PUSH + WAIT FOR ARGOCD
   Within ~3 minutes the pod rolls. Check:
     kubectl -n <ns> logs deploy/<release>-hermes -c hermes
     # expect: dockerd-ready, panda-server-ready, hermes-ready

6. SMOKE TEST
   Open the chat at <hostname>. Ask a panda-backed question.
   Expect: agent shells out to `panda execute`, returns numbers.
```

---

## 6. Token lifecycle (superseded: no rotation)

> The 30-day refresh-token rotation playbook that used to live here is
> obsolete — there are no refresh tokens anymore.

panda-server mints a fresh access token (Authentik validity 1h) from
the non-expiring app password whenever the cached one approaches
expiry, entirely in memory. The only operator action left is
**revocation on suspected leak**: delete the `panda-chat-svc-token`
app password in Authentik (immediate), create a replacement, update
`services.enc.yaml#chat.panda_bot_token`, re-roll. See
[`panda-bot-setup.md`](panda-bot-setup.md).

---

## 7. Operations

### 7.1 Logs

Inside the fat container:
- `/var/log/dockerd.log`         — dockerd
- `/var/log/panda-server.log`    — panda-server
- stdout                          — hermes

To stream all three:
```bash
kubectl -n org-<slug> exec -it deploy/<release>-hermes-general -c hermes -- \
  tail -F /var/log/dockerd.log /var/log/panda-server.log
```

### 7.2 Health

The existing `/health` probe checks Hermes. Add a startup probe (not
liveness — startup is slow) that waits for panda-server too:

```yaml
startupProbe:
  exec:
    command: ["sh","-c","curl -sf http://127.0.0.1:2480/health && curl -sf http://127.0.0.1:8642/health"]
  periodSeconds: 5
  failureThreshold: 60   # 5 min — covers dockerd + sandbox-image pull
```

### 7.3 Resource sizing

| What | Default | Why |
|---|---|---|
| `requests.memory` | 1Gi   | Hermes 512Mi + panda-server 256Mi + dockerd 256Mi |
| `limits.memory`   | 6Gi   | Sandbox containers add up to ~1Gi each; allow ~2 concurrent |
| `requests.cpu`    | 300m  | Slightly bumped from 200m |
| `limits.cpu`      | 3000m | Bumped from 2000m for sandbox bursts |

Tunable per-org via `hermes_defaults.resources`.

---

## 8. Failure modes

| Symptom | Cause | Fix |
|---|---|---|
| Hermes pod stuck in startupProbe | dockerd failed | `kubectl logs … -c hermes` → grep dockerd.log; common: missing `privileged: true` |
| `panda execute` → "no server URL configured" | initContainer didn't seed config.yaml | Check the seed-config initContainer logs; restart pod |
| `panda execute` → 401 unauthorized | Bot app password wrong or revoked | Re-seed `services.enc.yaml#chat.panda_bot_token` (see §6 / panda-bot-setup.md) |
| `panda execute` → "unsupported sandbox backend" | Sandbox image not pulled / dockerd unhealthy | `kubectl exec … docker ps`; manually `docker pull ethpandaops/panda:sandbox-<ver>` |
| Sandbox container crashes immediately | Sandbox image version mismatch with panda-server | Confirm `sandbox.image` in config.yaml matches a published tag at `ethpandaops/panda:sandbox-*` |
| OOM on Hermes pod | Concurrent sandboxes | Bump `hermes_defaults.resources.limits.memory`; consider `sandbox.max_sessions` |

---

## 9. Security model & blast radius

**[Superseded: container split.]** The fat container originally ran
Hermes + panda-server + dockerd in one privileged container. The
panda-chat chart now splits the pod: an unprivileged `hermes` container
(uid 10000, caps dropped, no bot credential, no docker socket) and a
privileged `panda-server` sidecar (dockerd) that alone holds
`PANDA_BOT_USERNAME`/`PANDA_BOT_TOKEN` in a dedicated Secret. What the
privileged sidecar still exposes:

- Anyone with `exec` rights into the sidecar can break out to the node.
  Mitigation: RBAC restricts pod exec to platform admins.
- A compromise of the panda-server process (e.g., RCE in a sandbox
  callback handler) gives shell + dockerd, i.e., root on the node.
  Mitigation: panda-server is a thin, well-reviewed Go binary; the
  attack surface is the sandbox-callback HTTP API.
- Hermes (the LLM-driven shell executor) can no longer read the bot
  credential or the docker socket; it can still *use* panda-server over
  127.0.0.1 (confused-deputy — bounded by the bot's external-tier,
  read-only proxy access).

**NetworkPolicy** (new — add to `charts/org-stack/templates/`):
- Ingress to the Hermes pod: only from the LibreChat pod in the same
  namespace (port 8642).
- Egress from the Hermes pod: only to (a) cluster DNS, (b) the hosted
  panda-proxy hostname, (c) image registries. Block everything else
  (this matters because dockerd-spawned sandbox containers inherit the
  pod's egress and could otherwise call out arbitrarily).

**Bot identity scope**: the Authentik service account is a member of
the `panda-chat` group **only** — no org-mirror groups, so no internal
ClickHouse tier and no platform prometheus. Never add it to other
groups.

**Token compromise**: the app password in SOPS is non-expiring; a leak
grants external-tier read-only proxy access until the
`panda-chat-svc-token` app password is deleted in Authentik (immediate,
one click). Access tokens themselves live 1h and only in pod memory.

---

## 10. Rollback

Two levels:

1. **Disable panda for an org**: set `agents[0].panda.enabled: false` in
   `orgs/<slug>/values.yaml`. Rolls back to the pre-panda fat-container
   shape (unprivileged Hermes, no sidecar processes). PVC data
   preserved.

2. **Roll back to no-overlay base image**: set
   `hermes_defaults.image.repository` back to
   `git.starflinger.eu/qu0b/hermes-agent` (the legacy alias the base
   workflow still pushes) and `tag` to a known-good base tag.

Both are reversible by reverting the values.yaml change and waiting for
ArgoCD.

---

## 11. What's deferred (not in scope for v1)

- **Per-user identity at panda-proxy.** Decided against — see
  [`identity-and-attribution-plan.md`](identity-and-attribution-plan.md)
  §1 ("Explicitly rejected"). One bot identity per deployment at the
  proxy; per-user attribution lives at the Hermes/Langfuse layer
  (`user_id` on traces, `X-Panda-On-Behalf-Of` audit header). Revisit
  only if a datasource ever distinguishes *users* rather than *teams*.
- ~~**CronJob refresh automation.**~~ Obsolete: there is no refresh
  cadence to automate — the service account's app password does not
  expire and access tokens are re-minted in memory.
- **gVisor (runsc) sandbox.** Today: `sandbox.backend: docker`. gVisor
  is stronger but requires runsc to be installed inside the fat
  container alongside dockerd. Tractable, but not v1.
- **Replacing in-pod dockerd with Kubernetes Jobs.** A Kubernetes-native
  sandbox backend doesn't exist in panda. Would be a meaningful
  upstream PR. Defer until there's a stronger reason to drop dockerd
  than the privilege requirement.
- **Per-agent (rather than per-pod) panda enable.** Today: when
  enabled, the whole pod runs the fat container. The chart's `panda`
  block is per-agent in the values schema but its rendering is
  per-pod. If two agents in the same org need different panda
  configs, they'd need separate overlay images. Not a real
  requirement today.

---

## 12. Phased execution

Each phase is independent and reversible. Land in order; each lands on
its own PR.

**Phase A — Replace the overlay Dockerfile** (chat repo)
- Update `orgs/ethpandaops/image/Dockerfile` to install CLI + server +
  dockerd + entrypoint shim.
- Add `orgs/ethpandaops/image/entrypoint.sh`.
- Remove `orgs/ethpandaops/image/config/panda.yaml` (config now lives
  in SOPS).
- The `build-hermes-agent-orgs.yml` workflow rebuilds the overlay
  image automatically on push.
- **Don't roll yet**: nobody points at the new image until Phase D.

**Phase B — Chart support for the panda sidecar shape** (chat repo)
- Add the `agents[].panda.enabled` knob to
  `charts/org-stack/values.yaml`.
- Update `_render-hermes.tpl` for the privileged-securityContext branch
  and the `seed-panda-creds` initContainer.
- Update startup/liveness probes.
- Add `charts/org-stack/templates/networkpolicy.yaml` (new file) for
  the egress restrictions.
- Verify with `helm template` — render with and without panda enabled.
- **Don't roll yet**: every org has `panda.enabled: false`.

**Phase C — Bot-user setup** (operator, off-cluster)
- Create the ethpandaops-chat-bot GitHub user.
- Add to the appropriate teams.
- Run §5 steps 2-3 locally.
- Pre-seed the SOPS Secret with `PANDA_*` keys (still
  `panda.enabled: false`, so the keys are dormant).

**Phase D — Enable for ethpandaops** (chat repo)
- Flip `agents[0].panda.enabled: true` in
  `orgs/ethpandaops/values.yaml`.
- Bump `hermes_defaults.image.tag` to the Phase-A overlay build.
- Bump persistence size to 8Gi.
- Commit, push. ArgoCD rolls. §5 step 6-7 smoke test.

**Phase E — Documentation** (chat repo)
- Update `docs/hermes-agent.md` (new §10).
- Update `docs/onboarding.md` (link to panda-bot-setup.md).
- Add `docs/panda-bot-setup.md` (full operator runbook).

---

## 13. Open questions (decide before Phase A)

1. **`docker.io` or DinD image stage?** `docker.io` apt package adds
   ~250 MiB to the layer but is one apt line. Multi-stage from `docker:24-dind`
   is cleaner but doubles build complexity. Recommend `docker.io` for v1.
2. **`storage-driver: vfs` vs `overlay2`.** vfs always works but is slow.
   overlay2 needs node-level kernel features; usually present on Hetzner
   k3s. Default to `overlay2`, fall back to vfs if the pod logs show
   overlay errors.
3. **Sandbox image cache: PVC vs ephemeral.** Default ephemeral. Revisit
   if pod restarts get frequent.
4. **Egress allowlist host vs IP.** NetworkPolicy egress can match by
   namespaceSelector + podSelector OR by ipBlock. The hosted panda-proxy
   is reached by hostname — use Cilium/Calico's FQDN policy if available
   in the cluster, else fall back to ipBlock for the panda-proxy ingress IP.

# Panda Integration — Implementation Plan

Concrete plan to wire ethpandaops's hosted panda-proxy into the chat
Hermes agent via the panda CLI and a local panda-server, all inside one
container. Targets the org `ethpandaops`; the per-org overlay mechanism
already on `qu0b/per-org-hermes-builds` carries it.

---

## 1. Summary

For orgs that opt in, the Hermes container becomes a **fat container**:
it carries Hermes + `panda` CLI + `panda-server` + a Docker daemon for
the panda Python sandbox. The container is privileged. Outbound, it
authenticates to the hosted `panda-proxy.analytics.production.platform.ethpandaops.io`
as **a single bot identity per org** — credentials seeded at deploy time
from a SOPS-encrypted `credentials.json`. All chat users in the org
share that identity at the proxy. Per-user attribution stays at the
chat layer.

This is the minimum viable shape. It uses panda as designed (CLI → local
server → hosted proxy). No upstream changes. Re-auth every ~30 days when
the refresh token expires.

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
│  │     ├── .config/panda/credentials/<hash>.json            │   │
│  │     └── panda-storage/  (sandbox outputs, embedding $$)  │   │
│  │                                                          │   │
│  └──────────────────────────────────────────────────────────┘   │
│                              │                                  │
│  ┌──── initContainer: seed-config (existing) ──────────────┐    │
│  │   Copy hermes config.yaml from ConfigMap to PVC         │    │
│  └─────────────────────────────────────────────────────────┘    │
│  ┌──── initContainer: seed-panda-creds (new) ──────────────┐    │
│  │   Decode org-secrets.PANDA_CONFIG_YAML      → /opt/data │    │
│  │          org-secrets.PANDA_CREDENTIALS_JSON → /opt/data │    │
│  │   Idempotent: writes only if absent / newer.            │    │
│  └─────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────┘
                              │
                              │ HTTPS, Bearer <bot's Dex JWT>
                              ▼
                hosted panda-proxy (UNCHANGED)
                              │
                              ▼
                Xatu / Prometheus / Loki / Ethnode
```

**Identity model**: the bot is a real GitHub user (e.g.
`ethpandaops-chat-bot`) added to the ethpandaops org with whatever team
membership is needed to satisfy the proxy's per-datasource
`allowed_orgs`. Auth flow uses panda's existing OIDC-via-Dex
machinery. The proxy validates as it does for any other panda user.

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

Seeded by the new initContainer from `org-secrets.PANDA_CONFIG_YAML`.
Generated once during the bot-setup procedure (§5) so it matches the
credentials file's namespacing. Roughly:

```yaml
server:
  host: "127.0.0.1"
  port: 2480
  base_url: "http://127.0.0.1:2480"
  sandbox_url: "http://172.17.0.1:2480"  # dockerd bridge gateway → pod

sandbox:
  backend: docker
  image: ethpandaops/panda:sandbox-0.24.0
  network: "bridge"
  timeout: 300
  memory_limit: "1g"
  cpu_limit: 1.0

storage:
  base_dir: "/opt/data/panda-storage"

proxy:
  url: "https://panda-proxy.analytics.production.platform.ethpandaops.io"
  auth:
    mode: "oidc"
    issuer_url: "https://dex.primary.production.platform.ethpandaops.io"
    client_id: "panda-proxy"
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
│   ├── config.yaml           # panda-server config (seeded by initContainer)
│   └── credentials/
│       └── <hash>.json       # bot tokens (seeded by initContainer)
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

## 5. One-time setup procedure (per org)

Per org, performed by an operator with: (a) GitHub-org-admin rights on
`ethpandaops`, (b) sops-edit rights on the org's SOPS Secret.

```text
1. CREATE BOT USER
   In GitHub: create user `ethpandaops-<slug>-chat-bot`
   (or similar), enable 2FA, store the password in 1Password.
   Add to `ethpandaops` org with the team(s) whose data the chat
   should access (matching panda-proxy's allowed_orgs entries).

2. RUN panda init + auth login LOCALLY
   On the operator's laptop:

     $ panda init --proxy-url https://panda-proxy.analytics.production.platform.ethpandaops.io
     $ panda auth login --headless
     [device flow URL — the operator opens it AS THE BOT user]

3. EXTRACT THE OUTPUTS

     $ cat ~/.config/panda/config.yaml
     $ ls   ~/.config/panda/credentials/
     <hash>.json
     $ cat ~/.config/panda/credentials/<hash>.json

4. SEED THE SOPS SECRET
   In orgs/<slug>/sopssecrets/org-secrets.sops.yaml, add:
     PANDA_CONFIG_YAML       = contents of step 3's config.yaml
     PANDA_CREDENTIALS_FILE  = the <hash>.json filename verbatim
     PANDA_CREDENTIALS_JSON  = contents of <hash>.json

5. ENABLE THE AGENT
   In orgs/<slug>/values.yaml, set `agents[0].panda.enabled: true`.
   Bump hermes_defaults.persistence.size to 8Gi.
   Bump hermes_defaults.image.tag to a SHA-pinned overlay build that
   has the new Dockerfile (build via build-hermes-agent-orgs.yml).

6. COMMIT + PUSH + WAIT FOR ARGOCD
   Within ~3 minutes the pod rolls. Check:
     kubectl -n org-<slug> logs deploy/<release>-hermes-general -c hermes
     # expect: dockerd-ready, panda-server-ready, hermes-ready

7. SMOKE TEST
   Open the chat at <hostname>. Ask: "what's mainnet's recent finality?"
   Expect: agent shells out to `panda execute`, returns numbers.
```

---

## 6. Refresh-token rotation (~every 30 days)

The Dex-issued refresh token expires every **720h** (`panda-proxy`
production config). When it expires, panda-server starts returning
auth errors and the agent surfaces them.

Rotation playbook:

```text
1. On laptop:  panda auth login --headless   (as the bot user)
2. Replace PANDA_CREDENTIALS_JSON in the SOPS Secret.
3. Commit, push. ArgoCD reconciles. The initContainer overwrites
   /opt/data/.config/panda/credentials/<hash>.json on next pod restart.
4. Force-restart:  kubectl -n org-<slug> rollout restart deploy/<release>-hermes-general
```

Automate later with a CronJob that does the device-flow refresh
using a long-lived bot PAT — not in scope for v1.

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
| `panda execute` → "no server URL configured" | initContainer didn't seed config.yaml | Check `PANDA_CONFIG_YAML` is set in `org-secrets`; restart pod |
| `panda execute` → 401 unauthorized | Refresh token expired | Run §6 rotation playbook |
| `panda execute` → "unsupported sandbox backend" | Sandbox image not pulled / dockerd unhealthy | `kubectl exec … docker ps`; manually `docker pull ethpandaops/panda:sandbox-<ver>` |
| Sandbox container crashes immediately | Sandbox image version mismatch with panda-server | Confirm `sandbox.image` in config.yaml matches a published tag at `ethpandaops/panda:sandbox-*` |
| OOM on Hermes pod | Concurrent sandboxes | Bump `hermes_defaults.resources.limits.memory`; consider `sandbox.max_sessions` |

---

## 9. Security model & blast radius

**Privileged container in the org's namespace**. The fat container has
`privileged: true` (required for dockerd). What this exposes:

- Anyone with `exec` rights into the pod can break out to the node.
  Mitigation: RBAC restricts pod exec to platform admins.
- A compromise of the panda-server process (e.g., RCE in a sandbox
  callback handler) gives shell + dockerd, i.e., root on the node.
  Mitigation: panda-server is a thin, well-reviewed Go binary; the
  attack surface is the sandbox-callback HTTP API.

**NetworkPolicy** (new — add to `charts/org-stack/templates/`):
- Ingress to the Hermes pod: only from the LibreChat pod in the same
  namespace (port 8642).
- Egress from the Hermes pod: only to (a) cluster DNS, (b) the hosted
  panda-proxy hostname, (c) image registries. Block everything else
  (this matters because dockerd-spawned sandbox containers inherit the
  pod's egress and could otherwise call out arbitrarily).

**Bot user scope**: the GitHub bot user is added to teams **only those
necessary for the datasources chat needs**. Don't add it to `Core` or
any admin team. If a team grants admin access to other infrastructure,
do NOT add the bot user to that team.

**Token rotation**: refresh tokens last 30 days. Compromise of the SOPS
Secret discloses a refresh token that can mint Dex access tokens for
~that long. Rotating the bot user's GitHub credentials revokes all its
Dex sessions; rotating the SOPS Secret + restarting the pod replaces
the disclosed token.

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

- **Per-user identity at panda-proxy.** Today: one bot identity per
  org. If a future use case requires per-user RBAC at the proxy, that
  needs upstream changes to panda CLI (token-per-call) + panda-server
  (statelessness) + a way to forward the user's Dex JWT from LibreChat
  through Hermes. Substantial; revisit only when an actual requirement
  surfaces.
- **CronJob refresh automation.** Today: operator runs the §6 playbook
  every ~30 days. A scheduled job that uses a long-lived bot PAT to
  refresh the Dex tokens silently is feasible but adds moving parts.
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

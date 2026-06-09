# Panda bot setup & rotation

Operator runbook for provisioning the per-org GitHub bot user that the
Hermes panda sidecar uses to authenticate against
`panda-proxy.analytics.production.platform.ethpandaops.io`, and for the
~monthly refresh-token rotation. Background, chart wiring, and design
rationale are in [`panda-integration-plan.md`](panda-integration-plan.md);
the in-pod shape is summarised in [`hermes-agent.md`](hermes-agent.md) §10.

Run this once per org when first enabling panda, then re-run the
**rotation** section every ~30 days (or whenever panda-server starts
returning 401s in the pod logs).

## Prerequisites

- GitHub-org-admin rights on `ethpandaops` (to create the bot user and
  manage team membership).
- `sops` installed locally and your age key configured for the target
  org's SOPS Secret (`orgs/<slug>/sopssecrets/org-secrets.sops.yaml`).
- The `panda` CLI binary on your laptop, matching the version pinned in
  the org's overlay (`PANDA_VERSION` in
  `orgs/<slug>/image/Dockerfile`). Install:

  ```bash
  curl -sSfL https://raw.githubusercontent.com/ethpandaops/panda/master/scripts/install.sh | sh
  panda --version
  ```
- Access to the ethpandaops 1Password vault (to store the bot user's
  GitHub password and 2FA recovery codes).
- `kubectl` configured against the chat cluster (for the post-rotation
  verification step).

## One-time setup

### 1. Create the bot GitHub user

In GitHub, register a new account: `ethpandaops-<slug>-chat-bot` (e.g.
`ethpandaops-chat-bot` for the ethpandaops org itself). Use an email
alias the team controls.

1. Enable 2FA via TOTP. Generate recovery codes; paste **all of them**
   into a new 1Password entry under the ethpandaops vault, alongside the
   password and the TOTP seed.
2. Invite the bot to the `ethpandaops` GitHub org.
3. Add the bot to the **minimum set of teams** matching the datasources
   the chat actually needs — i.e. the teams that appear in the
   panda-proxy `allowed_orgs`/team list for each datasource. Do not add
   the bot to admin or write-access teams. If unsure, list the teams the
   proxy currently authorises for each datasource (see the proxy's
   config) and pick the intersection with what the chat must read.

### 2. `panda init` locally as the bot user

On your laptop, sign out of any existing panda session, then initialise
against the hosted proxy as the bot user:

```bash
panda auth logout || true
rm -rf ~/.config/panda

panda init \
  --proxy-url https://panda-proxy.analytics.production.platform.ethpandaops.io \
  --skip-docker \
  --skip-start
```

`--skip-docker` and `--skip-start` skip the local server bootstrap —
you only need the generated config and credentials, not a running
panda-server.

`panda init` writes `~/.config/panda/config.yaml` and, when it reaches
the auth step, opens a browser for GitHub OAuth. **Sign in as the bot
user**, not your personal account. Complete 2FA with the TOTP code.

### 3. Extract the outputs

After `panda init` finishes:

```bash
cat ~/.config/panda/config.yaml
ls   ~/.config/panda/credentials/
# expect a single file: <hash>.json (16-hex-char filename)
cat ~/.config/panda/credentials/*.json
```

Verify the credentials file by running `panda auth status` — it should
report `Authenticated (expires in …)` and show the resolved issuer and
client ID.

### 4. Seed the SOPS Secret

Edit the org's SOPS Secret:

```bash
sops orgs/<slug>/sopssecrets/org-secrets.sops.yaml
```

Add three keys under `stringData`:

| Key | Value |
|---|---|
| `PANDA_CONFIG_YAML` | Verbatim contents of `~/.config/panda/config.yaml` |
| `PANDA_CREDENTIALS_FILE` | The credential filename only, e.g. `a1b2c3d4e5f60718.json` |
| `PANDA_CREDENTIALS_JSON` | Verbatim contents of `~/.config/panda/credentials/<that-file>.json` |

Save; `sops` re-encrypts in place.

### 5. Enable the agent in the org's values

Edit `orgs/<slug>/values.yaml`:

```yaml
agents:
  - name: general
    panda:
      enabled: true

hermes_defaults:
  image:
    repository: git.starflinger.eu/qu0b/hermes-agent-<slug>
    tag: "<base-tag>-<short-sha>"   # the overlay build that ships the fat container
  persistence:
    size: 8Gi                       # bumped from 5Gi for sandbox storage
```

### 6. Commit, push, wait for ArgoCD

```bash
git add orgs/<slug>/
git commit -m "Enable panda sidecar for <slug>"
git push
```

Within ~3 minutes the pod rolls. Watch:

```bash
kubectl -n org-<slug> get pods -w
kubectl -n org-<slug> logs deploy/<release>-hermes-general -c hermes --tail=200
```

Expect, in order: dockerd ready, sandbox image pulled, `panda-server`
listening on `:2480`, Hermes ready.

### 7. Smoke-test from the chat UI

Open `https://<hostname>` and ask the agent a panda-backed question
(e.g. "what is mainnet's recent finality?"). The agent should shell out
to `panda execute` and return numerical results from Xatu.

## Refresh-token rotation (~every 30 days)

The Dex-issued refresh token in `PANDA_CREDENTIALS_JSON` expires every
**720 hours**. Symptom: `panda execute` calls in the pod start returning
401s; `kubectl -n org-<slug> logs deploy/<release>-hermes-general -c hermes`
shows `panda-server` auth errors.

### 1. Re-authenticate locally as the bot user

```bash
panda auth login
```

`panda auth login` resolves the issuer and client ID from your existing
`~/.config/panda/config.yaml`. Over SSH it auto-selects the device-code
flow; for headless terminals pass `--no-browser` explicitly. Complete the
flow signed in as the bot user.

Verify:

```bash
panda auth status
# expect: Authenticated (expires in 720h0m0s)
ls -la ~/.config/panda/credentials/
```

The filename **must match** the `PANDA_CREDENTIALS_FILE` stored in SOPS.
If it changed, the issuer/client/resource tuple changed too — update
both `PANDA_CREDENTIALS_FILE` and `PANDA_CONFIG_YAML` in the next step.

### 2. Replace the credentials in SOPS

```bash
sops orgs/<slug>/sopssecrets/org-secrets.sops.yaml
```

Overwrite `PANDA_CREDENTIALS_JSON` with the new file contents. Save.

### 3. Commit, push, let ArgoCD reconcile

```bash
git add orgs/<slug>/sopssecrets/org-secrets.sops.yaml
git commit -m "Rotate panda credentials for <slug>"
git push
```

ArgoCD reconciles the SopsSecret within ~1 min. The `seed-panda-creds`
initContainer rewrites the credential file on the next pod start, so
force a restart:

### 4. Force-restart the Hermes pod

```bash
kubectl -n org-<slug> rollout restart deploy/<release>-hermes-general
kubectl -n org-<slug> rollout status deploy/<release>-hermes-general
```

## Verifying after rotation

After the pod is `Running` and ready, confirm panda-server can reach the
proxy with the new token by executing a no-op in the sandbox:

```bash
kubectl -n org-<slug> exec deploy/<release>-hermes-general -c hermes -- \
  panda execute --code 'print(1)'
```

Expect `1` on stdout and a zero exit code. A 401 here means the rotation
didn't take — re-check that `PANDA_CREDENTIALS_FILE` matches the actual
file in `~/.config/panda/credentials/` from step 1, and that the pod
actually restarted after the SOPS commit.

For a deeper check that hits the proxy, list datasources:

```bash
kubectl -n org-<slug> exec deploy/<release>-hermes-general -c hermes -- \
  panda datasources
```

## Troubleshooting

Lifted from [`panda-integration-plan.md`](panda-integration-plan.md) §8:

| Symptom | Cause | Fix |
|---|---|---|
| Hermes pod stuck in startupProbe | dockerd failed | `kubectl logs … -c hermes` → grep dockerd.log; common: missing `privileged: true` |
| `panda execute` → "no server URL configured" | initContainer didn't seed config.yaml | Check `PANDA_CONFIG_YAML` is set in `org-secrets`; restart pod |
| `panda execute` → 401 unauthorized | Refresh token expired | Run the rotation playbook above |
| `panda execute` → "unsupported sandbox backend" | Sandbox image not pulled / dockerd unhealthy | `kubectl exec … -- docker ps`; manually `docker pull ethpandaops/panda:sandbox-<ver>` |
| Sandbox container crashes immediately | Sandbox image version mismatch with panda-server | Confirm `sandbox.image` in `PANDA_CONFIG_YAML` matches a published tag at `ethpandaops/panda:sandbox-*` |
| OOM on Hermes pod | Concurrent sandboxes | Bump `hermes_defaults.resources.limits.memory`; consider `sandbox.max_sessions` |

## Security notes

- **Minimal team membership.** The bot user is added to the smallest set
  of `ethpandaops` teams that satisfies the proxy's per-datasource
  `allowed_orgs`. Never add the bot to admin teams or to teams that
  grant write access to infrastructure.
- **Refresh tokens last ~30 days.** Compromise of the SOPS Secret
  discloses a refresh token usable for that long. Rotate per the
  playbook above; rotate the SOPS Secret itself if you suspect leakage.
- **Revocation via GitHub.** Rotating the bot user's GitHub credentials
  (password + 2FA reset) revokes all its Dex sessions immediately; the
  next `panda execute` call fails with 401 until step 2 of the setup is
  re-run.
- **Shared identity at the proxy.** All chat users in the org share the
  bot's identity at panda-proxy. Per-user attribution stays in Hermes
  via `X-Hermes-Session-Key` and in LibreChat's own audit log.
- **Lost 2FA recovery codes = unrecoverable account.** If the bot's TOTP
  seed and recovery codes are both lost, the GitHub account cannot be
  recovered and future rotations are blocked. Mitigation: create a new
  bot user (`ethpandaops-<slug>-chat-bot-v2`), add to the same teams,
  and re-run the One-time setup. Always store the recovery codes in
  1Password alongside the password the moment they're generated.

See [`panda-integration-plan.md`](panda-integration-plan.md) §9 for the
full security model (privileged-container blast radius,
NetworkPolicy egress, and bot-user scoping).

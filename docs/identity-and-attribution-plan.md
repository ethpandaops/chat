# Identity & Attribution Plan — bot identity at the proxy, per-user attribution at the chat layer

Supersedes §6 (refresh-token rotation) and §11's "per-user identity at
panda-proxy" sketch in [`panda-integration-plan.md`](panda-integration-plan.md).
Everything else in that plan (fat container, sandbox, chart shape) stands.

---

## 1. Decision

The data behind panda-proxy is read-only org data with **team-level** ACLs
(`panda-integration-plan.md` §2). Per-user identity at the proxy is therefore
ceremony: Hermes sits mid-chain and would hold every user's token anyway, so
"per-user at the proxy" adds plumbing, not security. We commit to:

1. **Authentication to panda-proxy: one bot identity per deployment**, an
   Authentik *service account* using the `client_credentials` grant — no
   refresh tokens, no 30-day rotation playbook, no GitHub bot user.
2. **Attribution: per-user at the Hermes/Langfuse layer.** Every gateway can
   produce a stable user key (CF email on web, Telegram user id, session key);
   the chat layer stamps it on traces and (optionally) forwards it to the
   proxy's audit log as a plain header.
3. **Access gating: per-gateway, at the gateway's natural edge.** Cloudflare
   Access (GitHub org policy) for the web UI; an adapter-level allowlist or
   account-link check for future gateways (Telegram, Discord, ...).

### Explicitly rejected

- **CF-JWT passthrough to the proxy** (forwarding `Cf-Access-Jwt-Assertion`
  as the proxy bearer). Verified feasible — the team domain serves
  `/.well-known/openid-configuration`, so the proxy's existing go-oidc
  multi-issuer verifier would accept it with config only — but it couples
  identity to the *transport*: a Telegram gateway has no CF edge and no
  GitHub identity, so every new gateway would need a bespoke trust path into
  the proxy. It also delivers the weakest identity (email only, no `groups`
  claim → no team-tier authorization).
- **Per-user Authentik credentials in panda-server** (device-flow account
  linking). Gateway-agnostic and gives real `groups`, but it's a credential
  vault + linking UX + monthly re-link per user — unjustified while no
  datasource distinguishes *users* rather than *teams*. Revisit only if that
  changes; the device-flow machinery (Authentik `default-device-code-flow`,
  `panda auth login --headless`) already exists if it does.

---

## 2. Target architecture

```
web user ── CF Access (GitHub org policy) ──▶ open-webui-cf
                                                 │  trusted-header SSO (email)
                                                 │  Cf-Access-Jwt-Assertion → attribution only
                                                 ▼
tg user ──── Telegram adapter (allowlist) ──▶ Hermes gateway
                                                 │  user key: cf:<email> / tg:<id>
                                                 │  → Langfuse user_id, session key
                                                 ▼
                                  panda CLI ──▶ panda-server
                                                 │  Authorization: Bearer <access token>
                                                 │  minted on demand via client_credentials
                                                 │  (Authentik service account, app password)
                                                 │  X-Panda-On-Behalf-Of: <user key>  (audit only)
                                                 ▼
                                            panda-proxy (config unchanged)
```

The proxy keeps exactly its current trust model: Authentik-issued tokens,
audience `panda-proxy`, groups-based datasource authorization. The chat bot
lands in the external tier (no org-mirror groups), which is all the devnet
chat needs (clickhouse-refined external variant, xatu-experimental, devnets
prometheus — none require `allowed_orgs`).

---

## 3. Workstream A — bot identity via `client_credentials`

### A1. Authentik blueprint (platform repo)

`environments/staging/applications/authentik/templates/blueprints-configmap.yaml`
already contains the exact pattern for CI (`panda-ci` group + `panda-ci-svc`
service account + non-expiring app-password token, panda-proxy-provider.yaml
entries). Clone it for chat:

- Group `panda-chat` — bound to the `panda-proxy` application as a third
  access-gate policy binding (engine mode is "any"). **No org-mirror groups**:
  the bot must not reach `ethpandaops:Core`-gated variants or the `platform`
  prometheus.
- Service account `panda-chat-svc`, member of `panda-chat`.
- Token `panda-chat-svc-token`, `intent: app_password`, `expiring: false`.

Post-deploy, an operator retrieves the token value from Authentik and puts it
in the devnet SOPS (`services.enc.yaml#chat.panda_bot_token`). One identity
for all devnet chats in v1; if per-devnet audit separation is wanted later,
adding `panda-chat-<network>-svc` accounts is three blueprint lines each.

Staging first, then `./promote.sh application authentik staging production`.

### A2. panda upstream — `client_credentials` auth mode (the one real change)

`pkg/auth/client/client.go` supports `authorization_code` / `refresh_token` /
`device_code`; add the `client_credentials` grant. Config shape
(consumed by panda-server via `proxy.auth`):

```yaml
proxy:
  url: "https://panda-proxy.analytics.production.platform.ethpandaops.io"
  auth:
    mode: "client_credentials"
    issuer_url: "https://authentik.analytics.production.platform.ethpandaops.io/application/o/panda-proxy/"
    client_id: "panda-proxy"
    username: "panda-chat-svc"          # service account
    password: "${PANDA_BOT_TOKEN}"      # app password, from env
```

Behavior: POST the issuer's token endpoint with
`grant_type=client_credentials&client_id=panda-proxy&username=...&password=...`
(Authentik's service-account form), cache the access token, re-mint on expiry
(Authentik validity 1h). No credential files, no refresh tokens, no state on
the PVC. `proxySvc.RegisterToken()` (`pkg/server/api.go:764`) returns the
cached/fresh token; everything downstream is untouched.

Acceptance: a panda-server with only `username`+`password` env (no
`~/.config/panda/credentials/`) serves proxy-backed queries indefinitely,
across token expiry boundaries, surviving restarts with zero seeded state.

### A3. panda-chat chart (ethereum-helm-charts)

- `values.yaml`: replace `credentials.panda.credentialsJson` /
  `credentialsFile` with `credentials.panda.botUsername` / `botToken`;
  `panda.issuerUrl` switches default from Dex to the Authentik issuer.
- `templates/secret.yaml`: materialize `PANDA_BOT_USERNAME` /
  `PANDA_BOT_TOKEN`.
- `templates/configmap.yaml` (panda-config.yaml): the `proxy.auth` block from
  A2, referencing the env vars.
- `templates/deployment.yaml`: delete the `seed-panda-creds` initContainer
  and the credentials copy in the image entrypoint
  (`images/hermes-agent-panda/entrypoint.sh:25-27`).

### A4. Ansible (ansible-collection-general + bal-devnets)

- `roles/generate_kubernetes_config/templates/chat.yaml.j2`: swap the
  `credentials.panda.*` AVP paths to
  `{.panda_bot_username}` / `{.panda_bot_token}`.
- `services.enc.yaml#chat` per devnet: add `panda_bot_username`,
  `panda_bot_token`; drop `panda_credentials_json` / `panda_credentials_file`.
- Role defaults comment block (lines 94-98) updated to the new key list.

### A5. Platform repo follow-ups (independent, opportunistic)

- The panda-proxy values comment block already documents Authentik
  client_credentials for CI — no proxy change needed for chat.
- Separately: remove the legacy Dex issuer from
  `applications/panda-proxy/values.yaml` once humans have re-run
  `panda init` (pre-existing TODO, not a dependency of this plan).

---

## 4. Workstream B — per-user attribution at the chat layer

### B1. Web identity capture (Hermes gateway, api_server platform)

`open-webui-cf` already forwards `Cf-Access-Jwt-Assertion` on model calls,
and the api_server platform already supports `X-Hermes-Session-Id` /
`X-Hermes-Session-Key`. Add: when `Cf-Access-Jwt-Assertion` is present,
decode the payload (no signature verification needed — the only network path
to Hermes is OW inside the namespace, enforced by NetworkPolicy; CF verified
it at the edge) and derive `user_key = cf:<email>`. Absent the header, fall
back to the session key as today.

### B2. Langfuse user attribution

Stamp `user_id = user_key` on traces in the langfuse observability plugin so
per-user cost/usage attribution works in the existing
`langfuse.analytics.production` project. (This is the "per-user attribution"
half of what open-webui-cf was built for; the "per-user auth" half is
retired by this plan.)

### B3. Proxy audit attribution (cheap, optional)

panda-server forwards all incoming headers to the proxy except
`Authorization` (`pkg/server/api.go:757-762`) — so the chain
Hermes → panda CLI → panda-server can carry `X-Panda-On-Behalf-Of: <user_key>`
to the proxy *today* with no server change; the panda skill just sets it (or
an env the CLI maps to a header). Proxy side: log the header in
`pkg/proxy/auditor.go` as untrusted free-text attribution alongside the
authenticated bot identity. Never use it for authorization.

---

## 5. Workstream C — per-gateway access gating

The contract every gateway adapter must satisfy before reaching the agent:

| Gateway | Admission (who gets in) | User key (attribution) |
|---|---|---|
| Web (Open-WebUI) | CF Access app on `chat.<network>` with GitHub-org policy; trusted-header SSO (`Cf-Access-Authenticated-User-Email`) | `cf:<email>` |
| Telegram (future) | Adapter allowlist of Telegram user ids, or a one-time `/link` device-flow against Authentik if it ever needs GitHub-verified admission | `tg:<user_id>` |
| Anything else | Adapter's own edge — never the proxy's problem | `<gw>:<stable id>` |

Web is already wired (`gen_kubernetes_config_chat_auth_enabled: true` flips
trusted-header SSO; the CF Access app per devnet is the documented prereq in
`bal-devnets .../devnet-7/group_vars/all/chat.yaml`). Telegram lands whenever
the gateway does — nothing in this plan blocks on it, which is the point.

---

## 6. Phasing

Each phase independently shippable and reversible.

- [x] **Phase 1 — panda upstream** (ethpandaops/panda): A2
  `client_credentials` mode + tests. Gate for everything else.
  *Done on `qu0b/panda-client-credentials` (incl. the B3
  `PANDA_ON_BEHALF_OF` → header mapping in the CLI); awaiting release.*
- [x] **Phase 2 — Authentik blueprint** (platform repo, staging → soak →
  promote): A1. Verify by minting a token manually with the app password and
  curling the staging proxy.
  *Blueprint done on `qu0b/authentik-panda-chat-svc` (staging only);
  manual verification + `./promote.sh application authentik staging
  production` still pending.*
- [x] **Phase 3 — chart + image** (ethereum-helm-charts + chat repo): A3 +
  entrypoint cleanup; bump `hermes-agent-panda` to the Phase-1 panda release.
  *Chart 0.2.0 on `qu0b/panda-chat-client-credentials`; entrypoint cleanup
  on `qu0b/chat-service-account`. The `PANDA_VERSION` bump is a marked TODO
  until a panda release ships Phase 1.*
- [x] **Phase 4 — ansible + SOPS** (ansible-collection-general + bal-devnets):
  A4; roll one devnet (bal-devnet-7 pilot), smoke-test a panda query in chat;
  then delete the old `panda_credentials_*` SOPS keys.
  *Role done on `qu0b/chat-panda-bot-token`; the bal-devnets SOPS edits
  (write `chat.panda_bot_username`/`panda_bot_token`, drop
  `panda_credentials_*`) are a manual operator step.*
- [x] **Phase 5 — attribution** (hermes-agent + chat repo): B1 + B2, optionally
  B3. Independent of phases 2-4; can land in parallel with Phase 1.
  *Done on hermes-agent `qu0b/cf-user-attribution` (user_key derivation +
  Langfuse user_id) and chat `qu0b/chat-service-account` (panda skill sets
  `PANDA_ON_BEHALF_OF`). B3's optional proxy-side audit logging stays
  deferred.*
- [x] **Phase 6 — docs**: mark `panda-bot-setup.md` (GitHub bot user + laptop
  device flow + 30-day rotation) obsolete and replace with the service-account
  procedure; annotate `panda-integration-plan.md` §6/§11 as superseded by
  this document.

Rollback at any phase is `git revert` of that phase's commits. (Deviation
from the original draft: the old seeded-credentials path was **removed
outright** rather than kept warm through Phase 4 — no `credentialsJson` /
`credentialsFile` values, no `seed-panda-creds` initContainer, no entrypoint
credentials copy, no compat flags. See Open Questions.)

---

## 7. Security notes

- **App password is non-expiring.** Compromise of the SOPS value grants
  bot-tier (external, read-only) proxy access until the token is revoked in
  Authentik — one click, immediate, vs. the old world where a leaked refresh
  token was valid up to 30 days and revocation meant rotating a GitHub bot
  user. Strictly better.
- **Blast radius of the bot identity** is bounded by its groups: `panda-chat`
  only → no internal ClickHouse tier, no platform prometheus. Enforced by the
  blueprint, testable by asserting the proxy denies `internal.*` queries for
  a bot token.
- **Attribution headers are untrusted.** `X-Panda-On-Behalf-Of` is free-text
  from inside the pod; it must never appear in an authorization decision.
- **NetworkPolicy story from `panda-integration-plan.md` §9 still applies**
  unchanged (it is what makes B1's "decode without verifying" sound).

## 8. Open questions

1. One shared `panda-chat-svc` vs per-devnet service accounts (audit
   granularity vs blueprint noise). Default: shared, revisit when a second
   non-devnet deployment appears.
2. Should the Authentik token endpoint path be `panda init`-discoverable for
   service accounts (extend `/auth/metadata`), or is static chart config
   enough? Default: static config; metadata discovery is a human-flow nicety.
3. B3 proxy-side audit logging of the on-behalf-of header: worth the small
   proxy PR, or is Langfuse attribution sufficient? Default: defer until
   someone actually asks "which user ran this query" at the proxy.

### Implementation deviations (recorded as built)

- **No rollback shim.** The original §6 kept the seeded-credentials path
  alive through Phase 3-4; implementation deleted it in the same change
  (chart values, initContainer, entrypoint copy, ansible AVP paths).
  Rollback is `git revert` per repo.
- **B3 landed as a tiny panda CLI change**, not a skill-only hack: the CLI
  maps env `PANDA_ON_BEHALF_OF` → header `X-Panda-On-Behalf-Of` (the
  "env the CLI maps to a header" option in §4 B3), and the api_server
  surfaces `user_key` to skills via a `<user_attribution>` system-prompt
  block. panda-server and panda-proxy are unchanged, as required.
- **Ordering dependency left as a marked TODO**: the chat image's
  `PANDA_VERSION` stays at 0.31.0 (latest release, no client_credentials)
  with a do-not-roll warning until a panda release carries Phase 1.
- **Chart fails fast**: `credentials.panda.botUsername`/`botToken` are
  `required` when `panda.enabled`, so a missing SOPS value breaks at
  `helm template` time instead of producing a half-authenticated pod.

# Panda bot setup (Authentik service account)

Operator runbook for provisioning the bot identity the Hermes panda
sidecar uses to authenticate against
`panda-proxy.analytics.production.platform.ethpandaops.io`.

The identity is an **Authentik service account** (`panda-chat-svc`) with a
**non-expiring app password**. panda-server mints short-lived proxy access
tokens from it on demand via the OAuth2 `client_credentials` grant and
keeps them in memory only. There is **no GitHub bot user, no `panda auth
login`, no credential files, and no 30-day rotation** — the previous
procedure based on those is obsolete (superseded by
[`identity-and-attribution-plan.md`](identity-and-attribution-plan.md)).

Run the one-time setup once per environment; afterwards the only
recurring operation is revocation/rotation *if the app password leaks*.

## Prerequisites

- The Authentik blueprint that defines the service account is deployed
  (platform repo,
  `environments/<env>/applications/authentik/templates/blueprints-configmap.yaml`
  — `panda-chat` group, `panda-chat-svc` user, `panda-chat-svc-token`
  app-password token, and the `panda-chat` policy binding on the
  `panda-proxy` application).
- Admin access to the Authentik UI
  (`authentik.analytics.<env>.platform.ethpandaops.io`).
- `sops` configured for the bal-devnets secrets (`services.enc.yaml`).

## One-time setup

### 1. Retrieve the app password from Authentik

In the Authentik admin UI: **Directory → Tokens and App passwords** →
`panda-chat-svc-token` → copy the key. (The token is created by the
blueprint with `intent: app_password`, `expiring: false`.)

Sanity-check it mints a proxy-accepted token:

```bash
ISSUER=https://authentik.analytics.production.platform.ethpandaops.io/application/o/panda-proxy/
TOKEN=$(curl -sf "${ISSUER}token/" \
  -d grant_type=client_credentials \
  -d client_id=panda-proxy \
  -d username=panda-chat-svc \
  -d "password=${APP_PASSWORD}" | jq -r .access_token)

curl -sf -H "Authorization: Bearer ${TOKEN}" \
  https://panda-proxy.analytics.production.platform.ethpandaops.io/datasources
```

Expect a datasource list containing only external-tier entries (the bot
is in the `panda-chat` group only — no org-mirror groups, so no
`ethpandaops:Core`-gated variants and no platform prometheus).

### 2. Seed the devnet SOPS secret

Per devnet, under `services.enc.yaml#chat`:

```bash
sops <path-to>/services.enc.yaml
```

```yaml
chat:
  panda_bot_username: panda-chat-svc
  panda_bot_token: <the app password from step 1>
```

The ansible `generate_kubernetes_config` role maps these to the
panda-chat chart values `credentials.panda.botUsername` /
`credentials.panda.botToken`, which materialize as `PANDA_BOT_USERNAME` /
`PANDA_BOT_TOKEN` in the pod.

### 3. Roll and smoke-test

Re-run the devnet's kubernetes config generation, let ArgoCD sync, then
ask the chat a panda-backed question (or check directly):

```bash
kubectl -n <ns> exec deploy/<release>-hermes -c hermes -- \
  panda datasources
```

A 401 here means the token or username in SOPS is wrong, or the
Authentik blueprint isn't deployed in that environment.

## Revocation / rotation (only on suspected leak)

The app password does not expire; rotation is event-driven, not
scheduled.

1. In Authentik: delete (or recreate) the `panda-chat-svc-token` app
   password. Revocation is immediate — the next token mint fails.
2. Create a replacement token for `panda-chat-svc`
   (intent *app password*, non-expiring), or re-apply the blueprint.
3. Update `services.enc.yaml#chat.panda_bot_token` in each devnet and
   re-roll the pods.

Blast radius of a leaked app password: read-only, external-tier proxy
access (`panda-chat` group only) until revoked.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| panda-server logs `minting client_credentials token ... 401` | Wrong app password / username, or token revoked | Re-check step 1; re-seed SOPS |
| Token mints but `/datasources` is 403 | `panda-chat` group binding missing on the panda-proxy application | Verify the blueprint applied (Authentik → Applications → Panda Proxy → Policy bindings) |
| `proxies[0].auth.password is required for mode client_credentials` at startup | `PANDA_BOT_TOKEN` env empty | Secret not populated — check the chart values / AVP path |
| Bot sees internal datasources | Bot was added to org-mirror groups | Remove `panda-chat-svc` from everything except `panda-chat` |

---
name: faucet
description: "Fund an account on this Ethereum devnet using the devnet's PoW faucet (powfaucet)."
version: 0.1.0
platforms: [linux]
metadata:
  hermes:
    tags: [ethereum, devnet, faucet, funding, powfaucet, testnet, eth, fund, drip]
---

# faucet — fund an account on this devnet

Use this skill when the user wants **test ETH / funds on this devnet**, e.g.
"fund 0x… ", "I need testnet ETH", "top up my account", "send me some ETH on
the devnet".

The faucet is **powfaucet**, reachable at the URL in the `DEVNET_FAUCET_URL`
environment variable (e.g. `https://faucet.bal-devnet-7.ethpandaops.io`). It is
network `DEVNET_NETWORK`.

## What you need from the user

1. A destination **address** (`0x` + 40 hex). If they didn't give one, ask.
   Never invent one. Validate the format before proceeding.

## How powfaucet works

powfaucet gates drips behind a proof-of-work mining session (and sometimes a
login module) to limit abuse. There is a small HTTP API; the typical happy path:

```bash
BASE="${DEVNET_FAUCET_URL%/}"

# 1. Read faucet config (max drop, min/max amounts, modules in effect).
curl -fsS "$BASE/api/getFaucetConfig" | jq '{name:.faucetTitle, minClaim, maxClaim, modules:(.modules|keys)}'

# 2. Start a session for the target address.
SESSION=$(curl -fsS "$BASE/api/startSession" \
  --data-urlencode "addr=<ADDRESS>" | jq -r '.session // empty')
```

- If `startSession` returns a `session` and a `status` of `claimable`, claim it:
  ```bash
  curl -fsS "$BASE/api/claimReward" --data-urlencode "session=$SESSION" | jq .
  ```
  Report the returned transaction hash and the amount.
- If the response indicates a **PoW / mining** or **captcha / login** challenge
  is required (status `running` with a `powParams`/`failedReason` mentioning
  pow, or a module like `pow`/`captcha`/`auth`), you **cannot** complete that
  from here. Give the user the direct link instead:
  ```
  Open ${DEVNET_FAUCET_URL}, paste your address (<ADDRESS>), complete the
  mining/verification step, and claim. It usually takes under a minute.
  ```

Always state the network (`DEVNET_NETWORK`) so the user knows these funds are
devnet-only and have no value.

## What NOT to do
- Never fund an address the user didn't explicitly provide.
- Never claim to have sent funds unless `claimReward` returned a tx hash —
  report exactly what the API returned, including failures.
- Never imply devnet ETH is worth anything.

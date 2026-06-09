---
name: join-devnet
description: "Help a user join this Ethereum devnet: hand them enodes/bootnodes, genesis + chain config, and a ready-to-run client command."
version: 0.1.0
platforms: [linux]
metadata:
  hermes:
    tags: [ethereum, devnet, join, enode, bootnode, enr, genesis, chainspec, config, node, peering]
---

# join-devnet — connect a node to this devnet

Use this skill when the user wants to **run a node on / join / sync / peer with
this devnet**, e.g. "how do I join", "give me the bootnodes", "what's the
genesis", "how do I run geth/lighthouse here", "chain id?".

Everything is published by the devnet's `config` service at the URL in the
`DEVNET_CONFIG_URL` environment variable
(e.g. `https://config.bal-devnet-7.ethpandaops.io`). Network is
`DEVNET_NETWORK`; chain id is `DEVNET_CHAIN_ID`; public RPC is `DEVNET_RPC_URL`;
explorer is `DEVNET_EXPLORER_URL`.

## Fetch the artifacts the user needs

```bash
CFG="${DEVNET_CONFIG_URL%/}"

# Execution layer
curl -fsS "$CFG/el/enodes.txt"        # EL enodes (static peers)
curl -fsS "$CFG/el/genesis.json"      # EL genesis (geth/reth/nethermind init)
curl -fsS "$CFG/el/chainspec.json"    # besu/nethermind chainspec

# Consensus layer
curl -fsS "$CFG/cl/bootstrap_nodes.txt"  # CL bootnodes (ENRs)
curl -fsS "$CFG/cl/config.yaml"          # CL chain config
curl -fsS "$CFG/cl/genesis.ssz"          # CL genesis state (binary)
curl -fsS "$CFG/cl/deposit_contract.txt" # deposit contract address
```

Use the bash tool to fetch these, then present the concrete values (don't just
restate the URLs). For binary artifacts (`genesis.ssz`) give the URL, not the bytes.

## Produce a ready-to-run recipe

Give a copy-pasteable command for the client the user names. Example (geth + a
CL client), substituting the fetched values and `DEVNET_CHAIN_ID`:

```bash
# Execution (geth)
curl -fsSL "$CFG/el/genesis.json" -o genesis.json
geth init --datadir ./data genesis.json
geth --datadir ./data --networkid <DEVNET_CHAIN_ID> \
  --bootnodes "$(curl -fsS "$CFG/el/enodes.txt" | paste -sd, -)" \
  --http --http.api eth,net,web3,engine --authrpc.jwtsecret ./jwt.hex
```

For the consensus client, point `--genesis-state` at `cl/genesis.ssz`, the
config at `cl/config.yaml`, and `--bootnodes` at the ENRs in
`cl/bootstrap_nodes.txt`. Adapt flags to the client the user asked for
(lighthouse / prysm / teku / nimbus / lodestar / grandine).

Mention the public RPC (`DEVNET_RPC_URL`) and explorer (`DEVNET_EXPLORER_URL`)
as a quick alternative if they just want to interact, not run a node.

## What NOT to do
- Never hard-code enodes/genesis from memory — always fetch live from `DEVNET_CONFIG_URL`.
- Don't dump binary `genesis.ssz` contents into chat; link it.
- If a `config` endpoint 404s, say so and list what you could fetch.

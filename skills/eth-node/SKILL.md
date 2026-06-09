---
name: eth-node
description: "Query the devnet's execution and consensus nodes directly via JSON-RPC and beacon REST API using DEVNET_RPC_URL and DEVNET_CL_URL."
version: 0.1.0
platforms: [linux]
metadata:
  hermes:
    tags: [ethereum, devnet, rpc, beacon, block, slot, finality, sync, validators, eth-node]
---

# eth-node — direct EL/CL node queries for this devnet

Use this skill for **real-time, low-latency devnet state** questions:
current block, slot, sync status, head finality, peer counts, validator
queue, fork choice, single-block details. This skill hits the nodes directly
via HTTP; no panda-server or sandbox needed.

## Environment

| Variable | Value | Purpose |
|---|---|---|
| `DEVNET_RPC_URL` | e.g. `http://el-1-geth-lighthouse:8545` | EL JSON-RPC |
| `DEVNET_CL_URL` | e.g. `http://cl-1-lighthouse-geth:4000` | CL beacon REST |
| `DEVNET_NETWORK` | e.g. `kurtosis` | Network name |

## Execution pattern

Always use `execute_code` with Python — do NOT use the terminal for these
queries (the security scanner prompts for HTTP URLs in shell, but Python
requests are pre-approved for known devnet endpoints).

```python
import os, requests

rpc = os.environ.get("DEVNET_RPC_URL", "http://localhost:8545")
cl  = os.environ.get("DEVNET_CL_URL",  "http://localhost:4000")

def rpc_call(method, params=None):
    r = requests.post(rpc, json={"jsonrpc":"2.0","method":method,"params":params or [],"id":1}, timeout=5)
    r.raise_for_status()
    return r.json().get("result")

def beacon_get(path):
    r = requests.get(f"{cl}{path}", headers={"Accept":"application/json"}, timeout=5)
    r.raise_for_status()
    return r.json().get("data")
```

## Common queries

### Current block and slot
```python
block_hex = rpc_call("eth_blockNumber")
block_num  = int(block_hex, 16)
print("EL head block:", block_num)

head = beacon_get("/eth/v1/beacon/headers/head")
print("CL head slot:", head["header"]["message"]["slot"])
```

### Sync status
```python
sync = rpc_call("eth_syncing")
print("EL syncing:", sync)   # False = in sync

cl_sync = beacon_get("/eth/v1/node/syncing")
print("CL syncing:", cl_sync)
```

### Finality checkpoints
```python
fin = beacon_get("/eth/v1/beacon/states/head/finality_checkpoints")
print("Justified epoch:", fin["current_justified"]["epoch"])
print("Finalized epoch:", fin["finalized"]["epoch"])
```

### Peer counts
```python
el_peers = rpc_call("net_peerCount")
print("EL peers:", int(el_peers, 16))

cl_peers = beacon_get("/eth/v1/node/peer_count")
print("CL peers connected:", cl_peers["connected"])
```

### Block details
```python
block = rpc_call("eth_getBlockByNumber", ["latest", False])
print("Hash:", block["hash"])
print("Gas used:", int(block["gasUsed"], 16))
print("Tx count:", len(block["transactions"]))
```

### Validator count
```python
validators = beacon_get("/eth/v1/beacon/states/head/validators?status=active_ongoing")
print("Active validators:", len(validators))
```

## When to use panda instead

Use the `panda` skill (not this one) when the user asks for:
- **Historical / aggregated data** (e.g. block production over the last 24 h)
- **Cross-validator analysis** (attestation performance, missed proposals)
- **Xatu / ClickHouse** queries
- **Prometheus metrics** (CPU, memory, network)
- **Loki logs**

panda has much richer data once the network has been running long enough for
data to flow into the analytics stack. For a brand-new network use this skill
until panda has data.

## Error handling

| Error | Likely cause | Fix |
|---|---|---|
| `Connection refused` | Node not started yet | Wait and retry |
| `timeout` | Node overloaded or restarting | Retry after a few seconds |
| `KeyError: result` | RPC error response | Print full `r.json()` to see the error message |
| `404` on beacon path | Wrong path or old CL version | Check `/eth/v1/node/version` first |

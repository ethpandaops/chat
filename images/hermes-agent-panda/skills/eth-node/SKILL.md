---
name: eth-node
description: "Query the devnet's execution and consensus nodes directly via JSON-RPC, beacon REST API, and Dora explorer API using DEVNET_RPC_URL, DEVNET_CL_URL, and DEVNET_DORA_URL."
version: 0.2.0
platforms: [linux]
required_environment_variables:
  - DEVNET_RPC_URL
  - DEVNET_CL_URL
  - DEVNET_NETWORK
  - DEVNET_CHAIN_ID
  - DEVNET_DORA_URL
  - DEVNET_GRAFANA_URL
  - DEVNET_PROMETHEUS_URL
metadata:
  hermes:
    tags: [ethereum, devnet, rpc, beacon, block, slot, finality, sync, validators, dora, eth-node]
---

# eth-node — direct EL/CL/Dora queries for this devnet

Use this skill for **real-time devnet state** questions: current block, slot,
sync status, head finality, peer counts, validator queue, block details, Dora
explorer data. Hits nodes and explorers directly via HTTP — no panda-server needed.

## Environment

| Variable | Purpose |
|---|---|
| `DEVNET_RPC_URL` | EL JSON-RPC (e.g. `http://el-1-geth-lighthouse:8545`) |
| `DEVNET_CL_URL` | CL beacon REST (e.g. `http://cl-1-lighthouse-geth:4000`) |
| `DEVNET_DORA_URL` | Dora explorer API (e.g. `http://dora:8080`) — empty if not running |
| `DEVNET_GRAFANA_URL` | Grafana — empty if not running |
| `DEVNET_PROMETHEUS_URL` | Prometheus — empty if not running |
| `DEVNET_NETWORK` | Network name (e.g. `kurtosis`) |

## Execution pattern

Always use `execute_code` with Python — do NOT use the terminal for HTTP queries
(the security scanner prompts for HTTP URLs in shell).

```python
import os, requests

rpc  = os.environ.get("DEVNET_RPC_URL", "http://localhost:8545")
cl   = os.environ.get("DEVNET_CL_URL",  "http://localhost:4000")
dora = os.environ.get("DEVNET_DORA_URL", "")

def rpc_call(method, params=None):
    r = requests.post(rpc, json={"jsonrpc":"2.0","method":method,"params":params or [],"id":1}, timeout=5)
    r.raise_for_status()
    return r.json().get("result")

def beacon_get(path):
    r = requests.get(f"{cl}{path}", headers={"Accept":"application/json"}, timeout=5)
    r.raise_for_status()
    return r.json().get("data")

def dora_get(path):
    if not dora:
        return None
    r = requests.get(f"{dora}{path}", headers={"Accept":"application/json"}, timeout=5)
    r.raise_for_status()
    return r.json()
```

## EL / CL queries

### Current block and slot
```python
block_num = int(rpc_call("eth_blockNumber"), 16)
print("EL head block:", block_num)

head = beacon_get("/eth/v1/beacon/headers/head")
print("CL head slot:", head["header"]["message"]["slot"])
```

### Sync status
```python
print("EL syncing:", rpc_call("eth_syncing"))   # False = in sync
print("CL syncing:", beacon_get("/eth/v1/node/syncing"))
```

### Finality checkpoints
```python
fin = beacon_get("/eth/v1/beacon/states/head/finality_checkpoints")
print("Justified epoch:", fin["current_justified"]["epoch"])
print("Finalized epoch:", fin["finalized"]["epoch"])
```

### Peer counts
```python
print("EL peers:", int(rpc_call("net_peerCount"), 16))
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

### Active validators
```python
validators = beacon_get("/eth/v1/beacon/states/head/validators?status=active_ongoing")
print("Active validators:", len(validators))
```

## Dora explorer queries

Check `DEVNET_DORA_URL` is set before using Dora. Dora exposes a REST API at `/api/v1/`.

### Epoch summary
```python
data = dora_get("/api/v1/epoch/latest")
if data:
    print("Latest epoch:", data)
```

### Recent slots / blocks
```python
data = dora_get("/api/v1/slots?limit=10")
if data:
    for slot in data.get("data", []):
        print(slot)
```

### Validator list
```python
data = dora_get("/api/v1/validators?limit=20")
if data:
    for v in data.get("data", []):
        print(v["index"], v["status"])
```

### Discover Dora API routes
If unsure of the path, fetch the Dora home page or try `/api/v1/` to see available endpoints.

## When to use panda instead

Use the `panda` skill when the user asks for:
- **Historical / aggregated data** (block production over time, missed proposals)
- **Xatu / ClickHouse** queries
- **Prometheus metrics** (CPU, memory, network) — or query `DEVNET_PROMETHEUS_URL` directly
- **Loki logs**

panda has much richer data once the network has been running long enough for data
to flow into the analytics stack. For a brand-new network use this skill.

## Error handling

| Error | Likely cause | Fix |
|---|---|---|
| `Connection refused` | Node not started yet | Wait and retry |
| `timeout` | Node overloaded | Retry after a few seconds |
| `KeyError: result` | RPC error | Print full `r.json()` to inspect |
| `404` on beacon path | Wrong path | Check `/eth/v1/node/version` first |
| `dora_get` returns `None` | Dora not running | Tell user Dora is not in this enclave |

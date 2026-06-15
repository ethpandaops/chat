---
name: panda
description: "Query Ethereum devnet analytics (Xatu ClickHouse chain events, container logs in ClickHouse otel_logs, Prometheus, beacon/exec nodes) by running Python in a sandboxed environment via the panda CLI. Scoped to this devnet."
version: 0.4.0
platforms: [linux]
metadata:
  hermes:
    tags: [ethereum, devnet, analytics, xatu, clickhouse, prometheus, logs, otel, beacon, validators, slots, finality, mev, panda]
---

# panda — Ethereum analytics for this devnet

Use this skill whenever the user asks about **this devnet's state, validator
behaviour, MEV, beacon chain finality, block production, block-level access
lists, network metrics, client logs, or any data sourced from Xatu ClickHouse,
Prometheus, or beacon/exec nodes**.

`panda` is a CLI in the container that talks to a local panda-server, which runs
sandboxed Python with libraries for ClickHouse (Xatu chain events + container
logs), Prometheus, Dora, and Ethnode. **You write Python; the server executes it;
you read the output.**

## Always scope to this devnet

This chat serves a single devnet; **its name is stated in your system prompt**
(e.g. `bal-devnet-7`). The sandbox has **no environment variables** — do NOT read
`os.environ` for it. Pass the name as a literal/parameter in your query. Xatu
holds many networks, so **filter every Xatu query by `meta_network_name`**, e.g.
`WHERE meta_network_name = {net:String}` with `{"net": "bal-devnet-7"}`. If the
user asks about another network, tell them this chat only covers this one.

## Attribute queries to the user

If your system prompt contains a `<user_attribution>` block with a `user_key`,
prefix **every** `panda` command with `PANDA_ON_BEHALF_OF='<user_key>'`, e.g.

```bash
PANDA_ON_BEHALF_OF='cf:someone@example.com' panda execute --code '...'
```

This rides along as an audit header (`X-Panda-On-Behalf-Of`) so downstream
logs can tell which human asked. It is attribution only — it grants nothing,
so never skip a query because the key is missing; just omit the prefix.

## The Python API — never guess it

The sandbox exposes the **`ethpandaops`** package. Its modules (run
`panda docs` to list, `panda docs <module>` for signatures):
`clickhouse`, `dora`, `ethnode`, `prometheus`, `forky`, `tracoor`,
`block_archive`, `cbt`, `benchmarkoor`, `storage`. There is **no** `xatu`
module and **no** top-level `clickhouse`/`beacon` — always
`from ethpandaops import <module>`.

ClickHouse is the workhorse:

```python
from ethpandaops import clickhouse
clickhouse.list_datasources()                      # discover datasources
df = clickhouse.query(datasource, sql, params)     # -> pandas.DataFrame
```

**Which datasource holds this devnet's data varies — discover it, don't assume.**
Different devnets land in different places and the layout is in flux: some send
their logs to a ClickHouse sink, others still use Loki; the `xatu-experimental`
cluster is experimental and not always populated. So:
- Call `clickhouse.list_datasources()` and read each one's `description`.
- Probe with a cheap `count()` filtered by `meta_network_name` to find which
  datasource actually has rows for **this** network before building a real query.
- An empty result usually means **wrong datasource for this devnet**, not "no
  data" — try another datasource before concluding there's nothing.

**Two different data planes — don't confuse them:**
- **Chain events** (slots, attestations, blocks, blobs) live in the Xatu
  clusters, filtered by `meta_network_name` (e.g. `xatu-experimental`, or
  `clickhouse-raw` `default.canonical_*`).
- **Container/client logs** (geth/lighthouse/etc. stdout — the "log sink") are
  being migrated off Loki into `clickhouse-raw` **`internal.otel_logs`**, keyed by
  **`k8s.namespace.name`** (= the devnet name, *not* `meta_network_name`) and
  requiring a `Timestamp` filter, e.g.
  `WHERE k8s.namespace.name = {ns:String} AND Timestamp > now() - INTERVAL 1 HOUR`.
  **panda has no Loki datasource** — a devnet still on Loki has **no logs panda
  can read**; only devnets migrated to the ClickHouse sink appear in `otel_logs`.

Bind values with `params` (`{name:Type}`). Discover tables/columns with
`panda schema` or `clickhouse://tables/{table}` — never invent names.

## Live node queries — the `ethnode` module

For **real-time** chain state (current block/slot, sync, finality, peers, config
spec, fork schedule) query the devnet's EL/CL nodes directly — no ClickHouse, no
log lag:

```python
from ethpandaops import ethnode
net  = "bal-devnet-7"        # the devnet name from your system prompt
inst = "lighthouse-geth-1"   # a <cl>-<el>-<n> node instance (see below)
ethnode.eth_block_number(net, inst)                      # EL head block
ethnode.get_beacon_headers(net, inst)                    # CL head header
ethnode.get_finality_checkpoints(net, inst)              # justified/finalized
ethnode.get_peer_count(net, inst)                        # CL peers
ethnode.execution_rpc(net, inst, "eth_syncing")          # any EL JSON-RPC
ethnode.beacon_get(net, inst, "/eth/v1/node/syncing")    # any beacon REST path
```

Discover reachable networks with `ethnode.list_networks()`. Instances are named
`<cl>-<el>-<n>` (e.g. `lighthouse-geth-1`, `lodestar-geth-1`, `lighthouse-reth-1`);
a **502** means that client pairing isn't in this devnet — try another. Use
`ethnode` for "right now" questions and ClickHouse for history/aggregates.

## The three-step workflow

### 1. Discover (run once per fresh task; cache in your head)
```bash
panda datasources   # clickhouse clusters, prometheus instances, eth nodes
panda schema        # ClickHouse table schemas (Xatu and friends)
panda docs clickhouse   # real Python signatures for a module
```

### 2. Look for an existing example before writing new Python
```bash
panda search examples "block production rate"
panda search runbooks "validator queue depth"
```
Found example → adapt it. No example → write fresh Python from `panda docs`.

### 3. Execute Python in the sandbox
```bash
panda execute --code 'from ethpandaops import clickhouse; print(clickhouse.list_datasources())'
```
Multi-line scripts: write to a temp file and pass `--file`. Filter by the
devnet name from your system prompt (here shown as `bal-devnet-7`) and read the
returned **DataFrame**:
```bash
cat > /tmp/q.py <<'PY'
from ethpandaops import clickhouse

net = "bal-devnet-7"   # use the name from your system prompt
# Find which datasource actually has rows for this devnet, then query it.
for ds in clickhouse.list_datasources():
    try:
        n = clickhouse.query(
            ds["name"],
            "SELECT count() AS n FROM beacon_api_eth_v1_events_block "
            "WHERE meta_network_name = {net:String}",
            {"net": net},
        ).iloc[0]["n"]
    except Exception:
        continue
    if n:
        print(ds["name"], "->", n, "rows")
PY
panda execute --file /tmp/q.py
```

## Failure modes
| Error | Meaning | Do |
|---|---|---|
| `ModuleNotFoundError` | wrong import | Use `from ethpandaops import <module>`; check `panda docs` |
| `no server URL configured` | panda not wired | Tell the user panda isn't configured here and stop |
| `401` / token expired | bot auth lapsed | Tell the user; do not try to re-auth from chat |
| Empty result set | query ok, no match | Re-check table/time window vs `panda schema`; confirm `meta_network_name` |

## What NOT to do
- Never guess the API — imports come from `ethpandaops`, signatures from `panda docs`.
- Never read `os.environ` for the network — the sandbox has none; use the name from your prompt.
- Never invent table/column names — derive from `panda schema`.
- Never drop the `meta_network_name` filter — you'd leak other networks' data.
- Never paste raw credentials — they live in panda-proxy, invisible to your Python.

Summarise the numbers in plain English. The user wants the answer, not the query.

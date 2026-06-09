---
name: panda
description: "Query Ethereum devnet analytics (Xatu ClickHouse, Prometheus, Loki, beacon/exec nodes) by running Python in a sandboxed environment via the panda CLI. Scoped to this devnet."
version: 0.2.0
platforms: [linux]
metadata:
  hermes:
    tags: [ethereum, devnet, analytics, xatu, clickhouse, prometheus, loki, beacon, validators, slots, finality, mev, panda]
---

# panda — Ethereum analytics for this devnet

Use this skill whenever the user asks about **this devnet's state, validator
behaviour, MEV, beacon chain finality, block production, block-level access
lists, network metrics, or any data sourced from Xatu, Prometheus, Loki, or
beacon/exec nodes**.

`panda` is a CLI in the container that talks to a local panda-server, which runs
sandboxed Python with libraries for ClickHouse (Xatu), Prometheus, Loki, Dora,
and Ethnode. **You write Python; the server executes it; you read the output.**

## Always scope to this devnet

This chat serves a single devnet. Its name is in the `DEVNET_NETWORK`
environment variable (e.g. `bal-devnet-7`). **Filter every query by this
network** — Xatu holds many networks. Use the `meta_network_name` column on
Xatu tables, e.g. `WHERE meta_network_name = '<DEVNET_NETWORK>'`. If the user
asks about a different network, tell them this chat only covers `DEVNET_NETWORK`.

## The three-step workflow

### 1. Discover what's available
```bash
panda datasources   # clusters, prom instances, loki, eth nodes
panda schema        # ClickHouse table schemas (Xatu and friends)
panda docs          # available Python helper APIs
```
Run these once at the start of a fresh task; cache the output in your head.

### 2. Look for an existing example before writing new Python
```bash
panda search examples "block production rate"
panda search runbooks "validator queue depth"
```
Found example → adapt it. No example → write fresh Python from the docs.

### 3. Execute Python in the sandbox
```bash
panda execute --code 'print(beacon.head().slot)'
```
Multi-line scripts: write to a temp file and pass `--file`.
```bash
cat > /tmp/q.py <<'PY'
import os
from xatu import clickhouse
net = os.environ["DEVNET_NETWORK"]
rows = clickhouse.query(f"""
  SELECT toStartOfHour(slot_start_date_time) AS h, count() AS n
  FROM canonical_beacon_block
  WHERE meta_network_name = '{net}'
    AND slot_start_date_time > now() - INTERVAL 24 HOUR
  GROUP BY h ORDER BY h
""")
for r in rows:
    print(r["h"], r["n"])
PY
panda execute --file /tmp/q.py
```

## Failure modes
| Error | Meaning | Do |
|---|---|---|
| `no server URL configured` | panda not wired | Tell the user panda isn't configured here and stop |
| `401` / token expired | bot auth lapsed | Tell the user; do not try to re-auth from chat |
| Empty result set | query ok, no match | Re-check table/time window vs `panda schema`; confirm `meta_network_name` |

## What NOT to do
- Never invent table/column names — derive from `panda schema`.
- Never drop the `meta_network_name` filter — you'd leak other networks' data.
- Never paste raw credentials — they live in panda-proxy, invisible to your Python.

Summarise the numbers in plain English. The user wants the answer, not the query.

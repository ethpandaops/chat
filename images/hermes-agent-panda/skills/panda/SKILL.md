---
name: panda
description: "Query and debug this devnet's chain data, metrics, logs, and live nodes — validators, finality, blocks, blobs, MEV, ePBS/PeerDAS — via the panda CLI. Scoped to this devnet."
version: 0.6.0
platforms: [linux]
metadata:
  hermes:
    tags: [ethereum, devnet, analytics, xatu, clickhouse, prometheus, dora, ethnode, logs, otel, beacon, validators, slots, finality, blobs, mev, panda]
---

# panda — Ethereum analytics for this devnet

Use this skill whenever the user asks about **this devnet's state**: validators,
finality, block/blob production, MEV, network metrics, client logs, or anything
sourced from Xatu ClickHouse, Prometheus, Dora, or the EL/CL nodes.

`panda` is a CLI that runs sandboxed Python against a local panda-server. **You
write Python; the server runs it; you read the output.**

## Read the live guide first

panda's usage guide is **owned by the code and never stale** — read it before you
write anything, and follow it. It covers the `panda execute` workflow, the
`ethpandaops` modules, the ClickHouse raw-vs-refined rules, Dora, ethnode, and
sessions:

```bash
panda getting-started
```

Then discover the live surface instead of guessing — datasource, cluster, and
table names are owned by the server and change over time:

```bash
panda --help                          # all commands
panda resources                       # available data resources
panda docs <module>                   # real Python API + signatures for a module
panda schema [cluster [db [table]]]   # clusters → tables → columns
```

## Search before writing a query

Working query patterns and end-to-end procedures ship with panda — start here
rather than probing raw tables:

```bash
panda search examples "block production rate"
panda search runbooks "debug devnet"
```

Found one → adapt it. Runbooks encode which datasource to hit and the common
pitfalls; for anything non-trivial (debugging the devnet, blob propagation,
finality delay) start there.

## Scope every query to THIS devnet

This chat serves a single devnet; **its name is stated in your system prompt**
(e.g. `bal-devnet-7`). The sandbox has **no environment variables** — never read
`os.environ` for the network; pass the name as a literal/parameter. Xatu holds
many networks, so filter every Xatu query by `meta_network_name`, e.g.
`WHERE meta_network_name = {net:String}`. If the user asks about another network,
tell them this chat only covers this one.

## Where this devnet's data lives

Which source holds a given devnet varies and is in flux — confirm with
`panda getting-started` + a cheap `count()` probe rather than assuming:

- **Chain events** (slots, blocks, attestations, blobs) — Xatu ClickHouse.
  `clickhouse-raw`: `FROM <table>` + `WHERE meta_network_name = …`.
  `clickhouse-refined`: `FROM <network>.<table>` (the db prefix IS the filter).
  Always filter the partition column (usually `slot_start_date_time`).
- **Live EL/CL nodes** — the `ethnode` module: `(network, instance)`, instances
  named `<cl>-<el>-<index>` (e.g. `lighthouse-geth-1`). Use for "right now"
  questions (head block/slot, sync, finality, peers).
- **Container/client logs** — `external.otel_logs` in `clickhouse-raw`, filtered
  by `ResourceAttributes['network']` + `Timestamp`. **There is no Loki
  datasource.** See `panda search runbooks "debug devnet"` for the schema.
- **Network status / explorer** — the `dora` module (`get_network_overview`,
  `get_validator`, `link_*`).

## Debugging this devnet

For any "why is X broken / slow / missing / split" question, panda ships the
canonical, **fork-aware** procedure — read and follow it rather than improvising:

```bash
panda read runbooks://debug_devnet
```

The shape of it:

1. **Read the network first** — `panda resources read networks://<network>` for
   chain id, genesis, fork schedule, service URLs, and the node-inventory URL. A
   symptom only means something under the network's *active* fork rules.
2. **Stay fork-aware** — don't assume the pre-ePBS model. Under Gloas/ePBS a slot
   splits into separate beacon-block, payload-reveal, and PTC verdicts; under
   Fulu/PeerDAS availability is per data-column. A "missing payload" or "missing
   column" can be a normal protocol artifact — classify before you drill.
3. **Baseline → classify → drill** into nodes, logs, and RPC.

Pick the source that actually answers the question; treat disagreement between
them as evidence, not noise:

| Source | Answers | Reach it with |
| --- | --- | --- |
| `networks://<network>` | metadata, fork schedule, node inventory | `panda resources read` |
| Dora | indexed chain: epochs, slots, missed proposers, offline attesters | `panda dora …` |
| Forky | fork-choice / head comparison across nodes | `panda` forky module |
| ethnode | one node's live truth: head, finality, peers, syncing | `panda ethnode …` |
| `external.otel_logs` | container/runtime behaviour + historical logs | `panda clickhouse …` |

Two drill-downs you'll reach for constantly (substitute the network name from your
system prompt; confirm instance names with `panda ethnode networks`):

```bash
# Recent error+ logs for this devnet. Filter on SeverityNumber (>=17 = ERROR);
# SeverityText casing is inconsistent (error/Error/ERROR), so don't match on it.
panda clickhouse query clickhouse-raw "
  SELECT Timestamp, ServiceName, SeverityText, Body
  FROM external.otel_logs
  WHERE ResourceAttributes['network'] = '<network>'
    AND Timestamp > now() - INTERVAL 1 HOUR
    AND SeverityNumber >= 17
  ORDER BY Timestamp DESC LIMIT 100"

# One node's live consensus view (instance e.g. lighthouse-geth-1, or 'lb')
panda ethnode finality <network> <instance>
panda ethnode syncing  <network> <instance>
panda ethnode peers    <network> <instance>
```

Keep a running report at `/workspace/<network>-debug-<ts>.md`, paste raw output
(never paraphrase counts, roots, or log lines), and cite the `panda` command that
re-derives each finding — `runbooks://debug_devnet` includes a report helper.

## Attribute queries to the user

If your system prompt contains a `<user_attribution>` block with a `user_key`,
prefix **every** `panda` command with `PANDA_ON_BEHALF_OF='<user_key>'`:

```bash
PANDA_ON_BEHALF_OF='cf:someone@example.com' panda execute --code '...'
```

It rides along as an audit header only — it grants nothing, so never skip a query
because the key is missing; just omit the prefix.

## Notes

- Each `panda execute` is a **fresh Python process** — variables don't persist;
  `/workspace/` files do. Default timeout 60s, max 600s; reuse with `--session <id>`.
- Never guess the API or table/column names — derive them from `panda docs`,
  `panda schema`, and `panda search`.
- **Never paste base64 image data** — save the file to `/workspace/` and
  `storage.upload()` it to return it to the user.
- Summarise the numbers in plain English. The user wants the answer, not the query.

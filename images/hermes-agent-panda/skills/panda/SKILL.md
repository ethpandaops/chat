---
name: panda
description: "Query this Ethereum devnet's analytics — validators, finality, block/blob production, MEV, metrics, client logs — by running sandboxed Python via the panda CLI (Xatu ClickHouse, Prometheus, Dora, EL/CL nodes). Scoped to this devnet."
version: 0.5.0
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

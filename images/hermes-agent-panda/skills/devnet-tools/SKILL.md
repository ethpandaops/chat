---
name: devnet-tools
description: "Orientation for this devnet: the pre-loaded context briefing (identity, forks, nodes) and the tooling map (Dora/Forky/Tracoor/Assertoor/panda). Load first when debugging or exploring the devnet."
version: 0.2.0
platforms: [linux]
metadata:
  hermes:
    tags: [ethereum, devnet, tooling, context, orientation, dora, forky, tracoor, assertoor, grafana, nodes, forks, ethpandaops]
---

# devnet-tools — how this devnet is instrumented

Use this to orient on **this devnet** — what it is, which nodes run it, and what
tools observe it. This is the map; the `panda` skill is how you pull data. This
chat serves a **single** devnet (named in your system prompt).

## Read the briefing first — it's already on disk

```bash
cat /opt/data/devnet-context.md
```

It carries: network + chain id, **fork schedule**, every deployed tool's URL,
and the **node inventory** — pre-loaded at boot so you don't spend round-trips
discovering them. Two rules:

- If a section says *"panda-server not ready"* or the file looks stale,
  regenerate it: `/opt/panda/devnet-context.sh --refresh` (takes seconds).
- The briefing is orientation, not live state. Anything time-sensitive (head
  slot, sync, peers) comes from `panda` at answer time.

## The tooling map

The briefing lists the URLs actually deployed for this devnet — trust it over
convention. What each tool answers:

| Tool | Answers | Reach it with |
| --- | --- | --- |
| **panda** | chain data, metrics, logs, live nodes — the workhorse | `panda` skill (`panda getting-started`) |
| **Dora** | indexed explorer: epochs, slots, missed proposers, offline validators | `panda dora …`, or the explorer URL |
| **Forky** | fork-choice / head comparison across nodes (splits) | panda `forky` module |
| **Tracoor** | EL/CL block & state traces, snapshots for a slot | its URL in the briefing |
| **Assertoor** | test/health assertions running against the devnet | its URL in the briefing |
| **Grafana** | dashboards; underlying metrics via panda Prometheus | its URL in the briefing |
| **Xatu** | raw chain events (ClickHouse) — analytics source of truth | `panda clickhouse …` |
| **config service** | enodes, genesis, chain config, deposit contract | `join-devnet` skill |
| **faucet** | fund an account (powfaucet) | `faucet` skill |

## Answering devnet questions fast

1. `cat /opt/data/devnet-context.md` — identity, forks, nodes, tools.
2. Pick the source that answers it (table above). For "why is X broken":
   `panda read runbooks://debug_devnet` — the canonical, **fork-aware** procedure.
3. Lead with the numbers in plain English; cite the command that re-derives
   them. Never fabricate a node name, URL, or value — it must appear in the
   briefing or come back from a tool.

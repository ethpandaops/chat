# Devnet AI-chat agent: Hermes Agent + panda CLI/server + sandbox Python env +
# skill pack (panda/faucet/join-devnet/eth-node). Consumed by the panda-chat chart.
# Published to: docker.io/ethpandaops/chat (tag hermes-agent-panda-<base>-<sha>)
#
# BASE_IMAGE = a vanilla NousResearch Hermes Agent image. There is no published
# upstream image, so CI builds it from github.com/NousResearch/hermes-agent
# (tag = BASE_TAG) into a local `hermes-agent-base:<tag>` first, then builds this
# overlay FROM it. For a local build, build that base yourself (see README).
ARG BASE_IMAGE=hermes-agent-base
ARG BASE_TAG=2026.6.5

# Panda artifacts come from the published panda images — NOT release tarballs —
# so a branch/PR build of panda (label `build` → `pr-<n>-<sha>` image tags) can
# be consumed directly without cutting a release. One ref drives all three:
# CLI `ethpandaops/panda:<ref>`, server `:server-<ref>`, sandbox `:sandbox-<ref>`.
# For a release, set the version (e.g. `0.33.0`). Needs a panda with BOTH the
# `client_credentials` auth and the `direct` sandbox backend.
ARG PANDA_REF=latest
FROM ethpandaops/panda:${PANDA_REF} AS pandacli
FROM ethpandaops/panda:server-${PANDA_REF} AS pandaserver
FROM ethpandaops/panda:sandbox-${PANDA_REF} AS sandbox

FROM ${BASE_IMAGE}:${BASE_TAG}

USER root

# curl (the chart startupProbe curls :2480/health), yq/jq for the entrypoint +
# skills, libgomp1 for the sandbox stack's numpy/pandas C-extensions.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      curl yq jq libgomp1 && \
    rm -rf /var/lib/apt/lists/*

# panda CLI + server (static CGO-free Go binaries) from the panda images.
COPY --from=pandacli /usr/local/bin/panda /usr/local/bin/panda
COPY --from=pandaserver /usr/local/bin/panda-server /usr/local/bin/panda-server

# Sandbox python prefix → /opt/sandbox. The `direct` backend runs executed code
# with the panda-server container's own python3, so it MUST carry the analytics
# stack (pandas/numpy/clickhouse-connect + the `ethpandaops` package). CPython
# derives sys.prefix from the executable location, so /opt/sandbox/bin/python3
# resolves its site-packages under /opt/sandbox/lib; the panda-stack entrypoint
# puts that first on PATH so it's the python the `direct` backend uses.
COPY --from=sandbox /usr/local /opt/sandbox

# Langfuse SDK for Hermes' observability/langfuse plugin (into the uv venv).
RUN uv pip install --no-cache-dir --python /opt/hermes/.venv/bin/python langfuse

# Dispatch entrypoint: `panda-stack` arg → panda-server; anything else → Hermes.
COPY entrypoint.sh /opt/panda/entrypoint.sh
RUN chmod +x /opt/panda/entrypoint.sh

# Devnet skill pack — Hermes loads these when relevant.
COPY skills/ /opt/hermes/skills/data/

# Both containers run unprivileged (no dockerd); the chart pins uid 10000.
ENTRYPOINT ["/opt/panda/entrypoint.sh"]
CMD ["gateway", "run"]

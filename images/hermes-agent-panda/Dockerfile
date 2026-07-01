# Devnet AI-chat agent: Hermes Agent + panda CLI/server + dockerd + skill pack
# (panda/faucet/join-devnet). Consumed by the panda-chat chart.
# Published to: docker.io/ethpandaops/chat (tag hermes-agent-panda-<base>-<sha>)
#
# BASE_IMAGE = a vanilla NousResearch Hermes Agent image. There is no published
# upstream image, so CI builds it from github.com/NousResearch/hermes-agent
# (tag = BASE_TAG) into a local `hermes-agent-base:<tag>` first, then builds this
# overlay FROM it. For a local build, build that base yourself (see README).
ARG BASE_IMAGE=hermes-agent-base
ARG BASE_TAG=2026.6.5

# Panda CLI + server come from the published panda images — NOT release tarballs
# — so a branch/PR build of panda (label `build` → `pr-<n>-<sha>` image tags) can
# be consumed without cutting a release. One ref drives both: CLI
# `ethpandaops/panda:<ref>`, server `:server-<ref>`. Set a release version (e.g.
# `0.37.0`) for durable use. Needs a panda with the `client_credentials` proxy
# auth and the `docker` sandbox backend. The sandbox image itself is NOT baked
# in — the in-pod dockerd pulls `ethpandaops/panda:sandbox-<ref>` at runtime
# (the chart's `panda.sandboxImage`), so it must track this ref.
ARG PANDA_REF=0.37.0
FROM ethpandaops/panda:${PANDA_REF} AS pandacli
FROM ethpandaops/panda:server-${PANDA_REF} AS pandaserver

# Toolstream proxy: rewrites Hermes' custom tool-progress SSE events into
# standard OpenAI delta.tool_calls so Open-WebUI renders tool activity natively.
FROM golang:1.23-alpine AS toolproxy
WORKDIR /src
COPY toolstream-proxy/ .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /hermes-toolstream-proxy .

FROM ${BASE_IMAGE}:${BASE_TAG}

USER root

# dockerd + client (docker.io ships both on bookworm) for the `docker` sandbox
# backend; iptables/uidmap are dockerd runtime deps. curl (the chart
# startupProbe curls :2480/health), yq/jq for the entrypoint + skills.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      docker.io iptables uidmap \
      curl yq jq && \
    rm -rf /var/lib/apt/lists/*

# panda CLI + server (static CGO-free Go binaries) from the panda images. The
# sandbox image is pulled at runtime by dockerd — not baked in.
COPY --from=pandacli /usr/local/bin/panda /usr/local/bin/panda
COPY --from=pandaserver /usr/local/bin/panda-server /usr/local/bin/panda-server
COPY --from=toolproxy /hermes-toolstream-proxy /usr/local/bin/hermes-toolstream-proxy

# Langfuse SDK for Hermes' observability/langfuse plugin (into the uv venv).
RUN uv pip install --no-cache-dir --python /opt/hermes/.venv/bin/python langfuse

# Patch Hermes' chat-completions SSE to carry the tool result (it's in scope but
# dropped upstream); the toolstream proxy maps it to each card's OUTPUT in OW.
COPY patch-hermes-toolresult.py /tmp/patch-hermes-toolresult.py
RUN /opt/hermes/.venv/bin/python /tmp/patch-hermes-toolresult.py && rm /tmp/patch-hermes-toolresult.py

# Dispatch entrypoint: `panda-stack` arg → panda-server; anything else → Hermes.
COPY entrypoint.sh /opt/panda/entrypoint.sh
RUN chmod +x /opt/panda/entrypoint.sh

# Devnet skill pack — Hermes loads these when relevant.
COPY skills/ /opt/hermes/skills/data/

# overlay2 is reliable as long as dockerd's data-root (/var/lib/docker) is on a
# real fs — the chart mounts a node-local emptyDir there, so overlay2 stacks on
# ext4/xfs, NOT the pod's overlay rootfs (which the kernel rejects as an overlay
# upperdir). Override with DOCKERD_STORAGE_DRIVER=vfs only on exotic nodes whose
# kubelet dir is itself overlayfs/NFS.
ENV DOCKERD_STORAGE_DRIVER=overlay2

# Image default user is root: the panda-server container runs the panda-stack
# path (dockerd needs root) under the chart's privileged securityContext. The
# hermes container is pinned to uid 10000 by the chart, so it stays unprivileged.
USER root
ENTRYPOINT ["/opt/panda/entrypoint.sh"]
CMD ["gateway", "run"]

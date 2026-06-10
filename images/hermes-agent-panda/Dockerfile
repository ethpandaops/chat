# Devnet AI-chat agent: Hermes Agent + panda CLI/server + dockerd + skill pack
# (panda/faucet/join-devnet/eth-node). Consumed by the panda-chat chart.
# Published to: ghcr.io/ethpandaops/hermes-agent-panda
#
# BASE_IMAGE = a vanilla NousResearch Hermes Agent image. There is no published
# upstream image, so CI builds it from github.com/NousResearch/hermes-agent
# (tag = BASE_TAG) into a local `hermes-agent-base:<tag>` first, then builds this
# overlay FROM it. For a local build, build that base yourself (see README).
ARG BASE_IMAGE=hermes-agent-base
ARG BASE_TAG=2026.6.5
FROM ${BASE_IMAGE}:${BASE_TAG}

USER root

# dockerd + client (docker.io ships both on bookworm) + sandbox deps.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      docker.io iptables uidmap \
      curl yq jq && \
    rm -rf /var/lib/apt/lists/*

# TODO(panda release): bump to the first panda release that ships the
# client_credentials proxy auth mode (branch qu0b/panda-client-credentials,
# i.e. > v0.31.0). The panda-chat chart >= 0.2.0 renders proxy.auth.mode
# "client_credentials", which v0.31.0 rejects — do not roll that chart with
# an image built from this tag.
ARG PANDA_VERSION=0.31.0

# panda CLI
RUN curl -fsSL \
      "https://github.com/ethpandaops/panda/releases/download/v${PANDA_VERSION}/panda_${PANDA_VERSION}_linux_amd64.tar.gz" \
    | tar -xz -C /usr/local/bin panda \
 && chmod +x /usr/local/bin/panda

# panda-server
RUN curl -fsSL \
      "https://github.com/ethpandaops/panda/releases/download/v${PANDA_VERSION}/panda-server_${PANDA_VERSION}_linux_amd64.tar.gz" \
    | tar -xz -C /usr/local/bin panda-server \
 && chmod +x /usr/local/bin/panda-server

# Langfuse SDK for Hermes' observability/langfuse plugin (into the uv venv).
RUN uv pip install --no-cache-dir --python /opt/hermes/.venv/bin/python langfuse

# Entrypoint shim: starts dockerd + panda-server before Hermes.
COPY entrypoint.sh /opt/panda/entrypoint.sh
RUN chmod +x /opt/panda/entrypoint.sh

# Devnet skill pack — Hermes loads these when relevant.
COPY skills/ /opt/hermes/skills/data/

# Override with DOCKERD_STORAGE_DRIVER=vfs if overlayfs is unavailable in-pod.
ENV DOCKERD_STORAGE_DRIVER=overlay2

# Runs privileged (dockerd needs root).
USER root
ENTRYPOINT ["/opt/panda/entrypoint.sh"]
CMD ["gateway", "run"]

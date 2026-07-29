# Podiom Home Assistant add-on image.
#
# Multi-arch single Dockerfile (linux/amd64 + linux/arm64 via TARGETARCH).
# Build context is the repo root; podiomd/podiom are copied prebuilt from
# bin/linux-${TARGETARCH}/ (produced by `make cross`) so the shipped binaries
# are byte-identical to the GitHub release — no Go builds under QEMU.
#
# All version args come from ha/versions.env (sourced by `make ha-image` and
# by release CI). BUILD_FROM has a default only so bare `docker build` fails
# loudly later rather than at FROM; keep it in sync with versions.env.

ARG BUILD_FROM=ghcr.io/hassio-addons/debian-base:9.3.0
FROM ${BUILD_FROM}

ARG TARGETARCH
ARG NODE_MAJOR
ARG CLAUDE_CODE_VERSION
ARG CODEX_VERSION
ARG MCP_PROXY_VERSION
ARG UV_VERSION
ARG TTYD_VERSION
ARG TTYD_SHA256_AMD64
ARG TTYD_SHA256_ARM64
ARG PODIOM_VERSION=dev

# Container ↔ podiomd interface contract (plan Part B):
#   PODIOM_HOME/HOME anchor all persistent state on /data (HA19),
#   DISABLE_AUTOUPDATER stops the claude CLI self-updating past its pin (HA18),
#   PODIOM_TERMINAL_PROXY enables podiomd's /terminal/* reverse proxy to ttyd.
ENV PODIOM_HOME=/data/podiom \
    HOME=/data/home \
    DISABLE_AUTOUPDATER=1 \
    PODIOM_TERMINAL_PROXY=http://127.0.0.1:7681

# Fail early if the pins were not passed (versions.env is the source of truth).
RUN set -eu; \
    for v in "$NODE_MAJOR" "$CLAUDE_CODE_VERSION" "$CODEX_VERSION" \
             "$MCP_PROXY_VERSION" "$UV_VERSION" "$TTYD_VERSION"; do \
      [ -n "$v" ] || { echo "missing build-arg; source ha/versions.env" >&2; exit 1; }; \
    done

# Node from NodeSource (per-arch debs) + pipx for mcp-proxy. git is needed by
# the coding CLIs and by Podiom's project source control; openssh-client is a
# Recommends of git, so --no-install-recommends would otherwise drop it and
# every SSH remote (git@github.com:…) would fail. procps gives the terminal
# shell ps/top.
RUN set -eu; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
      ca-certificates git openssh-client gnupg pipx procps python3; \
    curl -fsSL "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" | bash -; \
    apt-get install -y --no-install-recommends nodejs; \
    rm -rf /var/lib/apt/lists/*

RUN npm install -g \
      "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
      "@openai/codex@${CODEX_VERSION}" \
    && npm cache clean --force

RUN PIPX_HOME=/opt/pipx PIPX_BIN_DIR=/usr/local/bin \
    pipx install "mcp-proxy==${MCP_PROXY_VERSION}" && \
    PIPX_HOME=/opt/pipx PIPX_BIN_DIR=/usr/local/bin \
    pipx inject --force mcp-proxy 'mcp<2'

# uv ships both `uv` and `uvx`; uvx runs MCP servers distributed as Python
# packages (e.g. unifi-mcp) without a persistent install. pipx exposes both
# entry points into /usr/local/bin.
RUN PIPX_HOME=/opt/pipx PIPX_BIN_DIR=/usr/local/bin \
    pipx install "uv==${UV_VERSION}"

# ttyd: SHA256-verified static release binary.
RUN set -eu; \
    case "${TARGETARCH}" in \
      amd64) ttyd_arch=x86_64;  ttyd_sha="${TTYD_SHA256_AMD64}" ;; \
      arm64) ttyd_arch=aarch64; ttyd_sha="${TTYD_SHA256_ARM64}" ;; \
      *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o /usr/local/bin/ttyd \
      "https://github.com/tsl0922/ttyd/releases/download/${TTYD_VERSION}/ttyd.${ttyd_arch}"; \
    echo "${ttyd_sha}  /usr/local/bin/ttyd" | sha256sum -c -; \
    chmod 0755 /usr/local/bin/ttyd

# Podiom binaries, prebuilt by `make cross` on the host/CI.
COPY bin/linux-${TARGETARCH}/podiomd bin/linux-${TARGETARCH}/podiom /usr/local/bin/

COPY ha/rootfs/ /
RUN chmod 0755 \
      /usr/local/bin/podiom-terminal \
      /usr/local/bin/podiom-token-sync \
      /etc/s6-overlay/scripts/init-podiom.sh \
      /usr/local/bin/podiomd /usr/local/bin/podiom

LABEL io.hass.type="addon" \
      io.hass.version="${PODIOM_VERSION}"

# No ENTRYPOINT/CMD: the base image's s6-overlay /init is the entrypoint
# (add-on config.yaml sets `init: false` accordingly).

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

# Optional language toolchains: pins only. Nothing is downloaded at build time —
# podiom-toolchains installs the ticked ones into /data at start (see
# ha/rootfs/usr/local/bin/podiom-toolchains).
ARG GO_VERSION
ARG GO_SHA256_AMD64
ARG GO_SHA256_ARM64
ARG RUSTUP_INIT_VERSION
ARG RUSTUP_INIT_SHA256_AMD64
ARG RUSTUP_INIT_SHA256_ARM64
ARG RUST_VERSION
ARG SWIFTLY_VERSION
ARG SWIFTLY_SHA256_AMD64
ARG SWIFTLY_SHA256_ARM64
ARG SWIFT_VERSION
ARG SWIFTLY_PLATFORM
ARG PYTHON_VERSION

# Container ↔ podiomd interface contract (plan Part B):
#   PODIOM_HOME/HOME anchor all persistent state on /data (HA19),
#   DISABLE_AUTOUPDATER stops the claude CLI self-updating past its pin (HA18),
#   PODIOM_TERMINAL_PROXY enables podiomd's /terminal/* reverse proxy to ttyd.
ENV PODIOM_HOME=/data/podiom \
    HOME=/data/home \
    DISABLE_AUTOUPDATER=1 \
    PODIOM_TERMINAL_PROXY=http://127.0.0.1:7681

# Toolchain pins reach podiom-toolchains through the environment; the versions
# themselves live in ha/versions.env.
#
# RUSTUP_HOME/CARGO_HOME and the SWIFTLY_* trio are runtime settings, not just
# install-time ones: the `cargo` and `swift` shims resolve their toolchain
# through these, and would otherwise look under $HOME and fail after a
# successful install.
# SWIFTLY_TOOLCHAINS_DIR is separate from SWIFTLY_HOME_DIR and is NOT derived
# from it — left unset, the ~3 GB of actual Swift toolchain lands in
# $HOME/.local/share/swiftly instead, where unticking `swift` would orphan it.
ENV PODIOM_TOOLCHAINS_DIR=/data/podiom/toolchains \
    RUSTUP_HOME=/data/podiom/toolchains/rustup \
    CARGO_HOME=/data/podiom/toolchains/cargo \
    SWIFTLY_HOME_DIR=/data/podiom/toolchains/swiftly \
    SWIFTLY_BIN_DIR=/data/podiom/toolchains/swiftly/bin \
    SWIFTLY_TOOLCHAINS_DIR=/data/podiom/toolchains/swiftly/toolchains \
    GO_VERSION=${GO_VERSION} \
    GO_SHA256_AMD64=${GO_SHA256_AMD64} \
    GO_SHA256_ARM64=${GO_SHA256_ARM64} \
    RUSTUP_INIT_VERSION=${RUSTUP_INIT_VERSION} \
    RUSTUP_INIT_SHA256_AMD64=${RUSTUP_INIT_SHA256_AMD64} \
    RUSTUP_INIT_SHA256_ARM64=${RUSTUP_INIT_SHA256_ARM64} \
    RUST_VERSION=${RUST_VERSION} \
    SWIFTLY_VERSION=${SWIFTLY_VERSION} \
    SWIFTLY_SHA256_AMD64=${SWIFTLY_SHA256_AMD64} \
    SWIFTLY_SHA256_ARM64=${SWIFTLY_SHA256_ARM64} \
    SWIFT_VERSION=${SWIFT_VERSION} \
    SWIFTLY_PLATFORM=${SWIFTLY_PLATFORM} \
    PYTHON_VERSION=${PYTHON_VERSION}

# Toolchain bin dirs go on PATH unconditionally, whether or not the user ticked
# the toolchain: a missing PATH entry is harmless, and PATH is resolved at exec
# time, so a toolchain that finishes installing mid-session becomes usable with
# no second restart. Every service and terminal inherits this, which is why
# Codex-backed turns see these tools even though per-agent PATH injection can't
# reach the long-lived app-server.
# python/bin deliberately precedes /usr/bin: the /data interpreter shadows the
# image's python3 for user code, while mcp-proxy and uv keep working because
# pipx venvs record their interpreter by absolute path.
ENV PATH=/data/podiom/toolchains/python/bin:/data/podiom/toolchains/go/bin:/data/podiom/toolchains/cargo/bin:/data/podiom/toolchains/swiftly/bin:${PATH}

# Claude refuses bypassPermissions while running as root. Run every Podiom
# service and terminal as this fixed account; s6 itself remains root so the
# init oneshot can migrate existing root-owned /data installs. The fixed IDs
# keep restored Home Assistant backups usable across image releases.
RUN set -eu; \
    groupadd --gid 1000 podiom; \
    useradd --uid 1000 --gid podiom --home-dir /data/home \
      --shell /bin/bash --no-create-home podiom; \
    getent passwd podiom | grep -q '^podiom:x:1000:1000:.*:/data/home:/bin/bash$'

# Fail early if the pins were not passed (versions.env is the source of truth).
RUN set -eu; \
    for v in "$NODE_MAJOR" "$CLAUDE_CODE_VERSION" "$CODEX_VERSION" \
             "$MCP_PROXY_VERSION" "$UV_VERSION" "$TTYD_VERSION" \
             "$GO_VERSION" "$RUSTUP_INIT_VERSION" "$RUST_VERSION" \
             "$SWIFTLY_VERSION" "$SWIFT_VERSION" "$SWIFTLY_PLATFORM" "$PYTHON_VERSION"; do \
      [ -n "$v" ] || { echo "missing build-arg; source ha/versions.env" >&2; exit 1; }; \
    done

# Node from NodeSource (per-arch debs) + pipx for mcp-proxy. git is needed by
# the coding CLIs and by Podiom's project source control; openssh-client is a
# Recommends of git, so --no-install-recommends would otherwise drop it and
# every SSH remote (git@github.com:…) would fail. procps gives the terminal
# shell ps/top.
#
# The second group is what the optional language toolchains need in order to
# LINK. They have to be baked in: /usr is not writable by uid 1000 at runtime
# and would not survive an image update anyway, so a user who ticks `swift`
# later cannot apt-install them then. Swift needs nearly all of it, Rust needs
# gcc/libc6-dev for its linker, Go needs gcc only for cgo. g++ is here for the
# C++ standard library headers: without them swiftly reports the toolchain as
# unusable and Swift's clang cannot do C++ interop. Measured at +177 MB
# for every add-on user, including those who tick nothing — the cost of the
# option existing at all. Keep in sync with swift.org's Debian dependency list.
RUN set -eu; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
      ca-certificates git openssh-client gnupg pipx procps python3; \
    apt-get install -y --no-install-recommends \
      binutils binutils-gold gcc g++ libc6-dev pkg-config tzdata uuid-dev \
      libcurl4-openssl-dev libedit-dev libicu-dev libncurses-dev \
      libpython3-dev libsqlite3-dev libxml2-dev; \
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
      /usr/local/bin/podiom-toolchains \
      /etc/s6-overlay/scripts/init-podiom.sh \
      /usr/local/bin/podiomd /usr/local/bin/podiom

LABEL io.hass.type="addon" \
      io.hass.version="${PODIOM_VERSION}"

# No ENTRYPOINT/CMD: the base image's s6-overlay /init is the entrypoint
# (add-on config.yaml sets `init: false` accordingly).

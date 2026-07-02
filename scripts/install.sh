#!/usr/bin/env bash
set -euo pipefail

VERSION="latest"
INSTALL_DIR="${HOME}/.local/bin"
PODIOM_HOME_VALUE="${PODIOM_HOME:-}"
AUTOSTART="ask"
RUN_ONBOARD="yes"
SOURCE_FALLBACK="no"
DRY_RUN="no"
RELEASE_BASE="${PODIOM_RELEASE_BASE:-https://github.com/Podiom/Podiom/releases}"
REPO_URL="${PODIOM_REPO_URL:-https://github.com/Podiom/Podiom.git}"

usage() {
  cat <<'USAGE'
Install Podiom.

Usage:
  curl -fsSL https://github.com/Podiom/Podiom/releases/latest/download/install.sh | bash
  bash install.sh [--version VERSION] [--install-dir DIR] [--podiom-home DIR]
                  [--autostart ask|yes|no] [--no-onboard]
                  [--source-fallback] [--dry-run]
USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version) VERSION="${2:?missing version}"; shift 2 ;;
    --install-dir) INSTALL_DIR="${2:?missing dir}"; shift 2 ;;
    --podiom-home) PODIOM_HOME_VALUE="${2:?missing dir}"; shift 2 ;;
    --autostart) AUTOSTART="${2:?missing value}"; shift 2 ;;
    --no-onboard) RUN_ONBOARD="no"; shift ;;
    --source-fallback) SOURCE_FALLBACK="yes"; shift ;;
    --dry-run) DRY_RUN="yes"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage; exit 2 ;;
  esac
done

case "$AUTOSTART" in ask|yes|no) ;; *) echo "--autostart must be ask, yes, or no" >&2; exit 2 ;; esac

say() { printf '%s\n' "$*"; }
run() {
  if [ "$DRY_RUN" = "yes" ]; then
    printf '[dry-run] %s\n' "$*"
  else
    "$@"
  fi
}

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
  darwin) os="darwin" ;;
  linux) os="linux" ;;
  *) echo "Unsupported OS: $os" >&2; exit 1 ;;
esac

arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) echo "Unsupported architecture: $arch" >&2; exit 1 ;;
esac

tmp="$(mktemp -d)"
cleanup() { rm -rf "$tmp"; }
trap cleanup EXIT

if [ "$VERSION" = "latest" ]; then
  release_url="${RELEASE_BASE}/latest/download"
  archive="podiom_${os}_${arch}.tar.gz"
else
  release_url="${RELEASE_BASE}/download/${VERSION}"
  archive="podiom_${VERSION}_${os}_${arch}.tar.gz"
fi
url="${release_url}/${archive}"
sum_url="${release_url}/SHA256SUMS"

download_release() {
  say "Downloading ${url}"
  if [ "$DRY_RUN" = "yes" ]; then
    say "[dry-run] curl -fL $url -o $tmp/$archive"
    say "[dry-run] curl -fL $sum_url -o $tmp/SHA256SUMS"
    run mkdir -p "$INSTALL_DIR"
    say "[dry-run] verify checksum and unpack $archive"
    say "[dry-run] install podiom and podiomd into $INSTALL_DIR"
    return 0
  fi
  curl -fL "$url" -o "$tmp/$archive" || return 1
  curl -fL "$sum_url" -o "$tmp/SHA256SUMS" || return 1
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$tmp" && grep " ${archive}$" SHA256SUMS | sha256sum -c -) || return 1
  else
    expected="$(grep " ${archive}$" "$tmp/SHA256SUMS" | awk '{print $1}')"
    actual="$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')"
    [ "$expected" = "$actual" ] || return 1
  fi
  mkdir -p "$tmp/unpack"
  tar -xzf "$tmp/$archive" -C "$tmp/unpack" || return 1
  run mkdir -p "$INSTALL_DIR"
  run install -m 0755 "$tmp/unpack/podiom" "$INSTALL_DIR/podiom"
  run install -m 0755 "$tmp/unpack/podiomd" "$INSTALL_DIR/podiomd"
}

build_from_source() {
  say "Building Podiom from source fallback."
  work="$tmp/src"
  if [ -f "go.mod" ] && [ -d "cmd/podiom" ]; then
    work="$(pwd)"
  else
    git clone --depth 1 "$REPO_URL" "$work"
  fi
  (cd "$work" && make build)
  run mkdir -p "$INSTALL_DIR"
  run install -m 0755 "$work/bin/podiom" "$INSTALL_DIR/podiom"
  run install -m 0755 "$work/bin/podiomd" "$INSTALL_DIR/podiomd"
}

if ! download_release; then
  if [ "$SOURCE_FALLBACK" = "yes" ]; then
    build_from_source
  else
    echo "Release download failed. Re-run with --source-fallback to build locally." >&2
    exit 1
  fi
fi

if [ -n "$PODIOM_HOME_VALUE" ]; then
  export PODIOM_HOME="$PODIOM_HOME_VALUE"
fi

# Provision the skills topology so the feature works regardless of install order
# (S24-S26): ensure ~/.agents/skills, ~/.claude, ~/.codex exist and build the
# union links from whatever providers are present. `podiom skills relink` runs
# Provision+Relink directly (no daemon needed), is idempotent, and never
# clobbers an existing real skills dir — it just reports what it linked.
provision_skills() {
  if [ "$DRY_RUN" = "yes" ]; then
    say "[dry-run] podiom skills relink"
    return 0
  fi
  PATH="$INSTALL_DIR:$PATH" "$INSTALL_DIR/podiom" skills relink || \
    say "Note: could not build the skills union now — run 'podiom skills relink' later."
}
provision_skills

install_autostart() {
  podiomd_path="$INSTALL_DIR/podiomd"
  if [ "$os" = "darwin" ]; then
    plist="$HOME/Library/LaunchAgents/com.podiom.podiomd.plist"
    run mkdir -p "$(dirname "$plist")"
    if [ "$DRY_RUN" = "yes" ]; then
      say "[dry-run] write $plist"
    else
      cat > "$plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>com.podiom.podiomd</string>
  <key>ProgramArguments</key><array><string>${podiomd_path}</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
$(if [ -n "${PODIOM_HOME_VALUE}" ]; then printf '  <key>EnvironmentVariables</key><dict><key>PODIOM_HOME</key><string>%s</string></dict>\n' "$PODIOM_HOME_VALUE"; fi)
</dict></plist>
PLIST
      launchctl unload "$plist" >/dev/null 2>&1 || true
      launchctl load "$plist"
    fi
  elif command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1; then
    unit="$HOME/.config/systemd/user/podiom.service"
    run mkdir -p "$(dirname "$unit")"
    if [ "$DRY_RUN" = "yes" ]; then
      say "[dry-run] write $unit"
    else
      {
        echo "[Unit]"
        echo "Description=Podiom daemon"
        echo
        echo "[Service]"
        echo "ExecStart=${podiomd_path}"
        [ -n "$PODIOM_HOME_VALUE" ] && echo "Environment=PODIOM_HOME=${PODIOM_HOME_VALUE}"
        echo "Restart=always"
        echo
        echo "[Install]"
        echo "WantedBy=default.target"
      } > "$unit"
      systemctl --user daemon-reload
      systemctl --user enable --now podiom.service
    fi
  else
    say "systemd --user is not available; skipping autostart."
  fi
}

# When the onboarding wizard will run interactively, it owns the autostart
# question (asked at the end, styled to match the wizard). In that case the
# installer must not prompt or configure autostart itself — it only signals the
# wizard via PODIOM_OFFER_AUTOSTART. Otherwise (no tty / --no-onboard / CI) the
# installer keeps handling autostart as before.
WIZARD_WILL_RUN="no"
if [ "$RUN_ONBOARD" = "yes" ] && [ -e /dev/tty ] && [ -r /dev/tty ]; then
  WIZARD_WILL_RUN="yes"
fi

if [ "$AUTOSTART" = "ask" ]; then
  if [ "$WIZARD_WILL_RUN" = "yes" ]; then
    export PODIOM_OFFER_AUTOSTART=1
    AUTOSTART="defer"
  elif [ -r /dev/tty ]; then
    printf 'Start Podiom automatically when your system starts? [Y/n] ' > /dev/tty
    read -r reply < /dev/tty || reply=""
    case "${reply:-Y}" in y|Y|yes|YES|"") AUTOSTART="yes" ;; *) AUTOSTART="no" ;; esac
  else
    say "Non-interactive install: enabling autostart (pass --autostart no to disable)."
    AUTOSTART="yes"
  fi
else
  # Explicit --autostart yes|no: the installer configures it below; tell the
  # wizard to stay quiet so it doesn't re-ask or double-configure.
  export PODIOM_OFFER_AUTOSTART=0
fi
[ "$AUTOSTART" = "yes" ] && install_autostart

say "Podiom installed to ${INSTALL_DIR}."
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) say "Add ${INSTALL_DIR} to PATH if your shell cannot find podiom." ;;
esac

if [ "$RUN_ONBOARD" = "yes" ]; then
  # Launch onboarding automatically so the one-liner is the only command a user
  # needs. `podiom onboard` attaches to the controlling terminal itself, so it
  # works even under `curl | bash` (where stdin is the script pipe). We still
  # require a terminal to exist — with none (e.g. CI) we defer instead, to avoid
  # a wizard with no way to answer its prompts.
  if [ -e /dev/tty ] && [ -r /dev/tty ]; then
    PATH="$INSTALL_DIR:$PATH" run "$INSTALL_DIR/podiom" onboard
  else
    say ""
    say "Next step: run 'podiom onboard' to check your agent CLIs and create your first agent."
  fi
fi

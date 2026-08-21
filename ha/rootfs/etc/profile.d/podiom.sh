# Podiom add-on: anchor all persistent state on /data for terminal shells too
# (HA19). Services get these from the image env; login shells re-assert them.
export PODIOM_HOME="${PODIOM_HOME:-/data/podiom}"
export HOME="${HOME:-/data/home}"
export DISABLE_AUTOUPDATER="${DISABLE_AUTOUPDATER:-1}"

# The shared toolset (tools agents installed via podiom_install_tool) and the
# optional language toolchains (add-on `toolchains` option). /etc/profile
# rewrites PATH for login shells, so the image's entries have to be put back
# here or the terminal would not see them. Entries for a toolchain the user did
# not tick, or a toolset with nothing in it yet, simply do not exist, which is
# harmless. RUSTUP_HOME/SWIFTLY_HOME_DIR are how the cargo and swift shims find
# their toolchain, so they must match the image env exactly.
podiom_toolset="${PODIOM_HOME:-/data/podiom}/toolset"
podiom_toolchains="${PODIOM_TOOLCHAINS_DIR:-/data/podiom/toolchains}"
export RUSTUP_HOME="${RUSTUP_HOME:-${podiom_toolchains}/rustup}"
export CARGO_HOME="${CARGO_HOME:-${podiom_toolchains}/cargo}"
export SWIFTLY_HOME_DIR="${SWIFTLY_HOME_DIR:-${podiom_toolchains}/swiftly}"
export SWIFTLY_BIN_DIR="${SWIFTLY_BIN_DIR:-${podiom_toolchains}/swiftly/bin}"
# Not derived from SWIFTLY_HOME_DIR by swiftly — unset, the toolchain itself
# would land in $HOME/.local/share/swiftly instead of on the managed path.
export SWIFTLY_TOOLCHAINS_DIR="${SWIFTLY_TOOLCHAINS_DIR:-${podiom_toolchains}/swiftly/toolchains}"
case ":${PATH}:" in
    *":${podiom_toolchains}/go/bin:"*) ;;
    *) export PATH="${podiom_toolset}/bin:${podiom_toolset}/npm/bin:${podiom_toolchains}/python/bin:${podiom_toolchains}/go/bin:${podiom_toolchains}/cargo/bin:${podiom_toolchains}/swiftly/bin:${PATH}" ;;
esac
unset podiom_toolset podiom_toolchains

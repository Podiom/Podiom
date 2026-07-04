# Podiom add-on: anchor all persistent state on /data for terminal shells too
# (HA19). Services get these from the image env; login shells re-assert them.
export PODIOM_HOME="${PODIOM_HOME:-/data/podiom}"
export HOME="${HOME:-/data/home}"
export DISABLE_AUTOUPDATER="${DISABLE_AUTOUPDATER:-1}"

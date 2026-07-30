#!/command/with-contenv bashio
# ==============================================================================
# init-podiom (s6 oneshot): prepare /data before any service starts.
# Creates the persistent dirs and seeds config.yaml ONCE — an existing file is
# the user's and is never touched (plan D8).
# ==============================================================================
set -e

readonly PODIOM_HOME="${PODIOM_HOME:-/data/podiom}"
readonly PODIOM_USER_HOME="${HOME:-/data/home}"
readonly PODIOM_RUNTIME_USER="podiom"
readonly OWNERSHIP_MIGRATION_MARKER="${PODIOM_HOME}/.nonroot-owner-v1"

mkdir -p "${PODIOM_HOME}" "${PODIOM_USER_HOME}"

if [ ! -f "${PODIOM_HOME}/config.yaml" ]; then
    bashio::log.info "Seeding ${PODIOM_HOME}/config.yaml (bind 0.0.0.0:8099 for Ingress)"
    cat > "${PODIOM_HOME}/config.yaml" <<'EOF'
# Seeded by the Podiom Home Assistant add-on on first start.
# podiomd fills in the remaining defaults; edit freely — this file is yours
# and survives add-on restarts and updates (it lives on /data).
server:
  bind: 0.0.0.0   # container-internal; reachable only via HA Ingress (source-filtered)
  port: 8099
EOF
fi

# Minimal shell setup for the web terminal (HA22): bash -il sessions read
# /etc/profile (-> /etc/profile.d/podiom.sh) and this .bashrc.
if [ ! -f "${PODIOM_USER_HOME}/.bashrc" ]; then
    cat > "${PODIOM_USER_HOME}/.bashrc" <<'EOF'
# Podiom add-on shell defaults (seeded once; yours to edit).
[ -f /etc/profile.d/podiom.sh ] && . /etc/profile.d/podiom.sh
PS1='podiom:\w\$ '
EOF
fi

# Older add-on releases ran every process as root. Migrate their complete
# persistent trees once, without following user-controlled symlinks outside
# /data. chown changes ownership only; existing SSH/key permission modes remain
# untouched. Fresh installs take the same path because the roots were just
# created by this root oneshot.
# Refuse to let a planted marker symlink suppress the migration or make touch
# write through to a target outside the persistent tree.
if [ -L "${OWNERSHIP_MIGRATION_MARKER}" ]; then
    rm -f "${OWNERSHIP_MIGRATION_MARKER}"
fi
if [ ! -e "${OWNERSHIP_MIGRATION_MARKER}" ]; then
    bashio::log.info "Migrating persistent data ownership to ${PODIOM_RUNTIME_USER}"
    chown -R --no-dereference \
        "${PODIOM_RUNTIME_USER}:${PODIOM_RUNTIME_USER}" \
        "${PODIOM_HOME}" "${PODIOM_USER_HOME}"
    touch "${OWNERSHIP_MIGRATION_MARKER}"
fi

# The root oneshot can recreate either seed file after the migration marker
# exists, so enforce ownership for these root-authored paths on every boot.
chown --no-dereference \
    "${PODIOM_RUNTIME_USER}:${PODIOM_RUNTIME_USER}" \
    "${PODIOM_HOME}" "${PODIOM_USER_HOME}" \
    "${PODIOM_HOME}/config.yaml" "${PODIOM_USER_HOME}/.bashrc" \
    "${OWNERSHIP_MIGRATION_MARKER}"

bashio::log.info "Podiom data directories ready under /data"

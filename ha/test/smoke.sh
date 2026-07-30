#!/usr/bin/env bash
# ==============================================================================
# Smoke test for the Podiom HA add-on image (plan B1 / acceptance check 11).
#
#   ha/test/smoke.sh [image]        default image: podiom-ha:dev
#
# Runs the image standalone (no Supervisor, no s6 base startup), then asserts:
#   - podiomd serves /healthz on 8099
#   - legacy root-owned /data is safely migrated to the non-root podiom account
#   - claude / codex / mcp-proxy / uvx / ttyd are present at their pinned versions
#   - yq is absent (profile login dispatch no longer needs it)
#   - migrated SSH keys load, Git can commit, and podiomd sees the public key
#   - Claude bypassPermissions starts as non-root instead of hitting its root guard
#   - ttyd is listening on 127.0.0.1:7681
#   - token-sync (no-supervisor mode) never prints a stubbed token value,
#     and the real gateway token never appears in the container log
# ==============================================================================
set -euo pipefail

IMAGE="${1:-podiom-ha:dev}"
HEALTHZ_TIMEOUT="${HEALTHZ_TIMEOUT:-90}"
STUB_TOKEN="SMOKE-STUB-TOKEN-d41d8cd98f00b204"

cid=""
cleanup() {
    [ -n "${cid}" ] && docker rm -f "${cid}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

fail() { echo "SMOKE FAIL: $*" >&2; exit 1; }
pass() { echo "  ok: $*"; }

echo "== starting ${IMAGE}"
cid="$(
    docker run -d \
        --entrypoint /bin/bash \
        -e PODIOM_HOME=/data/podiom \
        -e HOME=/data/home \
        -e DISABLE_AUTOUPDATER=1 \
        -e PODIOM_TERMINAL_PROXY=http://127.0.0.1:7681 \
        "${IMAGE}" \
        -lc '
            set -euo pipefail
            # Simulate an upgrade from a root-based add-on, including the
            # sensitive and nested paths that must remain usable afterward.
            mkdir -p \
                "${PODIOM_HOME}/projects/legacy-repo" \
                "${HOME}/.ssh" \
                "${HOME}/.claude-work"
            chmod 0700 "${HOME}/.ssh" "${HOME}/.claude-work"
            ssh-keygen -q -t ed25519 -N "" -C legacy@example.invalid \
                -f "${HOME}/.ssh/id_ed25519"
            chmod 0600 "${HOME}/.ssh/id_ed25519"
            printf "%s\n" \
                "Host podiom-smoke" \
                "  HostName example.invalid" \
                "  User git" \
                "  IdentityFile ~/.ssh/id_ed25519" \
                "  UserKnownHostsFile ~/.ssh/known_hosts" \
                > "${HOME}/.ssh/config"
            : > "${HOME}/.ssh/known_hosts"
            chmod 0600 "${HOME}/.ssh/config" "${HOME}/.ssh/known_hosts"
            printf "%s" "{\"legacy\":true}" > "${HOME}/.claude-work/.credentials.json"
            chmod 0600 "${HOME}/.claude-work/.credentials.json"

            HOME="${HOME}" git config --global user.name "Legacy Podiom"
            HOME="${HOME}" git config --global user.email "legacy@example.invalid"
            git -C "${PODIOM_HOME}/projects/legacy-repo" init --initial-branch=main
            printf "%s\n" "before migration" \
                > "${PODIOM_HOME}/projects/legacy-repo/tracked.txt"
            git -C "${PODIOM_HOME}/projects/legacy-repo" add tracked.txt
            git -C "${PODIOM_HOME}/projects/legacy-repo" commit -m "legacy root commit"

            # A recursive ownership migration must not follow this link.
            printf "%s\n" "outside data" > /tmp/podiom-owner-guard
            ln -s /tmp/podiom-owner-guard "${HOME}/outside-link"

            # The standalone smoke harness has no s6 container_environment, so
            # invoke the same script through bashio rather than its with-contenv
            # shebang.
            bashio /etc/s6-overlay/scripts/init-podiom.sh
            s6-setuidgid podiom /usr/local/bin/podiomd &
            s6-setuidgid podiom \
                /usr/local/bin/ttyd -W -a -i 127.0.0.1 -p 7681 -P 20 \
                /usr/local/bin/podiom-terminal &
            wait -n
        '
)"

echo "== waiting for /healthz (max ${HEALTHZ_TIMEOUT}s)"
deadline=$((SECONDS + HEALTHZ_TIMEOUT))
until docker exec "${cid}" curl -fsS -o /dev/null http://127.0.0.1:8099/healthz 2>/dev/null; do
    if [ "$(docker inspect -f '{{.State.Running}}' "${cid}")" != "true" ]; then
        docker logs "${cid}" | tail -50 >&2
        fail "container exited before /healthz became ready"
    fi
    [ ${SECONDS} -lt ${deadline} ] || {
        docker logs "${cid}" | tail -50 >&2
        fail "podiomd /healthz not up after ${HEALTHZ_TIMEOUT}s"
    }
    sleep 2
done
pass "/healthz responds on 8099"

echo "== runtime identity and legacy ownership migration"
docker exec "${cid}" getent passwd podiom \
    | grep -q '^podiom:x:1000:1000:.*:/data/home:/bin/bash$' \
    || fail "podiom passwd entry is not fixed to 1000:1000 and /data/home"
for process in podiomd ttyd; do
    [ "$(docker exec "${cid}" ps -o user= -C "${process}" | tr -d " ")" = "podiom" ] \
        || fail "${process} is not running as podiom"
done
if docker exec "${cid}" find /data/home /data/podiom -xdev \
    \( ! -user podiom -o ! -group podiom \) -print -quit | grep -q .; then
    fail "legacy data contains entries not owned by podiom"
fi
[ "$(docker exec "${cid}" stat -c %u /tmp/podiom-owner-guard)" = "0" ] \
    || fail "ownership migration followed a symlink outside /data"
[ "$(docker exec "${cid}" stat -c %a /data/home/.ssh)" = "700" ] \
    || fail "migration changed .ssh mode"
[ "$(docker exec "${cid}" stat -c %a /data/home/.ssh/id_ed25519)" = "600" ] \
    || fail "migration changed private-key mode"
[ "$(docker exec "${cid}" stat -c %a /data/home/.claude-work/.credentials.json)" = "600" ] \
    || fail "migration changed provider-credential mode"
pass "services are non-root; legacy data migrated without following symlinks or widening modes"

echo "== bundled tool versions"
docker exec --user podiom "${cid}" claude --version || fail "claude --version"
docker exec --user podiom "${cid}" codex --version || fail "codex --version"
docker exec --user podiom "${cid}" mcp-proxy --help >/dev/null || fail "mcp-proxy --help"
docker exec --user podiom "${cid}" uvx --version || fail "uvx --version"
docker exec --user podiom "${cid}" ttyd --version || fail "ttyd --version"
pass "claude / codex / mcp-proxy / uvx / ttyd all run"

if docker exec "${cid}" bash -lc 'command -v yq' >/dev/null 2>&1; then
    fail "yq should not be bundled in the HA image"
fi
pass "yq is not bundled"

echo "== migrated SSH keys and configuration work as podiom"
derived_key="$(docker exec --user podiom "${cid}" \
    ssh-keygen -y -f /data/home/.ssh/id_ed25519 | awk '{print $1 " " $2}')"
stored_key="$(docker exec --user podiom "${cid}" \
    awk '{print $1 " " $2}' /data/home/.ssh/id_ed25519.pub)"
[ "${derived_key}" = "${stored_key}" ] \
    || fail "migrated private key does not match its public key"
docker exec --user podiom "${cid}" bash -lc '
    eval "$(ssh-agent -s)" >/dev/null
    trap "ssh-agent -k >/dev/null" EXIT
    ssh-add ~/.ssh/id_ed25519 >/dev/null
    ssh-add -l | grep -q ED25519
' || fail "OpenSSH could not load the migrated private key"
ssh_config="$(docker exec --user podiom "${cid}" ssh -G podiom-smoke 2>/dev/null)"
echo "${ssh_config}" | grep -q '^hostname example.invalid$' \
    || fail "OpenSSH did not read the migrated ~/.ssh/config"
echo "${ssh_config}" | grep -q '^userknownhostsfile /data/home/.ssh/known_hosts$' \
    || fail "OpenSSH did not resolve known_hosts under /data/home"
pass "migrated private key loads and OpenSSH reads config from /data/home"

echo "== migrated Git identity and repository remain writable"
[ "$(docker exec --user podiom "${cid}" git config --global user.name)" = "Legacy Podiom" ] \
    || fail "Git did not read the migrated global identity"
docker exec --user podiom "${cid}" git config --global user.name "Podiom Migrated"
docker exec --user podiom "${cid}" bash -lc '
    printf "%s\n" "after migration" >> /data/podiom/projects/legacy-repo/tracked.txt
    git -C /data/podiom/projects/legacy-repo add tracked.txt
    git -C /data/podiom/projects/legacy-repo commit -m "non-root commit"
' || fail "Git could not commit in the migrated repository"
pass "Git reads ~/.gitconfig and can write the migrated repository"

echo "== ssh-keygen defaults to the persistent podiom home"
docker exec --user podiom "${cid}" bash -lc '
    mv ~/.ssh/id_ed25519 ~/.ssh/id_ed25519.legacy
    mv ~/.ssh/id_ed25519.pub ~/.ssh/id_ed25519.pub.legacy
    printf "\n" | ssh-keygen -q -t ed25519 -N "" >/dev/null 2>&1
' || fail "ssh-keygen failed as podiom"
docker exec "${cid}" test -f /data/home/.ssh/id_ed25519.pub \
    || fail "ssh-keygen wrote its default key outside /data/home/.ssh"
pass "ssh-keygen's default key lands in /data/home/.ssh (persists on /data)"

echo "== podiomd reports the generated key and migrated Git identity"
git_token="$(docker exec "${cid}" cat /data/podiom/gateway.token 2>/dev/null || true)"
if [ -n "${git_token}" ]; then
    git_status="$(docker exec "${cid}" curl -fsS \
        -H "Authorization: Bearer ${git_token}" \
        http://127.0.0.1:8099/api/git/status)"
    echo "${git_status}" | grep -q '"ssh_key":"ssh-ed25519 ' \
        || fail "/api/git/status does not report the SSH key"
    echo "${git_status}" | grep -q '"user_name":"Podiom Migrated"' \
        || fail "/api/git/status does not report the migrated Git identity"
    pass "/api/git/status reports the SSH key and Git identity"
else
    echo "  skip: no gateway.token on disk"
fi

echo "== Claude bypassPermissions is accepted for the runtime user"
claude_out="$(docker exec --user podiom "${cid}" claude -p test \
    --permission-mode bypassPermissions --output-format text 2>&1 || true)"
if echo "${claude_out}" | grep -q "cannot be used with root/sudo privileges"; then
    fail "Claude still rejects bypassPermissions as root"
fi
echo "${claude_out}" | grep -q "Not logged in" \
    || fail "Claude did not get past the root guard to its normal login check: ${claude_out}"
pass "Claude goal permission mode starts without the root/sudo rejection"

echo "== ttyd listening on 127.0.0.1:7681"
docker exec "${cid}" bash -c 'exec 3<>/dev/tcp/127.0.0.1/7681' \
    || fail "nothing listening on 7681"
pass "ttyd port open"

echo "== gateway token stays private and owned by podiom"
old_token="$(docker exec --user podiom "${cid}" cat /data/podiom/gateway.token)"
docker exec --user podiom "${cid}" podiom token rotate >/dev/null \
    || fail "gateway token rotation failed as podiom"
real_token="$(docker exec --user podiom "${cid}" cat /data/podiom/gateway.token)"
[ -n "${real_token}" ] && [ "${real_token}" != "${old_token}" ] \
    || fail "gateway token was not rotated"
[ "$(docker exec "${cid}" stat -c %U:%G /data/podiom/gateway.token)" = "podiom:podiom" ] \
    || fail "rotated gateway token is not owned by podiom"
[ "$(docker exec "${cid}" stat -c %a /data/podiom/gateway.token)" = "600" ] \
    || fail "rotated gateway token mode is not 0600"
docker exec "${cid}" curl -fsS -o /dev/null \
    -H "Authorization: Bearer ${real_token}" \
    http://127.0.0.1:8099/api/git/status \
    || fail "daemon did not accept the rotated token"
pass "rotated token is 0600, podiom-owned, and accepted by the daemon"

echo "== token-sync no-supervisor mode never leaks a token"
# Plant a stub token file, run token-sync with SUPERVISOR_TOKEN guaranteed
# unset, and assert the stub never appears in its output. `timeout` kills the
# expected `sleep infinity` park.
out="$(docker exec --user podiom -e SUPERVISOR_TOKEN= "${cid}" bash -c "
    mkdir -p /data/home/podiom-smoke &&
    printf '%s' '${STUB_TOKEN}' > /data/home/podiom-smoke/gateway.token &&
    PODIOM_HOME=/data/home/podiom-smoke timeout 5 podiom-token-sync 2>&1 || true
")"
echo "${out}" | grep -q "token sync disabled" \
    || fail "token-sync did not report no-supervisor no-op mode; output: ${out}"
if echo "${out}" | grep -qF "${STUB_TOKEN}"; then
    fail "token-sync printed the stubbed token value"
fi
pass "token-sync no-ops quietly without a Supervisor"

echo "== container log must not contain the real gateway token"
if [ -n "${real_token}" ]; then
    if docker logs "${cid}" 2>&1 | grep -qF "${real_token}"; then
        fail "gateway token value found in the container log"
    fi
    pass "gateway token absent from the container log"
else
    echo "  skip: no gateway.token on disk (podiomd token support not built yet?)"
fi

echo "SMOKE OK"

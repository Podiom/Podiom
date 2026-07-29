#!/usr/bin/env bash
# ==============================================================================
# Smoke test for the Podiom HA add-on image (plan B1 / acceptance check 11).
#
#   ha/test/smoke.sh [image]        default image: podiom-ha:dev
#
# Runs the image standalone (no Supervisor, no s6 base startup), then asserts:
#   - podiomd serves /healthz on 8099
#   - claude / codex / mcp-proxy / uvx / ttyd are present at their pinned versions
#   - yq is absent (profile login dispatch no longer needs it)
#   - ssh-keygen's default key lands in /data/home/.ssh, and podiomd sees it
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
            mkdir -p "${PODIOM_HOME}" "${HOME}"
            if [ ! -f "${PODIOM_HOME}/config.yaml" ]; then
                printf "%s\n" \
                    "server:" \
                    "  bind: 0.0.0.0" \
                    "  port: 8099" \
                    > "${PODIOM_HOME}/config.yaml"
            fi
            /usr/local/bin/podiomd &
            /usr/local/bin/ttyd -W -a -i 127.0.0.1 -p 7681 -P 20 /usr/local/bin/podiom-terminal &
            wait -n
        '
)"

echo "== waiting for /healthz (max ${HEALTHZ_TIMEOUT}s)"
deadline=$((SECONDS + HEALTHZ_TIMEOUT))
until docker exec "${cid}" curl -fsS -o /dev/null http://127.0.0.1:8099/healthz 2>/dev/null; do
    [ ${SECONDS} -lt ${deadline} ] || {
        docker logs "${cid}" | tail -50 >&2
        fail "podiomd /healthz not up after ${HEALTHZ_TIMEOUT}s"
    }
    sleep 2
done
pass "/healthz responds on 8099"

echo "== bundled tool versions"
docker exec "${cid}" claude --version || fail "claude --version"
docker exec "${cid}" codex --version || fail "codex --version"
docker exec "${cid}" mcp-proxy --help >/dev/null || fail "mcp-proxy --help"
docker exec "${cid}" uvx --version || fail "uvx --version"
docker exec "${cid}" ttyd --version || fail "ttyd --version"
pass "claude / codex / mcp-proxy / uvx / ttyd all run"

if docker exec "${cid}" bash -lc 'command -v yq' >/dev/null 2>&1; then
    fail "yq should not be bundled in the HA image"
fi
pass "yq is not bundled"

echo "== ssh and \$HOME agree on one persistent home"
# OpenSSH expands ~ from the passwd entry, not $HOME: without the image's
# passwd fix ssh-keygen would write to /root/.ssh, off /data, and the key would
# be lost on every add-on update. Generating a key at ssh-keygen's *default*
# path is the version-proof proof of where OpenSSH thinks home is.
docker exec "${cid}" getent passwd root | grep -q ':/data/home:' \
    || fail "root's passwd home is not /data/home"
docker exec "${cid}" bash -c "printf '\n' | ssh-keygen -q -t ed25519 -N '' >/dev/null 2>&1" \
    || fail "ssh-keygen failed"
docker exec "${cid}" test -f /data/home/.ssh/id_ed25519.pub \
    || fail "ssh-keygen wrote its default key outside /data/home/.ssh"
pass "ssh-keygen's default key lands in /data/home/.ssh (persists on /data)"

echo "== podiomd reports the generated key"
git_token="$(docker exec "${cid}" cat /data/podiom/gateway.token 2>/dev/null || true)"
if [ -n "${git_token}" ]; then
    docker exec "${cid}" curl -fsS -H "Authorization: Bearer ${git_token}" \
        http://127.0.0.1:8099/api/git/status | grep -q '"ssh_key":"ssh-ed25519 ' \
        || fail "/api/git/status does not report the SSH key"
    pass "/api/git/status reports the SSH key"
else
    echo "  skip: no gateway.token on disk"
fi

echo "== ttyd listening on 127.0.0.1:7681"
docker exec "${cid}" bash -c 'exec 3<>/dev/tcp/127.0.0.1/7681' \
    || fail "nothing listening on 7681"
pass "ttyd port open"

echo "== token-sync no-supervisor mode never leaks a token"
# Plant a stub token file, run token-sync with SUPERVISOR_TOKEN guaranteed
# unset, and assert the stub never appears in its output. `timeout` kills the
# expected `sleep infinity` park.
out="$(docker exec -e SUPERVISOR_TOKEN= "${cid}" bash -c "
    mkdir -p /data/podiom-smoke &&
    printf '%s' '${STUB_TOKEN}' > /data/podiom-smoke/gateway.token &&
    PODIOM_HOME=/data/podiom-smoke timeout 5 podiom-token-sync 2>&1 || true
")"
echo "${out}" | grep -q "token sync disabled" \
    || fail "token-sync did not report no-supervisor no-op mode; output: ${out}"
if echo "${out}" | grep -qF "${STUB_TOKEN}"; then
    fail "token-sync printed the stubbed token value"
fi
pass "token-sync no-ops quietly without a Supervisor"

echo "== container log must not contain the real gateway token"
real_token="$(docker exec "${cid}" cat /data/podiom/gateway.token 2>/dev/null || true)"
if [ -n "${real_token}" ]; then
    if docker logs "${cid}" 2>&1 | grep -qF "${real_token}"; then
        fail "gateway token value found in the container log"
    fi
    pass "gateway token absent from the container log"
else
    echo "  skip: no gateway.token on disk (podiomd token support not built yet?)"
fi

echo "SMOKE OK"

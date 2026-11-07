# shellcheck shell=bash
# What the one-click runbook actions share (Epic 50 story 50.12): reaching a node, and asking
# Patroni which one leads. Sourced by switchover.sh, failover.sh and dns-swap.sh; GitHub
# Actions (cloud-*.yml) and the cloud rehearsal run the same scripts.
#
# Env: RELAY_HOST_A, RELAY_HOST_B (the nodes' SSH addresses), RELAY_SSH_USER, RELAY_SSH_KEY (a
# private key's text), or NODE_EXEC (a command given <host> <shell command>: the rehearsal's
# docker exec); PATRONI_URL (on the node; default its private address's :8008, as deploy.sh);
# SUDO ("sudo"; the rehearsal's containers run as root: empty).
log() { echo "${0##*/}: $(date -u +%FT%TZ) $*" >&2; }
die() { log "$*"; exit 1; }

: "${RELAY_HOST_A:?}" "${RELAY_HOST_B:?}"
SUDO="${SUDO-sudo}"
if [ -z "${NODE_EXEC:-}" ]; then
    _key="$(mktemp)"; trap 'rm -f "${_key}"' EXIT
    printf '%s\n' "${RELAY_SSH_KEY:?}" >"${_key}"; chmod 0600 "${_key}"
    on() { local host=$1; shift; ssh -i "${_key}" -o StrictHostKeyChecking=accept-new -o BatchMode=yes -o ConnectTimeout=10 "${RELAY_SSH_USER:?}@${host}" "$@"; }
else
    on() { ${NODE_EXEC} "$@"; }
fi
_patroni="${PATRONI_URL:-http://\$(sed -n 's/^NODE_ADDR=//p' /etc/maktaba/cloud-ha.env):8008}"

# leads <host>: Patroni on <host> says it is the primary
leads() { on "$1" "curl -fsS -o /dev/null --max-time 3 ${_patroni}/primary" 2>/dev/null; }

# find_pair: sets leader and standby (the hosts), or fails when no node leads
# shellcheck disable=SC2034 # leader and standby are the caller's
find_pair() {
    leader="" standby=""
    for h in "${RELAY_HOST_A}" "${RELAY_HOST_B}"; do
        if leads "${h}"; then leader="${h}"; fi
    done
    [ -n "${leader}" ] || return 1
    standby="${RELAY_HOST_A}"; [ "${leader}" = "${RELAY_HOST_A}" ] && standby="${RELAY_HOST_B}"
    return 0
}

# wait_leads <host> <seconds>: until <host> leads
wait_leads() {
    local _
    for _ in $(seq 1 "$2"); do
        leads "$1" && return 0
        sleep 1
    done
    return 1
}

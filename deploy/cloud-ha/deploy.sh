#!/usr/bin/env bash
# Deploys a release to the Phase 1 pair with no downtime (Epic 50 story 50.08 §1). CI's
# relay.yml and a laptop run the same script. One deploy at a time (relay.yml's concurrency).
#
#   deploy.sh <tag>
#
#   0. ask Patroni which node leads (over SSH)
#   1. the standby first: fetch the release (the signed image's binary), run it once against
#      the standby's own replica (`serve` reaches /healthz on a read-only database), stop it.
#      A bad release fails here without touching users.
#   2. the migrations on the leader, expand only (tools/lint/cloud-expand-only.sh refuses a
#      contract migration in a release): `maktaba-cloud migrate up`; the running release
#      keeps working on the new schema
#   3. blue/green on the leader (bin/bluegreen: the new colour beside the old, the old drains
#      its homes to it, a watch rolls back on its own)
#   4. the colour and the tag recorded on the standby too (a failover starts the same release)
#
# Env: RELAY_HOST_A, RELAY_HOST_B (the nodes' SSH addresses), RELAY_SSH_USER, RELAY_SSH_KEY (a
# private key's text) or SSH (the command to reach a node: "ssh -i key user@"; NODE_EXEC
# replaces it entirely: a command given <host> <shell command>), IMAGE
# (ghcr.io/hamza-labs-core/maktaba-cloud).
set -euo pipefail

tag="${1:?usage: deploy.sh <tag>}"
image="${IMAGE:-ghcr.io/hamza-labs-core/maktaba-cloud}"
: "${RELAY_HOST_A:?}" "${RELAY_HOST_B:?}"
log() { echo "deploy: $(date -u +%FT%TZ) $*" >&2; }

if [ -z "${NODE_EXEC:-}" ]; then
    key="$(mktemp)"; trap 'rm -f "${key}"' EXIT
    printf '%s\n' "${RELAY_SSH_KEY:?}" >"${key}"; chmod 0600 "${key}"
    on() { local host=$1; shift; ssh -i "${key}" -o StrictHostKeyChecking=accept-new -o BatchMode=yes "${RELAY_SSH_USER:?}@${host}" "$@"; }
else
    on() { ${NODE_EXEC} "$@"; }
fi

# 0. who leads
leader=""
for h in "${RELAY_HOST_A}" "${RELAY_HOST_B}"; do
    if on "${h}" "curl -fsS -o /dev/null --max-time 3 http://\$(sed -n 's/^NODE_ADDR=//p' /etc/maktaba/cloud-ha.env):8008/primary"; then
        leader="${h}"
    fi
done
[ -n "${leader}" ] || { log "no node leads: refusing to deploy"; exit 1; }
standby="${RELAY_HOST_A}"; [ "${leader}" = "${RELAY_HOST_A}" ] && standby="${RELAY_HOST_B}"
log "${leader} leads; ${standby} is the standby"

fetch() { # the release's binary, from its signed image, under /opt/maktaba-cloud/releases/<tag>
    on "$1" "set -e; d=/opt/maktaba-cloud/releases/${tag}; [ -x \$d/maktaba-cloud ] && exit 0
        sudo docker pull -q ${image}:${tag} >/dev/null
        c=\$(sudo docker create ${image}:${tag}); sudo mkdir -p \$d
        sudo docker cp \$c:/usr/local/bin/maktaba-cloud \$d/maktaba-cloud; sudo docker rm \$c >/dev/null"
}

# 1. the standby
log "1. smoke test on ${standby}"
fetch "${standby}"
on "${standby}" "set -e; b=/opt/maktaba-cloud/releases/${tag}/maktaba-cloud
    sudo -u maktaba env \$(sudo cat /etc/maktaba/cloud.env | xargs) MAKTABA_CLOUD_LISTEN_ADDR=127.0.0.1:8499 \
        MAKTABA_CLOUD_RELAY_PEER_ADDR= \$b serve --role relay --config /etc/maktaba/cloud.toml >/tmp/smoke.log 2>&1 &
    pid=\$!; for i in \$(seq 1 30); do curl -fsS -o /dev/null http://127.0.0.1:8499/healthz && break; sleep 1; done
    ok=\$(curl -fsS -o /dev/null -w '%{http_code}' http://127.0.0.1:8499/healthz || true); kill \$pid; wait \$pid || true
    [ \"\$ok\" = 200 ] || { cat /tmp/smoke.log; exit 1; }" || { log "the release failed its smoke test on the standby: nothing changed"; exit 1; }

# 2. migrations on the leader (expand only: the old release keeps working)
log "2. migrations on ${leader}"
fetch "${leader}"
on "${leader}" "sudo -u maktaba env \$(sudo cat /etc/maktaba/cloud.env | xargs) /opt/maktaba-cloud/releases/${tag}/maktaba-cloud migrate --config /etc/maktaba/cloud.toml up"

# 3. blue/green on the leader
log "3. blue/green on ${leader}"
on "${leader}" "sudo /usr/local/bin/bluegreen ${tag}"

# 4. the standby knows what runs
c="$(on "${leader}" "cat /opt/maktaba-cloud/colour")"
on "${standby}" "echo ${c} | sudo tee /opt/maktaba-cloud/colour >/dev/null; echo ${tag} | sudo tee /opt/maktaba-cloud/deployed >/dev/null"
log "deployed ${tag} (${c}) on ${leader}; ${standby} recorded it"

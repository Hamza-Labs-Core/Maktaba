#!/usr/bin/env bash
# A rehearsal machine (Epic 50 story 50.07): what deploy/cloud-ha/setup.sh sets up on a real
# node, in one container, with the production templates. ROLE: node, db or witness.
#   witness: etcd only
#   node:    etcd, Patroni (Postgres 16; its callback is deploy/cloud-ha/bin/on-role-change),
#            PgBouncer (story 50.18), Caddy (the edge, plain HTTP here), and the cloud's relay
#            while this node leads (or always, once CLOUD_PROCESSES=off: the move to the
#            database nodes); the relay's database URLs from /etc/maktaba/cloud.env when it
#            has them, restarted when they change (the move's repoint)
#   db:      a database node (story 50.18): etcd, Patroni, PgBouncer, nothing of the cloud;
#            with JOIN=later etcd and Patroni wait for bin/migrate-db-nodes (svc.sh)
# The callback's "systemctl start|stop maktaba-cloud-active.target" is the active file here
# (MAKTABA_ACTIVE_CMD=/opt/rehearsal-node/active.sh), which the loop below follows.
set -uo pipefail
: "${NODE_NAME:?}" "${NODE_ADDR:?}" "${ETCD_INITIAL_CLUSTER:?}"

mkdir -p /etc/etcd /var/lib/etcd /run/maktaba /etc/maktaba
export ETCD_NAME="${NODE_NAME}" ETCD_CLUSTER_STATE="${ETCD_CLUSTER_STATE:-new}"
envsubst <"/opt/cloud-ha/etcd/etcd.conf.yml" >/etc/etcd/etcd.conf.yml
mkdir -p /etc/etcd/pki && cp /pki/ca.crt "/pki/${NODE_NAME}.crt" "/pki/${NODE_NAME}.key" /etc/etcd/pki/
if [ "${JOIN:-now}" != later ]; then
    etcd --config-file /etc/etcd/etcd.conf.yml >/var/log/etcd.log 2>&1 &
fi

# the cloud's logs (story 53.17): the token A, B and W share
printf '%s\n' "${LOGS_TOKEN:-rehearsal-logs-token}" >/etc/maktaba/logs.token
chmod 0644 /etc/maktaba/logs.token

if [ "${ROLE:-node}" = witness ]; then
    echo "witness ${NODE_NAME}: etcd, and the cloud's logs"
    MAKTABA_ENV=rehearsal MAKTABA_NODE_ID=witness MAKTABA_CLOUD_LOGS_DIR=/var/lib/maktaba-cloud/logs \
    MAKTABA_CLOUD_LOGS_LISTEN="${NODE_ADDR}:9610" MAKTABA_CLOUD_LOGS_TOKEN_FILE=/etc/maktaba/logs.token \
        maktaba-cloud logs >/var/log/logs.log 2>&1 &
    tail -F /var/log/etcd.log
fi

# the callback's settings (deploy/cloud-ha/cloud-ha.env's), readable by postgres
cat >/etc/maktaba/cloud-ha.env <<ENV
NODE_NAME=${NODE_NAME}
NODE_ADDR=${NODE_ADDR}
HCLOUD_ENDPOINT=${HCLOUD_ENDPOINT}
HCLOUD_TOKEN_FILE=/etc/maktaba/hcloud-token
FLOATING_IP_ID=${FLOATING_IP_ID}
SERVER_ID=${SERVER_ID}
FIP_ADD_ADDR=0
FIP_RETRY_FOR=${FIP_RETRY_FOR:-60}
FIP_RETRY_EVERY=${FIP_RETRY_EVERY:-30}
MAKTABA_ACTIVE_CMD=/opt/rehearsal-node/active.sh
ALERT_CMD=/opt/rehearsal-node/alert.sh
PG_APP_PASSWORD=${PG_APP_PASSWORD}
PG_SUPERUSER_PASSWORD=${PG_SUPERUSER_PASSWORD}
PGBOUNCER=yes
CLOUD_PROCESSES=${CLOUD_PROCESSES:-leader}
ENV
# the database's Floating IP (story 50.18): on the database nodes, and on the pair in the move
if [ -n "${DB_FLOATING_IP_ID:-}" ]; then
    printf 'DB_FLOATING_IP_ID=%s\nDB_HCLOUD_ENDPOINT=%s\n' "${DB_FLOATING_IP_ID}" "${DB_HCLOUD_ENDPOINT:-${HCLOUD_ENDPOINT}}" >>/etc/maktaba/cloud-ha.env
fi
echo "${HCLOUD_TOKEN}" >/etc/maktaba/hcloud-token
chown -R postgres:postgres /etc/maktaba /run/maktaba && chmod 0600 /etc/maktaba/hcloud-token

# pgBackRest's repositories, when the scenario has them (restore-pitr)
/opt/rehearsal-node/pgbr-conf.sh

mkdir -p /etc/patroni /var/lib/postgresql/16/maktaba
cp /pki/ca.crt /etc/patroni/etcd-ca.crt
cp "/pki/${NODE_NAME}.crt" /etc/patroni/etcd-client.crt
cp "/pki/${NODE_NAME}.key" /etc/patroni/etcd-client.key
export ETCD_PROTOCOL=https ETCD_CACERT=/etc/patroni/etcd-ca.crt ETCD_CERT=/etc/patroni/etcd-client.crt \
    ETCD_KEY=/etc/patroni/etcd-client.key PG_DATA=/var/lib/postgresql/16/maktaba \
    PG_BIN=/usr/lib/postgresql/16/bin PG_LISTEN="${PG_LISTEN:-0.0.0.0:5432}" RESTAPI_LISTEN="${RESTAPI_LISTEN:-0.0.0.0:8008}" ARCHIVE_COMMAND="${ARCHIVE_COMMAND:-/bin/true}" \
    POST_BOOTSTRAP=/usr/local/bin/maktaba-post-bootstrap ON_ROLE_CHANGE=/usr/local/bin/on-role-change \
    WATCHDOG_MODE=off
envsubst <"/opt/cloud-ha/patroni/patroni.yml" >/etc/patroni/patroni.yml
chown -R postgres:postgres /etc/patroni /var/lib/postgresql && chmod 0700 /var/lib/postgresql/16/maktaba
# PgBouncer next to Postgres (story 50.18), as setup.sh runs it on A, B and the database nodes
/opt/rehearsal-node/pgbouncer.sh
touch /var/log/patroni.log /var/log/etcd.log
if [ "${JOIN:-now}" != later ]; then
    gosu postgres env CLOUD_HA_ENV=/etc/maktaba/cloud-ha.env patroni /etc/patroni/patroni.yml >/var/log/patroni.log 2>&1 &
fi
if [ "${ROLE:-node}" = db ]; then
    echo "database node ${NODE_NAME}: etcd, Patroni, PgBouncer${JOIN:+ (JOIN=${JOIN})}"
    exec tail -F /var/log/patroni.log
fi

# the leader guard: the relay stops as soon as Patroni stops calling this node the primary
gosu postgres env CLOUD_HA_ENV=/etc/maktaba/cloud-ha.env PATRONI_URL="http://127.0.0.1:8008" \
    ACTIVE_FILE=/run/maktaba/active MAKTABA_ACTIVE_CMD=/opt/rehearsal-node/active.sh \
    leader-guard >/var/log/leader-guard.log 2>&1 &

# the edge: the rehearsal's plain-HTTP relay site, or with ORIGIN_MODE (story 50.31's scenarios)
# production's Caddyfile and UFW, and the api behind the loopback (origin.sh, firewall.sh)
caddy_config=/opt/rehearsal-node/Caddyfile
if [ -n "${ORIGIN_MODE:-}" ]; then
    [ -z "${FIREWALL_PUBLIC_NET:-}" ] || /opt/rehearsal-node/firewall.sh
    export RELAY_DOMAIN=relay.test RELAY_UPSTREAMS="127.0.0.1:8443" ACME_EMAIL=rehearsal@example.test NODE_ADDR
    /opt/rehearsal-node/origin.sh
    caddy_config=/etc/caddy/Caddyfile
fi
caddy run --adapter caddyfile --config "${caddy_config}" >/var/log/caddy.log 2>&1 &

# 50.31's api and its stand-in connector (cloudflared's place: maktaba-cloudflared.service is
# PartOf the active target): socat on the private address to the api's loopback listener, so the
# stand-in edge reaches the api only through the leader. Started and stopped with the relay.
api_pid=""
conn_pid=""
origin_start() {
    [ -n "${ORIGIN_MODE:-}" ] || return 0
    if [ -z "${api_pid}" ] || ! kill -0 "${api_pid}" 2>/dev/null; then
        MAKTABA_ENV=rehearsal MAKTABA_NODE_ID="${NODE_NAME}-api" \
        MAKTABA_CLOUD_DB_URL="postgres://maktaba:${PG_APP_PASSWORD}@127.0.0.1:5432/maktaba?sslmode=disable" \
        MAKTABA_CLOUD_PUBLIC_URL=https://api.maktaba.tv MAKTABA_CLOUD_TOKEN_SECRET="${TOKEN_SECRET}" \
        MAKTABA_CLOUD_BILLING_PROVIDER=none \
            maktaba-cloud serve --role api --config /etc/maktaba/cloud.toml >>/var/log/api.log 2>&1 &
        api_pid=$!
    fi
    if [ "${ORIGIN_MODE}" = tunnel ] && { [ -z "${conn_pid}" ] || ! kill -0 "${conn_pid}" 2>/dev/null; }; then
        echo "$(date -u +%FT%TZ) starting the connector (this node leads)"
        socat "TCP-LISTEN:7844,bind=${NODE_ADDR},fork,reuseaddr" TCP:127.0.0.1:8090 >>/var/log/connector.log 2>&1 &
        conn_pid=$!
    fi
}
origin_stop() {
    for pid in "${conn_pid}" "${api_pid}"; do
        [ -z "${pid}" ] || kill -TERM "${pid}" 2>/dev/null || true
    done
    [ -z "${conn_pid}" ] || echo "$(date -u +%FT%TZ) stopping the connector (this node no longer leads)"
    conn_pid=""
    api_pid=""
}

# the relay's database URLs: the local Postgres (Phase 1), or /etc/maktaba/cloud.env's once the
# move to the database nodes points them at PgBouncer on db.internal (story 50.18)
db_url() {
    local u
    u="$(sed -n 's/^MAKTABA_CLOUD_DB_URL=//p' /etc/maktaba/cloud.env 2>/dev/null | tail -n1)"
    echo "${u:-postgres://maktaba:${PG_APP_PASSWORD}@127.0.0.1:5432/maktaba?sslmode=disable}"
}
db_direct_url() { sed -n 's/^MAKTABA_CLOUD_DB_DIRECT_URL=//p' /etc/maktaba/cloud.env 2>/dev/null | tail -n1; }
env_sum() { cksum </etc/maktaba/cloud.env 2>/dev/null || echo none; }

# relay_start <colour>: blue on 8443 (peer 8444), green on 8453 (peer 8454), with the database
# URLs as they are now
relay_start() {
    local port=8443 peer=8444
    [ "$1" = green ] && port=8453 peer=8454
    relay_env="$(env_sum)" relay_colour="$1" relay_port="${port}"
    MAKTABA_ENV=rehearsal MAKTABA_NODE_ID="${NODE_NAME}-$1" \
    MAKTABA_CLOUD_LISTEN_ADDR="127.0.0.1:${port}" \
    MAKTABA_CLOUD_DB_URL="$(db_url)" MAKTABA_CLOUD_DB_DIRECT_URL="$(db_direct_url)" \
    MAKTABA_CLOUD_PUBLIC_URL=http://relay.test MAKTABA_CLOUD_RELAY_PUBLIC_HOST=relay.test \
    MAKTABA_CLOUD_TOKEN_SECRET="${TOKEN_SECRET}" MAKTABA_CLOUD_BILLING_PROVIDER=none \
    MAKTABA_CLOUD_RELAY_ID="${NODE_NAME}-$1" MAKTABA_CLOUD_RELAY_PEER_ADDR="${NODE_ADDR}:${peer}" \
    MAKTABA_CLOUD_LOGS_DIR=/var/lib/maktaba-cloud/logs MAKTABA_CLOUD_LOGS_INSTANCE="$1" \
    MAKTABA_CLOUD_LOGS_URL="http://${LOGS_W_ADDR:-10.60.0.13:9610}" MAKTABA_CLOUD_LOGS_TOKEN_FILE=/etc/maktaba/logs.token \
        maktaba-cloud serve --role relay >>/var/log/relay.log 2>&1 &
    relay_pid=$!
}

# the cloud processes follow the active file (on-role-change writes it through active.sh)
relay_pid="" relay_colour=blue relay_port=8443
relay_env="$(env_sum)"
while :; do
    want="$(cat /run/maktaba/active 2>/dev/null || echo stop)"
    if [ "${want}" = start ]; then origin_start; else origin_stop; fi
    # new database URLs (the move to the database nodes): a blue/green, as production's
    # CLOUD_RESTART: the other colour starts and is ready before the old one drains (the edge's
    # health check moves the requests); the old one is left to finish its drain
    if [ -n "${relay_pid}" ] && [ "$(env_sum)" != "${relay_env}" ] && kill -0 "${relay_pid}" 2>/dev/null; then
        old_pid="${relay_pid}" old_colour="${relay_colour}"
        next=green; [ "${old_colour}" = green ] && next=blue
        echo "$(date -u +%FT%TZ) the relay's database URLs changed: ${next} starts beside ${old_colour}"
        relay_start "${next}"
        for _ in $(seq 1 120); do curl -fsS -o /dev/null --max-time 1 "http://127.0.0.1:${relay_port}/readyz" && break; sleep 0.5; done
        echo "$(date -u +%FT%TZ) ${next} ready: ${old_colour} drains"
        kill -TERM "${old_pid}" 2>/dev/null || true
    fi
    if [ "${want}" = start ] && { [ -z "${relay_pid}" ] || ! kill -0 "${relay_pid}" 2>/dev/null; }; then
        echo "$(date -u +%FT%TZ) starting the relay (this node leads)"
        relay_start "${relay_colour}"
    elif [ "${want}" = stop ] && [ -n "${relay_pid}" ] && kill -0 "${relay_pid}" 2>/dev/null; then
        echo "$(date -u +%FT%TZ) stopping the relay (this node no longer leads)"
        kill -TERM "${relay_pid}"
        relay_pid=""
    fi
    sleep 0.5
done

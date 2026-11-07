#!/usr/bin/env bash
# A rehearsal machine (Epic 50 story 50.07): what deploy/cloud-ha/setup.sh sets up on a real
# node, in one container, with the production templates. ROLE: node or witness.
#   witness: etcd only
#   node:    etcd, Patroni (Postgres 16; its callback is deploy/cloud-ha/bin/on-role-change),
#            Caddy (the edge, plain HTTP here), and the cloud's relay while this node leads
# The callback's "systemctl start|stop maktaba-cloud-active.target" is the active file here
# (MAKTABA_ACTIVE_CMD=/opt/rehearsal-node/active.sh), which the loop below follows.
set -uo pipefail
: "${NODE_NAME:?}" "${NODE_ADDR:?}" "${ETCD_INITIAL_CLUSTER:?}"

mkdir -p /etc/etcd /var/lib/etcd /run/maktaba /etc/maktaba
export ETCD_NAME="${NODE_NAME}" ETCD_CLUSTER_STATE="${ETCD_CLUSTER_STATE:-new}"
envsubst <"/opt/cloud-ha/etcd/etcd.conf.yml" >/etc/etcd/etcd.conf.yml
mkdir -p /etc/etcd/pki && cp /pki/ca.crt "/pki/${NODE_NAME}.crt" "/pki/${NODE_NAME}.key" /etc/etcd/pki/
etcd --config-file /etc/etcd/etcd.conf.yml >/var/log/etcd.log 2>&1 &

if [ "${ROLE:-node}" = witness ]; then
    echo "witness ${NODE_NAME}: etcd"
    tail -F /var/log/etcd.log
fi

# the callback's settings (deploy/cloud-ha/cloud-ha.env's), readable by postgres
cat >/etc/maktaba/cloud-ha.env <<ENV
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
ENV
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
gosu postgres env CLOUD_HA_ENV=/etc/maktaba/cloud-ha.env patroni /etc/patroni/patroni.yml >/var/log/patroni.log 2>&1 &

# the leader guard: the relay stops as soon as Patroni stops calling this node the primary
gosu postgres env CLOUD_HA_ENV=/etc/maktaba/cloud-ha.env PATRONI_URL="http://127.0.0.1:8008" \
    ACTIVE_FILE=/run/maktaba/active MAKTABA_ACTIVE_CMD=/opt/rehearsal-node/active.sh \
    leader-guard >/var/log/leader-guard.log 2>&1 &

# the edge
caddy run --adapter caddyfile --config /opt/rehearsal-node/Caddyfile >/var/log/caddy.log 2>&1 &

# the cloud processes follow the active file (on-role-change writes it through active.sh)
relay_pid=""
while :; do
    want="$(cat /run/maktaba/active 2>/dev/null || echo stop)"
    if [ "${want}" = start ] && { [ -z "${relay_pid}" ] || ! kill -0 "${relay_pid}" 2>/dev/null; }; then
        echo "$(date -u +%FT%TZ) starting the relay (this node leads)"
        MAKTABA_ENV=rehearsal MAKTABA_NODE_ID="${NODE_NAME}-blue" \
        MAKTABA_CLOUD_LISTEN_ADDR=127.0.0.1:8443 \
        MAKTABA_CLOUD_DB_URL="postgres://maktaba:${PG_APP_PASSWORD}@127.0.0.1:5432/maktaba?sslmode=disable" \
        MAKTABA_CLOUD_PUBLIC_URL=http://relay.test MAKTABA_CLOUD_RELAY_PUBLIC_HOST=relay.test \
        MAKTABA_CLOUD_TOKEN_SECRET="${TOKEN_SECRET}" MAKTABA_CLOUD_BILLING_PROVIDER=none \
        MAKTABA_CLOUD_RELAY_ID="${NODE_NAME}-blue" MAKTABA_CLOUD_RELAY_PEER_ADDR="${NODE_ADDR}:8444" \
            maktaba-cloud serve --role relay >>/var/log/relay.log 2>&1 &
        relay_pid=$!
    elif [ "${want}" = stop ] && [ -n "${relay_pid}" ] && kill -0 "${relay_pid}" 2>/dev/null; then
        echo "$(date -u +%FT%TZ) stopping the relay (this node no longer leads)"
        kill -TERM "${relay_pid}"
        relay_pid=""
    fi
    sleep 0.5
done

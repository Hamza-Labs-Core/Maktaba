#!/usr/bin/env bash
# PgBouncer in front of the cloud's test Postgres (Epic 50 story 50.18 T01, T02), from the
# production template deploy/cloud-ha/pgbouncer/pgbouncer.ini: one PgBouncer in transaction
# mode on 127.0.0.1:6432 (the cloud's database.url on the database nodes) and one in session
# mode on 127.0.0.1:6433 (database.direct_url's cloud_session), each forwarding every database
# name (the integration tests make a scratch database each) to Postgres on PG_HOST:PG_PORT.
#
#   tools/ci/pgbouncer-ci.sh              start it (a container on the host's network), wait
#   tools/ci/pgbouncer-ci.sh logs         its two logs
#
# Env: PG_HOST (127.0.0.1), PG_PORT (5432), PG_USER/PG_PASSWORD (maktaba/maktaba, CI's),
# PGB_IMAGE (postgres:16-bookworm: PGDG's PgBouncer, >= 1.21), PGB_NETWORK (host), PGB_PORTS
# (docker run's -p arguments when PGB_NETWORK is not host, as on a laptop).
set -euo pipefail
here="$(cd "$(dirname "$0")/../.." && pwd)"
name="${PGB_NAME:-maktaba-pgbouncer-ci}"
if [ "${1:-}" = logs ]; then
    docker exec "${name}" sh -c 'for f in /tmp/pgb/*.log; do echo "== $f"; cat "$f"; done' || true
    exit 0
fi
pg_host="${PG_HOST:-127.0.0.1}" pg_port="${PG_PORT:-5432}"
user="${PG_USER:-maktaba}" pass="${PG_PASSWORD:-maktaba}"
work="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/pgbouncer-ci.XXXXXX")"
trap 'rm -rf "${work}"' EXIT

render() { # render <mode> <port>
    local mode=$1 port=$2
    printf '[databases]\n* = host=%s port=%s pool_mode=%s\n' "${pg_host}" "${pg_port}" "${mode}" >"${work}/databases-${mode}.ini"
    PGB_LISTEN_ADDR='*' PGB_LISTEN_PORT="${port}" PGB_SOCKET_DIR="/tmp/pgb/${mode}" PGB_AUTH_TYPE=scram-sha-256 PGB_AUTH_HBA_FILE='' PGB_CLIENT_TLS_SSLMODE=disable \
        PGB_AUTH_FILE=/tmp/pgb/userlist.txt PGB_ADMIN_USERS="${user}" PGB_LOGFILE="/tmp/pgb/${mode}.log" \
        PGB_PIDFILE="/tmp/pgb/${mode}.pid" PGB_DATABASES_FILE="/tmp/pgb/databases-${mode}.ini" \
        envsubst <"${here}/deploy/cloud-ha/pgbouncer/pgbouncer.ini" >"${work}/pgbouncer-${mode}.ini"
}
render transaction 6432
render session 6433
printf '"%s" "%s"\n' "${user}" "${pass}" >"${work}/userlist.txt"
chmod 0644 "${work}"/*

docker rm -f "${name}" >/dev/null 2>&1 || true
net=(--network "${PGB_NETWORK:-host}")
# shellcheck disable=SC2206 # the -p arguments, split on purpose
[ "${PGB_NETWORK:-host}" = host ] || net+=(${PGB_PORTS:-})
docker run -d --name "${name}" "${net[@]}" -v "${work}:/cfg:ro" "${PGB_IMAGE:-postgres:16-bookworm}" bash -c '
    set -e
    apt-get update -qq >/dev/null && apt-get install -y -qq --no-install-recommends pgbouncer >/dev/null
    mkdir -p /tmp/pgb/transaction /tmp/pgb/session && cp /cfg/* /tmp/pgb/ && chown -R postgres /tmp/pgb
    pgbouncer --version
    gosu postgres pgbouncer -d /tmp/pgb/pgbouncer-transaction.ini
    gosu postgres pgbouncer -d /tmp/pgb/pgbouncer-session.ini
    exec sleep infinity' >/dev/null
for _ in $(seq 1 90); do
    if docker exec "${name}" sh -c 'PGPASSWORD='"${pass}"' psql -h 127.0.0.1 -p 6432 -U '"${user}"' -d postgres -Atqc "SELECT 1" && PGPASSWORD='"${pass}"' psql -h 127.0.0.1 -p 6433 -U '"${user}"' -d postgres -Atqc "SELECT 1"' >/dev/null 2>&1; then
        docker exec "${name}" pgbouncer --version | awk 'NR == 1'
        echo "pgbouncer-ci: transaction pool on 6432, session pool on 6433"
        exit 0
    fi
    sleep 2
done
docker logs "${name}" 2>&1 | tail -20
"$0" logs
echo "pgbouncer-ci: PgBouncer did not answer" >&2
exit 1

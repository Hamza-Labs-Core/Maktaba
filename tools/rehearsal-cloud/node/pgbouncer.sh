#!/usr/bin/env bash
# PgBouncer on a rehearsal machine (story 50.18), from deploy/cloud-ha's templates as setup.sh
# renders them: the transaction pool and cloud_session in front of the local Postgres, on
# :6432, with no TLS (a Docker network; production requires it).
set -euo pipefail
mkdir -p /etc/pgbouncer /var/log/pgbouncer /run/pgbouncer
printf '"maktaba" "%s"\n"postgres" "%s"\n' "${PG_APP_PASSWORD:?}" "${PG_SUPERUSER_PASSWORD:?}" >/etc/pgbouncer/userlist.txt
PGB_LISTEN_ADDR='*' PGB_LISTEN_PORT=6432 PGB_SOCKET_DIR=/run/pgbouncer PGB_AUTH_TYPE=scram-sha-256 PGB_AUTH_HBA_FILE='' \
    PGB_AUTH_FILE=/etc/pgbouncer/userlist.txt PGB_ADMIN_USERS=postgres PGB_CLIENT_TLS_SSLMODE=disable \
    PGB_TLS_CERT='' PGB_TLS_KEY='' PGB_LOGFILE=/var/log/pgbouncer.log PGB_PIDFILE=/run/pgbouncer/pgbouncer.pid \
    PGB_DATABASES_FILE=/etc/pgbouncer/databases.ini \
    envsubst <"/opt/cloud-ha/pgbouncer/pgbouncer.ini" >/etc/pgbouncer/pgbouncer.ini
PG_DB=maktaba envsubst <"/opt/cloud-ha/pgbouncer/databases.ini" >/etc/pgbouncer/databases.ini
chown -R postgres:postgres /etc/pgbouncer /var/log/pgbouncer /run/pgbouncer
touch /var/log/pgbouncer.log && chown postgres /var/log/pgbouncer.log
gosu postgres pgbouncer -d /etc/pgbouncer/pgbouncer.ini

#!/usr/bin/env bash
# PgBouncer's production authentication (Epic 50 story 50.18), against a real PgBouncer and
# Postgres in one container (PGDG's image): the template deploy/cloud-ha/pgbouncer/pgbouncer.ini
# rendered as setup.sh renders it, and the hba rules taken from setup.sh itself. 6432 is open to
# the app nodes on a public address, so: the superuser only from the node (the role-change
# callback's KILL and RESUME), the app role only over TLS, and the certificate checked by name.
# Needs docker and openssl. Env: PGB_IMAGE (postgres:16-bookworm).
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/pgbouncer-auth.XXXXXX")"
trap 'rm -rf "${work}"' EXIT
bash "${here}/bin/make-etcd-certs" "${work}/pki" x=127.0.0.1 >/dev/null
bash "${here}/bin/make-pgbouncer-cert" "${work}/pki" >/dev/null
chmod 0644 "${work}/pki/"*
# setup.sh's rules, with the app role it names by default (the literal ${...} is matched)
# shellcheck disable=SC2016
sed -n '/^cat >\/etc\/pgbouncer\/pg_hba.conf <<HBA$/,/^HBA$/p' "${here}/setup.sh" | sed '1d;$d' \
    | sed 's/\${PG_APP_USER:-maktaba}/maktaba/' >"${work}/pg_hba.conf"
grep -q '^host *all *postgres *127.0.0.1/32' "${work}/pg_hba.conf" || { echo "no hba rules found in setup.sh" >&2; exit 1; }
grep -q 'PGB_AUTH_TYPE=hba PGB_AUTH_HBA_FILE=/etc/pgbouncer/pg_hba.conf' "${here}/setup.sh" \
    || { echo "setup.sh no longer renders PgBouncer with auth_type hba" >&2; exit 1; }
cp "${here}/pgbouncer/pgbouncer.ini" "${here}/pgbouncer/databases.ini" "${work}/"
cat >"${work}/run.sh" <<'SH'
#!/bin/bash
set -e
export PATH=/usr/lib/postgresql/16/bin:$PATH
apt-get update -qq >/dev/null && apt-get install -y -qq --no-install-recommends pgbouncer gettext-base ca-certificates >/dev/null
mkdir -p /tmp/pg /etc/pgbouncer /var/log/pgbouncer && chown postgres /tmp/pg /etc/pgbouncer /var/log/pgbouncer
echo super >/tmp/pw && chown postgres /tmp/pw
gosu postgres initdb -D /tmp/pg/d -A scram-sha-256 --pwfile=/tmp/pw >/dev/null
gosu postgres pg_ctl -D /tmp/pg/d -o "-k /tmp -p 5432 -c listen_addresses=127.0.0.1" -l /tmp/pg/log start >/dev/null
PGPASSWORD=super psql -h 127.0.0.1 -U postgres -qc "CREATE ROLE maktaba LOGIN PASSWORD 'app'"
PGPASSWORD=super psql -h 127.0.0.1 -U postgres -qc "CREATE DATABASE maktaba OWNER maktaba"
cp /w/pki/pgbouncer.crt /etc/pgbouncer/tls.crt; cp /w/pki/pgbouncer.key /etc/pgbouncer/tls.key
cp /w/pg_hba.conf /etc/pgbouncer/pg_hba.conf
printf '"maktaba" "app"\n"postgres" "super"\n' >/etc/pgbouncer/userlist.txt
PGB_LISTEN_ADDR='*' PGB_LISTEN_PORT=6432 PGB_SOCKET_DIR=/tmp PGB_AUTH_TYPE=hba PGB_AUTH_HBA_FILE=/etc/pgbouncer/pg_hba.conf \
  PGB_AUTH_FILE=/etc/pgbouncer/userlist.txt PGB_ADMIN_USERS=postgres PGB_CLIENT_TLS_SSLMODE=require \
  PGB_TLS_CERT=/etc/pgbouncer/tls.crt PGB_TLS_KEY=/etc/pgbouncer/tls.key PGB_LOGFILE=/var/log/pgbouncer/p.log \
  PGB_PIDFILE=/tmp/pgb.pid PGB_DATABASES_FILE=/etc/pgbouncer/databases.ini envsubst </w/pgbouncer.ini >/etc/pgbouncer/pgbouncer.ini
PG_DB=maktaba envsubst </w/databases.ini >/etc/pgbouncer/databases.ini
chown postgres /etc/pgbouncer/* && chmod 0600 /etc/pgbouncer/tls.key
gosu postgres pgbouncer -d /etc/pgbouncer/pgbouncer.ini
for _ in $(seq 1 20); do pg_isready -q -h 127.0.0.1 -p 6432 && break; sleep 0.5; done
me="$(hostname -i | awk '{print $1}')"
echo "${me} db.internal" >>/etc/hosts
fails=0
t() { if eval "$2" >/dev/null 2>&1; then r=yes; else r=no; fi
      if [ "$r" = "$3" ]; then echo "  ok    $1"; else echo "  FAIL  $1 (got $r)"; fails=1; fi; }
t "the superuser on the admin console from 127.0.0.1 (the callback's KILL and RESUME)" "PGPASSWORD=super psql -h 127.0.0.1 -p 6432 -U postgres -d pgbouncer -c 'SHOW POOLS'" yes
t "the superuser from the network: refused" "PGPASSWORD=super psql 'host=db.internal port=6432 user=postgres dbname=maktaba sslmode=require' -c 'SELECT 1'" no
t "the superuser on the admin console from the network: refused" "PGPASSWORD=super psql 'host=db.internal port=6432 user=postgres dbname=pgbouncer sslmode=require' -c 'SHOW POOLS'" no
t "the app role over TLS, the name checked (verify-full)" "PGPASSWORD=app psql 'host=db.internal port=6432 user=maktaba dbname=maktaba sslmode=verify-full sslrootcert=/w/pki/ca.crt' -Atc 'SELECT 1'" yes
t "the app role on cloud_session (the session pool)" "PGPASSWORD=app psql 'host=db.internal port=6432 user=maktaba dbname=cloud_session sslmode=verify-full sslrootcert=/w/pki/ca.crt' -Atc 'SELECT 1'" yes
t "the app role without TLS: refused" "PGPASSWORD=app psql 'host=db.internal port=6432 user=maktaba dbname=maktaba sslmode=disable' -c 'SELECT 1'" no
t "the app role with a wrong password: refused" "PGPASSWORD=nope psql 'host=db.internal port=6432 user=maktaba dbname=maktaba sslmode=require' -c 'SELECT 1'" no
t "a name the certificate does not carry: refused by verify-full" "PGPASSWORD=app psql 'host=${me} port=6432 user=maktaba dbname=maktaba sslmode=verify-full sslrootcert=/w/pki/ca.crt' -Atc 'SELECT 1'" no
[ "${fails}" = 0 ] || { tail -20 /var/log/pgbouncer/p.log; exit 1; }
echo "all passed"
SH
docker run --rm -v "${work}:/w:ro" "${PGB_IMAGE:-postgres:16-bookworm}" bash /w/run.sh

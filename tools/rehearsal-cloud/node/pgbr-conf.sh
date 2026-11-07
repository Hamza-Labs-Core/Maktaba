#!/usr/bin/env bash
# Renders deploy/cloud-ha's pgBackRest config for a rehearsal machine (story 50.09), when the
# scenario gave it repositories (REPO1_ENDPOINT). The MinIO stand-ins use a test certificate:
# no TLS verification here (production verifies).
set -euo pipefail
[ -n "${REPO1_ENDPOINT:-}" ] || exit 0
mkdir -p /etc/pgbackrest /var/log/pgbackrest /var/lib/pgbackrest /var/spool/pgbackrest
PG_DATA=/var/lib/postgresql/16/maktaba REPO_VERIFY_TLS=n envsubst <"/opt/cloud-ha/pgbackrest/pgbackrest.conf" >/etc/pgbackrest/pgbackrest.conf
chown -R postgres:postgres /etc/pgbackrest /var/log/pgbackrest /var/lib/pgbackrest /var/spool/pgbackrest
chmod 0600 /etc/pgbackrest/pgbackrest.conf

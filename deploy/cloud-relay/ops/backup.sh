#!/usr/bin/env bash
# Maktaba Cloud, one node: the nightly backup (maktaba-cloud-backup.timer, setup.sh).
#
# - the database: pg_dump's custom format, checked by listing it with pg_restore before it is
#   kept;
# - the node's keys: .env and secrets/ (the token secrets, the entitlement seed every home's
#   assertion key derives from, the log key), without which a restored database can't be
#   served as the same cloud.
#
# Both into /var/backups/maktaba-cloud (root, 0700), the newest BACKUP_KEEP_DAYS days (14) kept.
# On this node only: no off-node copy yet (docs/cloud-single-node.md, "Backups").
#
#   ops/backup.sh            take one now (what the timer runs)
#   ops/backup.sh --check    restore the newest dump into a scratch database, compare its
#                            accounts and homes with the live ones, drop it (the deploy's test)
set -euo pipefail

DEPLOY_DIR="${DEPLOY_DIR:-/opt/maktaba-relay}"
OUT="${BACKUP_DIR:-/var/backups/maktaba-cloud}"
KEEP_DAYS="${BACKUP_KEEP_DAYS:-14}"

cd "$DEPLOY_DIR"
umask 077
mkdir -p "$OUT"
get_env() { grep -E "^$1=" .env | sed -n 1p | cut -d= -f2- || true; }
PGU="$(get_env POSTGRES_USER)"; PGU="${PGU:-maktaba}"
PGD="$(get_env POSTGRES_DB)"; PGD="${PGD:-maktaba}"
pg() { docker compose --env-file .env exec -T postgres "$@"; }

if [ "${1:-}" = --check ]; then
	newest="$(ls -1t "$OUT"/db-*.dump 2>/dev/null | sed -n 1p)"
	[ -n "$newest" ] || { echo "backup check: no dump in $OUT" >&2; exit 1; }
	scratch="restore_check_$$"
	pg psql -X -q -U "$PGU" -d "$PGD" -c "DROP DATABASE IF EXISTS $scratch" -c "CREATE DATABASE $scratch" >/dev/null
	trap 'pg psql -X -q -U "$PGU" -d "$PGD" -c "DROP DATABASE IF EXISTS $scratch" >/dev/null 2>&1 || true' EXIT
	pg pg_restore -U "$PGU" -d "$scratch" --no-owner <"$newest"
	q="SELECT (SELECT count(*) FROM users) || ' ' || (SELECT count(*) FROM servers)"
	restored="$(pg psql -X -A -t -U "$PGU" -d "$scratch" -c "$q")"
	live="$(pg psql -X -A -t -U "$PGU" -d "$PGD" -c "$q")"
	echo "backup check: $(basename "$newest") restored; accounts and homes: restored ${restored}, live ${live}"
	# the live database may have grown since the dump; it never has fewer rows than a good dump
	read -r ru rs <<<"$restored"; read -r lu ls_ <<<"$live"
	[ "$ru" -le "$lu" ] && [ "$rs" -le "$ls_" ] || { echo "backup check: the restore does not match" >&2; exit 1; }
	exit 0
fi

ts="$(date -u +%Y%m%dT%H%M%SZ)"
tmp="$OUT/.db-$ts.dump.part"
trap 'rm -f "$tmp"' EXIT
pg pg_dump -U "$PGU" -d "$PGD" -Fc >"$tmp"
pg pg_restore --list <"$tmp" >/dev/null
mv "$tmp" "$OUT/db-$ts.dump"
tar -C "$DEPLOY_DIR" -czf "$OUT/keys-$ts.tar.gz" .env secrets
find "$OUT" -maxdepth 1 \( -name 'db-*.dump' -o -name 'keys-*.tar.gz' \) -mtime +"$KEEP_DAYS" -delete
echo "backup: db-$ts.dump ($(du -h "$OUT/db-$ts.dump" | cut -f1)), keys-$ts.tar.gz"

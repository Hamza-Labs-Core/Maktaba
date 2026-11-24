#!/usr/bin/env bash
# Maktaba Cloud, one node: the nightly backup (maktaba-cloud-backup.timer, setup.sh), also taken
# before each deploy (ops/rollout.sh begin).
#
# - the database: pg_dump's custom format, checked by listing it with pg_restore before it is
#   kept, and beside it every table's rows as the dump has them (both in one snapshot);
# - the node's keys: .env and secrets/ (the token secrets, the entitlement seed every home's
#   assertion key derives from, the log key), without which a restored database can't be
#   served as the same cloud, and beside them each file's SHA-256.
#
# All into /var/backups/maktaba-cloud (root, 0700), the newest BACKUP_KEEP_DAYS days (14) kept.
# On this node only: no off-node copy yet (docs/cloud-single-node.md, "Backups").
#
#   ops/backup.sh            take one now (what the timer runs)
#   ops/backup.sh --check    restore the newest backup for real and compare it (the deploy's test):
#                            the dump into a scratch database, every table's rows equal to those
#                            recorded with it; the keys into a scratch folder, every file equal to
#                            its SHA-256, the keys whole, and the password good for the database
set -euo pipefail

DEPLOY_DIR="${DEPLOY_DIR:-/opt/maktaba-relay}"
OUT="${BACKUP_DIR:-/var/backups/maktaba-cloud}"
KEEP_DAYS="${BACKUP_KEEP_DAYS:-14}"

cd "$DEPLOY_DIR"
umask 077
mkdir -p "$OUT"
env_in() { grep -E "^$2=" "$1" | sed -n 1p | cut -d= -f2- || true; }
PGU="$(env_in .env POSTGRES_USER)"; PGU="${PGU:-maktaba}"
PGD="$(env_in .env POSTGRES_DB)"; PGD="${PGD:-maktaba}"
# the stack as its own .env has it, whatever is in this environment
dc() { env -i PATH="$PATH" HOME="${HOME:-/root}" docker compose --env-file .env "$@"; }
pg() { dc exec -T postgres "$@"; }
die() { echo "backup: $*" >&2; exit 1; }

# every table's rows, "schema.table rows", sorted: psql runs the counts it generates
COUNTS="SELECT format('SELECT %L || '' '' || count(*) FROM %I.%I', schemaname || '.' || tablename, schemaname, tablename) FROM pg_tables WHERE schemaname NOT IN ('pg_catalog', 'information_schema') ORDER BY 1 \\gexec"

check() {
	local newest ts scratch dir rows
	newest="$(find "$OUT" -maxdepth 1 -name 'db-*.dump' | sort | tail -n 1)"
	[ -n "$newest" ] || die "check: no dump in $OUT"
	ts="${newest##*/db-}"; ts="${ts%.dump}"
	[ -s "$OUT/db-$ts.counts" ] || die "check: no row counts beside $(basename "$newest")"
	if [ ! -s "$OUT/keys-$ts.tar.gz" ] || [ ! -s "$OUT/keys-$ts.sha256" ]; then die "check: no keys beside $(basename "$newest")"; fi
	scratch="restore_check_$$"
	dir="$(mktemp -d)"
	# shellcheck disable=SC2064  # expands now, on purpose
	trap "rm -rf '$dir'; docker compose --env-file .env exec -T postgres psql -X -q -U '$PGU' -d '$PGD' -c 'DROP DATABASE IF EXISTS $scratch' </dev/null >/dev/null 2>&1 || true" EXIT

	# the database
	pg psql -X -q -v ON_ERROR_STOP=1 -U "$PGU" -d "$PGD" -c "DROP DATABASE IF EXISTS $scratch" -c "CREATE DATABASE $scratch" </dev/null >/dev/null
	pg pg_restore --exit-on-error -U "$PGU" -d "$scratch" --no-owner <"$newest"
	printf '%s\n' "$COUNTS" | pg psql -X -q -A -t -v ON_ERROR_STOP=1 -U "$PGU" -d "$scratch" | sort >"$dir/counts"
	if ! cmp -s "$dir/counts" "$OUT/db-$ts.counts"; then
		diff "$OUT/db-$ts.counts" "$dir/counts" >&2 || true
		die "check: the restored rows differ from those recorded with the dump"
	fi
	rows="$(awk '{n += $2} END {print n}' "$dir/counts")"
	grep -q '^public.users ' "$dir/counts" || die "check: the dump has no accounts table"

	# the keys
	mkdir "$dir/keys"
	tar -xzf "$OUT/keys-$ts.tar.gz" -C "$dir/keys"
	(cd "$dir/keys" && sha256sum --quiet --strict -c "$OUT/keys-$ts.sha256") || die "check: the keys differ from those recorded with them"
	[ "$(find "$dir/keys" -type f | wc -l | tr -d ' ')" = "$(wc -l <"$OUT/keys-$ts.sha256" | tr -d ' ')" ] || die "check: the keys archive has other files than those recorded"
	local k v
	for k in POSTGRES_PASSWORD MAKTABA_CLOUD_DB_URL MAKTABA_CLOUD_TOKEN_SECRET MAKTABA_CLOUD_STAFF_TOKEN_SECRET MAKTABA_CLOUD_STAFF_SECRET_KEY RELAY_DOMAIN; do
		v="$(env_in "$dir/keys/.env" "$k")"
		if [ -z "$v" ] || [ "$v" = CHANGEME ]; then die "check: the saved .env has no $k"; fi
	done
	seed_ok "$dir/keys/secrets/entitlement.key" || die "check: the saved entitlement key is not a 32-byte seed"
	[ "$(wc -c <"$dir/keys/secrets/log.key" | tr -d ' ')" = 32 ] || die "check: the saved log key is not 32 bytes"
	# the saved password opens the database, over the network (as the cloud's roles do)
	# shellcheck disable=SC2016  # expands in the container
	v="$(env_in "$dir/keys/.env" POSTGRES_PASSWORD | pg sh -c 'IFS= read -r PGPASSWORD && export PGPASSWORD && psql -X -q -A -t -h "$(hostname -i | cut -d" " -f1)" -U "$0" -d "$1" -c "SELECT 1"' "$PGU" "$PGD")" || v=""
	[ "$v" = 1 ] || die "check: the saved database password does not open the database"
	echo "backup check: $(basename "$newest") restored, $(wc -l <"$dir/counts" | tr -d ' ') tables and $rows rows equal to those dumped; keys-$ts restored, $(wc -l <"$OUT/keys-$ts.sha256" | tr -d ' ') files whole"
}

# seed_ok FILE: a 32-byte seed, raw, hex or base64 (what the cloud's signer loads)
seed_ok() {
	[ "$(wc -c <"$1" | tr -d ' ')" = 32 ] || grep -qxE '[0-9a-fA-F]{64}' "$1" ||
		[ "$(base64 -d <"$1" 2>/dev/null | wc -c | tr -d ' ')" = 32 ]
}

take() {
	local ts tmp
	ts="$(date -u +%Y%m%dT%H%M%SZ)"
	tmp="$OUT/.part-$ts"
	# shellcheck disable=SC2064
	trap "rm -rf '$tmp'; docker compose --env-file .env exec -T postgres rm -f /tmp/backup-$ts.dump </dev/null >/dev/null 2>&1 || true" EXIT
	mkdir "$tmp"
	# the counts and the dump in one snapshot: psql exports its own, and pg_dump (psql's \!, in the
	# container) reads it while psql's transaction holds it open
	pg psql -X -q -A -t -v ON_ERROR_STOP=1 -U "$PGU" -d "$PGD" >"$tmp/counts.raw" <<SQL
BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;
SELECT pg_export_snapshot() AS snap \\gset
\\setenv SNAP :snap
$COUNTS
\\! umask 077; pg_dump -U '$PGU' -d '$PGD' -Fc --snapshot="\$SNAP" -f /tmp/backup-$ts.dump || echo DUMP_FAILED
COMMIT;
SQL
	! grep -q DUMP_FAILED "$tmp/counts.raw" || die "pg_dump failed"
	sort "$tmp/counts.raw" >"$tmp/counts"
	[ -s "$tmp/counts" ] || die "no table was counted"
	pg cat "/tmp/backup-$ts.dump" </dev/null >"$tmp/dump"
	pg pg_restore --list <"$tmp/dump" >/dev/null
	[ -s "$tmp/dump" ] || die "the dump is empty"
	tar -C "$DEPLOY_DIR" -czf "$tmp/keys" .env secrets
	(cd "$DEPLOY_DIR" && find .env secrets -type f -exec sha256sum {} + | sort -k2) >"$tmp/sha256"
	mv "$tmp/counts" "$OUT/db-$ts.counts"
	mv "$tmp/sha256" "$OUT/keys-$ts.sha256"
	mv "$tmp/keys" "$OUT/keys-$ts.tar.gz"
	mv "$tmp/dump" "$OUT/db-$ts.dump" # last: a dump that is there has the rest
	find "$OUT" -maxdepth 1 \( -name 'db-*' -o -name 'keys-*' \) -mtime +"$KEEP_DAYS" -delete
	find "$OUT" -maxdepth 1 -name '.part-*' -mmin +60 -exec rm -rf {} +
	echo "backup: db-$ts.dump ($(du -h "$OUT/db-$ts.dump" | cut -f1)), keys-$ts.tar.gz"
}

if [ "${1:-}" = --check ]; then check; else take; fi

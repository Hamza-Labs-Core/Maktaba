#!/usr/bin/env bash
# Maktaba Cloud, one node: a deploy that can always go back (docs/cloud-single-node.md, "When a
# deploy fails"). Runs on the node, as root; installed as /usr/local/sbin/maktaba-rollout, outside
# the stack it saves and restores, by `begin`.
#
#   begin     before anything changes: room on the disk, the release's images pulled (by digest), a
#             backup, and the node's present set saved (compose file, Caddyfile, cloud.toml, .env,
#             secrets, ops, the web app's version, the migrations applied). Run from the new source
#             folder, with MAKTABA_CLOUD_TAG and MAKTABA_CADDY_TAG. A failure leaves the node as it was.
#   web       a web app's tar on stdin, unpacked beside the one being served; not served yet
#   apply     after setup.sh wrote the new set: migrations in a throwaway container, the stack, its
#             health watched, then the web app switched. Any failure restores the saved set.
#   commit    after the deploy's tests passed: the new set is the node's, the saved one is dropped,
#             unused images are removed
#   rollback  the tests failed, or the run stopped: restores the saved set
#   boot      the systemd unit's condition: a node that rebooted during a deploy restores first;
#             a held node does not start (exit 1)
#   unhold    a person decided: the node is as it is now, deploys may go on
#   status    prints what is pending or held
#
# Restoring: the migrations this deploy applied go back by their Down scripts (`maktaba-cloud migrate
# undo`, with the new release's image), then the saved files, then the previous images.
#   - A Down script that would delete rows written since is not run: the schema is kept, and the
#     previous release runs on it (a release's migrations only expand the schema, story 50.08).
#   - A migration that can't be undone, a Down script that fails, or a previous release that does
#     not come up: the node is HELD. The relay, api and worker are stopped, it says why, and nothing
#     more is done to the database. A dump is never put back by itself.
set -euo pipefail

DEPLOY_DIR="${DEPLOY_DIR:-/opt/maktaba-relay}"
STATE="${STATE_DIR:-/var/lib/maktaba-cloud-deploy}"
SELF=/usr/local/sbin/maktaba-rollout
MIN_FREE_MB="${MIN_FREE_MB:-3072}"
# how long the new stack's health and readiness must hold
WATCH_SECONDS="${WATCH_SECONDS:-45}"
APP=(maktaba-cloud api worker)
SET=(.env docker-compose.yml Caddyfile cloud.toml cloud.toml.template secrets ops)

log() { printf '[rollout] %s\n' "$*"; }
err() { printf '[rollout:error] %s\n' "$*" >&2; }
die() { err "$*"; exit 1; }

dc() { (cd "$DEPLOY_DIR" && docker compose --env-file .env "$@"); }
env_of() { grep -E "^$1=" "$DEPLOY_DIR/.env" 2>/dev/null | sed -n 1p | cut -d= -f2- || true; }

running() { [ -f "$DEPLOY_DIR/.env" ] && [ -f "$DEPLOY_DIR/docker-compose.yml" ] && dc ps --status running --services 2>/dev/null | grep -x "$1" >/dev/null; }

# the migrations the database records, one a line, in the order they were applied
applied() {
	local u d
	u="$(env_of POSTGRES_USER)"; d="$(env_of POSTGRES_DB)"
	dc exec -T postgres psql -X -q -A -t -v ON_ERROR_STOP=1 -U "${u:-maktaba}" -d "${d:-maktaba}" -c \
		"SELECT CASE WHEN to_regclass('goose_db_version') IS NULL THEN '' ELSE (SELECT coalesce(string_agg(version_id::text, E'\n' ORDER BY id), '') FROM goose_db_version WHERE is_applied AND version_id > 0) END" \
		</dev/null | sed '/^$/d'
}

# a first deploy has nothing to go back to: nothing was ever committed, and nothing serves
first_deploy() { [ ! -f "$STATE/committed" ] && ! running maktaba-cloud; }

cmd_begin() {
	local src tmp refs ref room p try
	src="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
	if [ -z "${MAKTABA_CLOUD_TAG:-}" ] || [ -z "${MAKTABA_CADDY_TAG:-}" ]; then die "begin: MAKTABA_CLOUD_TAG and MAKTABA_CADDY_TAG are required"; fi
	case "$MAKTABA_CLOUD_TAG $MAKTABA_CADDY_TAG" in
		*@sha256:*\ *@sha256:*) ;;
		*) die "begin: the images are named by digest" ;;
	esac
	[ ! -f "$STATE/held" ] || { cat "$STATE/held" >&2; die "the node is held: a person decides first (maktaba-rollout unhold)"; }
	install -m 0755 "${BASH_SOURCE[0]}" "$SELF"
	if [ -d "$STATE/pending" ]; then
		log "an earlier deploy was cut off: restoring what was before it"
		restore "a deploy that was cut off" || exit 1
	fi

	for p in /var/lib/docker "$(dirname "$DEPLOY_DIR")" /var/backups; do
		[ -d "$p" ] || continue
		room="$(df -Pm "$p" | awk 'NR==2 {print $4}')"
		[ "$room" -ge "$MIN_FREE_MB" ] || die "begin: $p has ${room} MB free, a deploy needs ${MIN_FREE_MB} MB: nothing was changed"
	done

	# every image of the new stack, before any file of the running one changes
	tmp="$(mktemp -d)"
	# shellcheck disable=SC2064  # expands now, on purpose
	trap "rm -rf '$tmp'" EXIT
	(
		umask 077
		if [ -f "$DEPLOY_DIR/.env" ]; then cat "$DEPLOY_DIR/.env"; else cat "$src/.env.example"; fi
		for kv in "${!MAKTABA_@}" RELAY_DOMAIN API_DOMAIN APP_DOMAIN ACME_EMAIL RELAY_TLS; do
			[ -z "${!kv:-}" ] || printf '\n%s=%s\n' "$kv" "${!kv}"
		done
		# what compose only needs to read the file (setup.sh makes the real ones)
		printf '\nMAKTABA_UID=%s\nMAKTABA_GID=%s\nPOSTGRES_PASSWORD=%s\n' 0 0 unused
	) > "$tmp/env"
	refs="$(docker compose -f "$src/docker-compose.yml" --env-file "$tmp/env" config --images | sort -u)"
	[ -n "$refs" ] || die "begin: the new compose file names no image"
	for ref in $refs; do
		case "$ref" in *@sha256:*) ;; *) die "begin: $ref is not pinned by digest" ;; esac
		for try in 1 2 3; do
			if docker image inspect "$ref" >/dev/null 2>&1 || docker pull -q "$ref" >/dev/null; then break; fi
			[ "$try" -lt 3 ] || die "begin: $ref can't be pulled: nothing was changed"
			sleep $((try * 5))
		done
		log "image $ref"
	done

	rm -rf "$STATE/pending.new"
	(umask 077; mkdir -p "$STATE/pending.new/set")
	cp /proc/sys/kernel/random/boot_id "$STATE/pending.new/boot_id"
	if first_deploy; then
		touch "$STATE/pending.new/first"
		log "a first deploy: nothing to go back to"
	else
		dc up -d --wait postgres </dev/null >/dev/null 2>&1 || die "begin: the database does not come up: nothing was changed"
		DEPLOY_DIR="$DEPLOY_DIR" bash "$src/ops/backup.sh" || die "begin: the backup before the deploy failed: nothing was changed"
		applied > "$STATE/pending.new/applied"
		for p in "${SET[@]}"; do
			[ ! -e "$DEPLOY_DIR/$p" ] || cp -a "$DEPLOY_DIR/$p" "$STATE/pending.new/set/"
		done
		readlink "$DEPLOY_DIR/web/current" > "$STATE/pending.new/web" 2>/dev/null || : > "$STATE/pending.new/web"
	fi
	sync
	mv "$STATE/pending.new" "$STATE/pending"
	log "the present set is saved"
}

need_pending() { [ -d "$STATE/pending" ] || die "$1: no deploy has begun"; }

cmd_web() {
	local t sha
	need_pending web
	t="$(mktemp -d "$DEPLOY_DIR/.web.XXXXXX")"
	# shellcheck disable=SC2064
	trap "rm -rf '$t'" EXIT
	cat > "$t/web.tar.gz"
	sha="$(sha256sum "$t/web.tar.gz" | cut -c1-16)"
	mkdir "$t/new"
	tar --no-same-owner -xzf "$t/web.tar.gz" -C "$t/new"
	[ -s "$t/new/index.html" ] || die "web: the app has no index.html"
	chmod -R a+rX "$t/new"
	mkdir -p "$DEPLOY_DIR/web/releases"
	rm -rf "$DEPLOY_DIR/web/releases/$sha.part"
	mv "$t/new" "$DEPLOY_DIR/web/releases/$sha.part"
	rm -rf "${DEPLOY_DIR:?}/web/releases/$sha"
	mv "$DEPLOY_DIR/web/releases/$sha.part" "$DEPLOY_DIR/web/releases/$sha"
	printf 'releases/%s' "$sha" > "$STATE/pending/web-next"
	log "web app $sha unpacked (not served yet)"
}

# one rename: a request sees the old app or the new one, never half of one
web_to() {
	ln -sfn "$1" "$DEPLOY_DIR/web/current.new" || return 1
	mv -T "$DEPLOY_DIR/web/current.new" "$DEPLOY_DIR/web/current"
}

# the relay's and the api's health and readiness, then that they hold (a release can come up and
# then stop being ready)
probe() { # probe SERVICE ADDR
	dc exec -T -e "HEALTHCHECK_URL=http://$2/healthz" "$1" /usr/local/bin/healthcheck </dev/null >/dev/null 2>&1 &&
		dc exec -T -e "HEALTHCHECK_URL=http://$2/readyz" "$1" /usr/local/bin/healthcheck </dev/null >/dev/null 2>&1
}
verify() {
	local end=$((SECONDS + WATCH_SECONDS)) ok=0 tries=0
	while :; do
		if probe maktaba-cloud 127.0.0.1:8443 && probe api 172.30.77.10:8090 && running worker && running caddy; then
			ok=1
		elif [ "$ok" = 1 ] || [ "$tries" -ge 12 ]; then
			return 1 # it was ready and is not any more, or never was
		fi
		tries=$((tries + 1))
		[ "$ok" = 0 ] || [ "$SECONDS" -lt "$end" ] || return 0
		sleep 5
	done
}

fail() { # fail WHY: restore, and the deploy failed
	err "$1"
	restore "$1" || true
	exit 1
}

cmd_apply() {
	need_pending apply
	[ ! -f "$STATE/held" ] || die "apply: the node is held"
	log "the database and the cache"
	dc up -d --wait postgres redis </dev/null || fail "the database or the cache does not come up"
	touch "$STATE/pending/migrating" || fail "the state folder can't be written"
	log "the migrations"
	# </dev/null everywhere: this script can reach bash on stdin, and a command that read it would
	# end the deploy at exit 0 with nothing rolled out
	dc run --rm -T --no-deps maktaba-cloud migrate --config /etc/maktaba/cloud/cloud.toml up </dev/null || fail "a migration failed"
	log "the stack"
	dc up -d --wait --remove-orphans </dev/null || fail "the stack does not come up healthy"
	log "its health, for ${WATCH_SECONDS} s"
	verify || fail "the new release is not healthy and ready"
	if [ -s "$STATE/pending/web-next" ]; then
		web_to "$(cat "$STATE/pending/web-next")" || fail "the web app could not be switched"
	fi
	log "applied: $(env_of MAKTABA_CLOUD_TAG) (pending its tests)"
	echo "__maktaba_deploy_complete__"
}

hold() { # hold WHY: stop the app, say why, leave the rest to a person
	{
		echo "HELD $(date -u +%FT%TZ): $1"
		echo "The relay, api and worker are stopped. The database is as the migrations and any Down"
		echo "scripts left it; nothing was put back over it. The set from before the deploy is in"
		echo "$STATE/pending/set, its migrations in $STATE/pending/applied, and the backups in"
		echo "/var/backups/maktaba-cloud. When the node is right again: maktaba-rollout unhold."
	} > "$STATE/held.new"
	mv "$STATE/held.new" "$STATE/held"
	dc stop "${APP[@]}" </dev/null >/dev/null 2>&1 || true
	cat "$STATE/held" >&2
	return 1
}

put_back() { # the saved files and the web app's version, each copied whole before it replaces
	local p
	for p in "${SET[@]}"; do
		[ -e "$STATE/pending/set/$p" ] || continue
		rm -rf "${DEPLOY_DIR:?}/$p.restore" || return 1
		cp -a "$STATE/pending/set/$p" "$DEPLOY_DIR/$p.restore" || return 1
	done
	for p in "${SET[@]}"; do
		[ -e "$DEPLOY_DIR/$p.restore" ] || continue
		rm -rf "${DEPLOY_DIR:?}/${p:?}" || return 1
		mv "$DEPLOY_DIR/$p.restore" "$DEPLOY_DIR/$p" || return 1
	done
	if [ -s "$STATE/pending/web" ]; then
		web_to "$(cat "$STATE/pending/web")" || return 1
	else
		rm -f "$DEPLOY_DIR/web/current" || return 1
	fi
}

restore() { # restore WHY: back to the saved set; 0 restored, 1 held (or nothing could be)
	# every step checks itself: this runs where errexit does not (restore ... || ...)
	local why="$1" added rc
	[ -d "$STATE/pending" ] || { log "nothing to restore"; return 0; }
	log "restoring the set from before the deploy ($why)"
	if [ -f "$STATE/pending/first" ]; then
		if [ -f "$DEPLOY_DIR/docker-compose.yml" ]; then dc stop "${APP[@]}" caddy </dev/null >/dev/null 2>&1 || true; fi
		rm -rf "$STATE/pending"
		err "a first deploy failed: there is no earlier release to go back to, so the stack is stopped"
		return 1
	fi

	# the migrations this deploy applied, newest first (none before apply began them)
	added=""
	if [ -f "$STATE/pending/migrating" ]; then
		dc up -d --wait postgres </dev/null >/dev/null 2>&1 || { hold "the database does not come up ($why)"; return 1; }
		applied > "$STATE/pending/applied.now" || { hold "the applied migrations can't be read ($why)"; return 1; }
		added="$(grep -vxF -f "$STATE/pending/applied" "$STATE/pending/applied.now" | tr '\n' ' ' || true)"
		[ -s "$STATE/pending/applied" ] || added="$(tr '\n' ' ' < "$STATE/pending/applied.now")"
	fi
	if [ -n "${added// /}" ]; then
		log "undoing the migrations it applied: $added"
		dc stop "${APP[@]}" </dev/null >/dev/null 2>&1 || true
		rc=0
		# shellcheck disable=SC2086  # a list of numbers
		dc run --rm -T --no-deps maktaba-cloud migrate --config /etc/maktaba/cloud/cloud.toml undo $added </dev/null || rc=$?
		case "$rc" in
			0) log "undone by their Down scripts" ;;
			3) err "a Down script would delete rows written since: the schema is KEPT, and the previous release runs on it" ;;
			4) hold "a migration it applied can't be undone, and the previous release may not run on what it left ($why)"; return 1 ;;
			*) hold "a Down script failed (exit $rc) ($why)"; return 1 ;;
		esac
	fi

	put_back || { hold "the saved files can't be put back ($why)"; return 1; }

	dc up -d --wait --remove-orphans postgres redis </dev/null || { hold "the previous database or cache does not come up ($why)"; return 1; }
	dc up -d --wait --force-recreate "${APP[@]}" caddy </dev/null || { hold "the previous release does not come up ($why)"; return 1; }
	WATCH_SECONDS=10 verify || { hold "the previous release is not healthy ($why)"; return 1; }
	rm -rf "$STATE/pending"
	log "restored: $(env_of MAKTABA_CLOUD_TAG) serves again"
	return 0
}

cmd_rollback() {
	[ ! -f "$STATE/held" ] || { cat "$STATE/held" >&2; exit 1; }
	restore "${1:-tests failed, or the run stopped}"
}

cmd_commit() {
	local cur
	need_pending commit
	[ ! -f "$STATE/held" ] || die "commit: the node is held"
	env_of MAKTABA_CLOUD_TAG > "$STATE/committed.new"
	mv "$STATE/committed.new" "$STATE/committed"
	rm -rf "$STATE/pending"
	# the web app's other versions, and the layout before versions (files right in web/)
	cur="$(readlink "$DEPLOY_DIR/web/current" 2>/dev/null || true)"
	if [ -n "$cur" ]; then
		find "$DEPLOY_DIR/web" -mindepth 1 -maxdepth 1 ! -name releases ! -name current -exec rm -rf {} +
		find "$DEPLOY_DIR/web/releases" -mindepth 1 -maxdepth 1 ! -name "${cur##*/}" -exec rm -rf {} +
	fi
	# images no container uses: the last release's, and any older
	docker image prune -af >/dev/null 2>&1 || true
	log "committed: $(cat "$STATE/committed")"
}

cmd_boot() {
	if [ -f "$STATE/held" ]; then
		cat "$STATE/held" >&2
		exit 1
	fi
	[ -d "$STATE/pending" ] || exit 0
	# the unit started again in the same boot (docker restarted by setup.sh): the deploy goes on
	if cmp -s "$STATE/pending/boot_id" /proc/sys/kernel/random/boot_id; then exit 0; fi
	log "the node rebooted during a deploy"
	restore "the node rebooted during the deploy" || exit 1
	exit 0
}

cmd_unhold() {
	[ -f "$STATE/held" ] || die "the node is not held"
	if [ -d "$STATE/pending" ]; then
		rm -rf "$STATE/held-set"
		mv "$STATE/pending" "$STATE/held-set"
	fi
	rm -f "$STATE/held"
	log "not held any more; the set from before the failed deploy is in $STATE/held-set"
}

cmd_status() {
	if [ -f "$STATE/held" ]; then cat "$STATE/held"; return 0; fi
	if [ -d "$STATE/pending" ]; then echo "pending: a deploy began and is neither committed nor rolled back"; return 0; fi
	echo "committed: $(cat "$STATE/committed" 2>/dev/null || echo none)"
}

main() {
	[ "$(id -u)" -eq 0 ] || die "run as root on the node"
	(umask 077; mkdir -p "$STATE")
	chmod 0700 "$STATE"
	local c="${1:-}"
	case "$c" in
		begin | web | apply | commit | rollback | unhold)
			exec 9>"$STATE/lock"
			flock -w 60 9 || die "another deploy is running"
			;;
		boot)
			# a deploy under way holds the lock: the unit's start is part of it
			exec 9>"$STATE/lock"
			flock -n 9 || exit 0
			;;
	esac
	shift || true
	case "$c" in
		begin) cmd_begin ;;
		web) cmd_web ;;
		apply) cmd_apply ;;
		commit) cmd_commit ;;
		rollback) cmd_rollback "$@" ;;
		boot) cmd_boot ;;
		unhold) cmd_unhold ;;
		status) cmd_status ;;
		*) die "usage: rollout.sh begin | web | apply | commit | rollback | boot | unhold | status" ;;
	esac
}

# read whole before it runs: restoring replaces files, and begin replaces this one
main "$@"
exit $?

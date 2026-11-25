#!/usr/bin/env bash
# Maktaba Cloud, one node: a deploy that can always go back (docs/cloud-single-node.md, "When a
# deploy fails"). Runs on the node, as root; installed as /usr/local/sbin/maktaba-rollout, outside
# the stack it saves and restores, by `begin`. Every command of a deploy names the deploy (ID): one
# of an older deploy does nothing to a newer one.
#
#   begin         before anything changes: room on the disk, the release's images pulled (by digest),
#                 no older release than the node's, a backup, and the node's present set saved
#                 (compose file, Caddyfile, cloud.toml, .env, secrets, ops, the web app's version, the
#                 migrations applied). Run from the new source folder, with DEPLOY_ID,
#                 MAKTABA_CLOUD_TAG and MAKTABA_CADDY_TAG. A failure leaves the node as it was.
#   setup ID      the new source folder's setup.sh, under the lock and only in this deploy
#   web ID        a web app's tar on stdin, unpacked beside the one being served; not served yet
#   job ID CMD N  apply, rollback or commit of ID as a unit of its own (systemd), started once, its
#                 output followed from line N: it outlives the SSH session that asked for it, and
#                 asking again follows the same one
#   apply ID      migrations in a throwaway container, the stack, its health watched, then the web
#                 app switched. Any failure restores the saved set.
#   commit ID     after the deploy's tests passed: the new set is the node's, the saved one is
#                 dropped, unused images are removed
#   rollback ID   the tests failed, or the run stopped: restores the saved set
#   renew ID      the deploy is still being worked on (below)
#   watch         a timer, each minute: a deploy nobody is working on any more goes back
#   boot          the stack's unit asks before it starts: a node that rebooted during a deploy
#                 restores first; a held node, one whose only deploys failed, or one a command is at
#                 work on, is not started
#   unhold        a person decided: the node is as it is now, deploys may go on
#   status        prints what is pending or held
#
# A lost runner. apply and rollback are units of the node's own, so a cut SSH session or a cancelled
# run never stops them. A deploy that begins has a lease (LEASE_SECONDS); the workflow renews it
# while it works. When it runs out, or a command was stopped part way (killed, out of memory), the
# watch restores the saved set: the node reaches a known state with nobody there.
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
SRC_DIR="${SRC_DIR:-/opt/maktaba-relay-src}"
STATE="${STATE_DIR:-/var/lib/maktaba-cloud-deploy}"
SELF=/usr/local/sbin/maktaba-rollout
PROJECT=maktaba-relay
MIN_FREE_MB="${MIN_FREE_MB:-3072}"
# how long the new stack's health and readiness must hold
WATCH_SECONDS="${WATCH_SECONDS:-45}"
# how long a deploy may go without a word from its workflow
LEASE_SECONDS="${LEASE_SECONDS:-1200}"
LOCK_WAIT="${LOCK_WAIT:-60}"
APP=(maktaba-cloud api worker)
SET=(.env docker-compose.yml Caddyfile cloud.toml cloud.toml.template secrets ops)

log() { printf '[rollout] %s\n' "$*"; }
err() { printf '[rollout:error] %s\n' "$*" >&2; }
die() { err "$*"; exit 1; }

# the stack as its own .env has it: a variable in this script's environment (the new release's tag,
# in begin) would win over the file, and restoring would start the wrong release
dc() { (cd "$DEPLOY_DIR" && env -i PATH="$PATH" HOME="${HOME:-/root}" docker compose --env-file .env "$@"); }
# the stack as the saved set has it, whatever state the new one is in; VAR=value words first win
# over its .env (the new release's image, to undo its migrations)
saved() {
	local vars=()
	while [[ "${1:-}" =~ ^[A-Z_]+= ]]; do vars+=("$1"); shift; done
	(cd "$DEPLOY_DIR" && env -i PATH="$PATH" HOME="${HOME:-/root}" ${vars[@]+"${vars[@]}"} docker compose \
		-f "$STATE/pending/set/docker-compose.yml" --project-directory "$DEPLOY_DIR" --env-file "$STATE/pending/set/.env" "$@")
}
# the stack as the new release's compose file has it, before it is the node's (begin)
BEGIN_WITH="" BEGIN_ENV=""
begun() {
	(cd "$DEPLOY_DIR" && env -i PATH="$PATH" HOME="${HOME:-/root}" docker compose \
		-f "$BEGIN_WITH" --project-directory "$DEPLOY_DIR" --env-file "$BEGIN_ENV" "$@")
}
env_of() { grep -E "^$1=" "$DEPLOY_DIR/.env" 2>/dev/null | sed -n 1p | cut -d= -f2- || true; }

# a service's containers by their labels, whatever the compose file says by now; -a: the ones that do
# not run too. It fails when docker does not answer.
containers() { # containers [-a] SERVICE
	local all=()
	if [ "$1" = -a ]; then all=(-a); shift; fi
	docker ps -q ${all[@]+"${all[@]}"} --filter "label=com.docker.compose.project=$PROJECT" --filter "label=com.docker.compose.service=$1"
}
running() { local ids; ids="$(containers "$1" 2>/dev/null)" && [ -n "$ids" ]; }
# stop_all SERVICE...: stopped and gone, and seen to be. Gone: docker starts at boot what it had not
# been told to stop (a container that had failed at that moment). Their last lines are kept.
stop_all() {
	local s c ids try left
	# a container that is restarting at that moment (it failed, and docker starts it again) can be
	# missed once
	for try in 1 2 3 4 5 6 7 8 9 10; do
		for s in "$@"; do
			ids="$(containers -a "$s" 2>/dev/null)" || ids=""
			for c in $ids; do
				docker stop -t 30 "$c" >/dev/null 2>&1 || true
				(umask 077; mkdir -p "$STATE/last-logs" && docker logs --tail 500 "$c" > "$STATE/last-logs/$s.log" 2>&1) 2>/dev/null || true
				docker rm -f "$c" >/dev/null 2>&1 || true
			done
		done
		left=""
		for s in "$@"; do
			# no answer is not "nothing runs"
			if ! ids="$(containers -a "$s" 2>/dev/null)"; then left="$left $s (docker does not answer)"
			elif [ -n "$ids" ]; then left="$left $s"
			fi
		done
		if [ -z "$left" ]; then
			sync # a removal half on the disk when the power goes is what heal_docker is for
			return 0
		fi
		sleep "$try"
	done
	err "still running:$left"
	return 1
}

# docker answers again (it was restarted under us): the daemon, and what it starts by itself
docker_ready() {
	local end=$((SECONDS + 120))
	while [ "$SECONDS" -lt "$end" ]; do
		if docker info >/dev/null 2>&1; then sleep 5; return 0; fi
		sleep 2
	done
	return 1
}

# The power went while a container was being removed: docker lists it and knows nothing of it ("No such
# container"), compose can do nothing for the stack while it is listed, and only its folder's removal
# and another start of the daemon end that. (Not `systemctl restart`, which stops the stack's
# unit with ExecStop. Docker's unit starts again by itself, and systemd may start the stack's again
# with it, this included: it can be run twice.)
heal_docker() {
	local root id pid half=() end
	root="$(docker info --format '{{.DockerRootDir}}' 2>/dev/null)" || return 0
	for id in $(docker ps -aq --no-trunc --filter "label=com.docker.compose.project=$PROJECT" 2>/dev/null); do
		if docker inspect --type container "$id" >/dev/null 2>&1; then continue; fi
		docker info >/dev/null 2>&1 || return 0 # no answer is not "no such container"
		if [ -n "$(docker ps -aq --no-trunc --filter "id=$id" 2>/dev/null)" ]; then half+=("$id"); fi
	done
	[ "${#half[@]}" -gt 0 ] || return 0
	log "docker lists ${#half[@]} container(s) that were half removed when the power went: out of its way, and docker starts again"
	for id in "${half[@]}"; do
		if [[ "$id" =~ ^[0-9a-f]{64}$ ]]; then rm -rf "${root:?}/containers/$id"; fi
	done
	sync
	pid="$(systemctl show -p MainPID --value docker)"
	if [ "${pid:-0}" -gt 1 ]; then kill -TERM "$pid" || true; fi
	end=$((SECONDS + 90))
	while [ "$SECONDS" -lt "$end" ] && [ "$(systemctl show -p MainPID --value docker)" = "$pid" ]; do sleep 1; done
	sleep 3 # its unit starts it again by itself
	systemctl is-active --quiet docker || systemctl start docker || true
	docker_ready
}

psql_as() { # psql_as COMPOSE(dc|saved) ENVFILE SQL
	local u d
	u="$(grep -E '^POSTGRES_USER=' "$2" | sed -n 1p | cut -d= -f2-)"; d="$(grep -E '^POSTGRES_DB=' "$2" | sed -n 1p | cut -d= -f2-)"
	"$1" exec -T postgres psql -X -q -A -t -v ON_ERROR_STOP=1 -U "${u:-maktaba}" -d "${d:-maktaba}" -c "$3" </dev/null
}

# the migrations the database records, one a line, in the order they were applied
applied() { # applied COMPOSE(dc|saved) ENVFILE
	psql_as "$1" "$2" \
		"SELECT CASE WHEN to_regclass('goose_db_version') IS NULL THEN '' ELSE (SELECT coalesce(string_agg(version_id::text, E'\n' ORDER BY id), '') FROM goose_db_version WHERE is_applied AND version_id > 0) END" |
		sed '/^$/d'
}

has_database() { docker volume inspect "${PROJECT}_pg_data" >/dev/null 2>&1; }
# a first deploy has nothing to go back to: no database was ever made here, or the only deploys
# there were failed as first ones and nothing serves. Anything else (a node set up by hand, one a
# person unheld or started) has a set worth saving, whether or not it serves at this moment.
first_deploy() {
	[ ! -f "$STATE/committed" ] || return 1
	if [ -f "$STATE/no-release" ] && ! running maktaba-cloud && ! running api; then return 0; fi
	! has_database
}

# --- the deploy under way ---------------------------------------------------------------------

valid_id() { [[ "${1:-}" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$ ]]; }
pending_id() { cat "$STATE/pending/id" 2>/dev/null || true; }
# need_this CMD ID: this deploy is the one under way
need_this() {
	valid_id "${2:-}" || die "$1: name the deploy"
	[ -d "$STATE/pending" ] || die "$1: no deploy has begun"
	[ "$(pending_id)" = "$2" ] || die "$1: the deploy under way is $(pending_id), not $2"
	[ ! -f "$STATE/held" ] || die "$1: the node is held"
}
lease() { printf '%s\n' "$(($(date +%s) + LEASE_SECONDS))" > "$STATE/pending/lease.new" && mv "$STATE/pending/lease.new" "$STATE/pending/lease"; }
lease_over() { [ "$(date +%s)" -gt "$(cat "$STATE/pending/lease" 2>/dev/null || echo 0)" ]; }

# vX.Y.Z@sha256:... as a number to compare ("" for anything else: a rehearsal's)
version_number() {
	[[ "${1:-}" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)(@|$) ]] || return 0
	printf '%d%05d%05d' "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}" "${BASH_REMATCH[3]}"
}

# the timer that brings back a deploy nobody works on any more
install_watch() {
	cat > /etc/systemd/system/maktaba-rollout-watch.service <<UNIT
[Unit]
Description=Maktaba Cloud: a deploy nobody works on any more goes back
After=docker.service

[Service]
Type=oneshot
ExecStart=$SELF watch
TimeoutStartSec=3600
UNIT
	cat > /etc/systemd/system/maktaba-rollout-watch.timer <<'UNIT'
[Unit]
Description=Maktaba Cloud: look after a deploy under way

[Timer]
OnBootSec=90
OnUnitInactiveSec=${WATCH_EVERY}

[Install]
WantedBy=timers.target
UNIT
	sed -i "s/\${WATCH_EVERY}/${WATCH_EVERY:-60}/" /etc/systemd/system/maktaba-rollout-watch.timer
	systemctl daemon-reload
	systemctl enable maktaba-rollout-watch.timer >/dev/null 2>&1
	systemctl restart maktaba-rollout-watch.timer
}

cmd_begin() {
	local src tmp refs ref room p try was now tables="" db=dc
	src="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
	valid_id "${DEPLOY_ID:-}" || die "begin: name the deploy (DEPLOY_ID)"
	if [ -z "${MAKTABA_CLOUD_TAG:-}" ] || [ -z "${MAKTABA_CADDY_TAG:-}" ]; then die "begin: MAKTABA_CLOUD_TAG and MAKTABA_CADDY_TAG are required"; fi
	case "$MAKTABA_CLOUD_TAG $MAKTABA_CADDY_TAG" in
		*@sha256:*\ *@sha256:*) ;;
		*) die "begin: the images are named by digest" ;;
	esac
	[ ! -f "$STATE/held" ] || { cat "$STATE/held" >&2; die "the node is held: a person decides first (maktaba-rollout unhold)"; }
	[ ! -d "$STATE/pending" ] || die "begin: a deploy is under way ($(pending_id)): nothing was changed"
	docker info >/dev/null 2>&1 || die "begin: docker does not answer: nothing was changed"
	heal_docker || die "begin: docker does not answer: nothing was changed"
	finish_commit
	# a name used before would be answered with that deploy's output
	if compgen -G "$STATE/jobs/$DEPLOY_ID.*" >/dev/null; then die "begin: deploy $DEPLOY_ID was here before: nothing was changed"; fi

	# a release older than the node's is never deployed (an old run started again, say): its code
	# may not know the schema, and a database never goes back by itself
	was="$(version_number "$(cat "$STATE/committed" 2>/dev/null || env_of MAKTABA_CLOUD_TAG)")"
	now="$(version_number "$MAKTABA_CLOUD_TAG")"
	if [ -n "$was" ] && [ -n "$now" ] && [ "$now" -lt "$was" ]; then
		die "begin: ${MAKTABA_CLOUD_TAG%%@*} is older than the node's release: nothing was changed"
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
		# what compose only needs to read the file (setup.sh makes the real ones, which win)
		printf 'MAKTABA_UID=%s\nMAKTABA_GID=%s\nPOSTGRES_PASSWORD=%s\n' 0 0 unused
		if [ -f "$DEPLOY_DIR/.env" ]; then cat "$DEPLOY_DIR/.env"; else cat "$src/.env.example"; fi
		for kv in "${!MAKTABA_@}" RELAY_DOMAIN API_DOMAIN APP_DOMAIN ACME_EMAIL RELAY_TLS; do
			[ -z "${!kv:-}" ] || printf '\n%s=%s\n' "$kv" "${!kv}"
		done
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
	cat /proc/sys/kernel/random/boot_id > "$STATE/pending.new/boot_id"
	printf '%s\n' "$DEPLOY_ID" > "$STATE/pending.new/id"
	# whatever there is in the database, before any deploy, a first one again included
	if has_database; then
		if ! dc up -d --wait --wait-timeout 180 postgres </dev/null >/dev/null 2>&1; then
			# a failed first deploy's own compose file may be what breaks it: the new release's then
			first_deploy || die "begin: the database does not come up: nothing was changed"
			BEGIN_WITH="$src/docker-compose.yml" BEGIN_ENV="$tmp/env" db=begun
			log "the database does not come up with the failed deploy's compose file: with this one's"
			begun up -d --wait --wait-timeout 180 postgres </dev/null >/dev/null 2>&1 || die "begin: the database does not come up: no file was changed"
		fi
		tables="$(psql_as "$db" "$DEPLOY_DIR/.env" "SELECT count(*) FROM pg_tables WHERE schemaname NOT IN ('pg_catalog', 'information_schema')")" || die "begin: the database can't be read: nothing was changed"
	fi
	if [ "$tables" = 0 ] && first_deploy; then
		log "the database is empty: nothing to back up" # a first deploy cut off before its migrations
	elif [ -n "$tables" ]; then
		DEPLOY_DIR="$DEPLOY_DIR" ROLLOUT_LOCKED=1 BACKUP_COMPOSE="$BEGIN_WITH" BACKUP_COMPOSE_ENV="$BEGIN_ENV" bash "$src/ops/backup.sh" || die "begin: the backup before the deploy failed: nothing was changed"
	fi
	if first_deploy; then
		touch "$STATE/pending.new/first"
		log "a first deploy: nothing to go back to"
	else
		dc up -d --wait postgres </dev/null >/dev/null 2>&1 || die "begin: the database does not come up: nothing was changed"
		applied dc "$DEPLOY_DIR/.env" > "$STATE/pending.new/applied"
		for p in "${SET[@]}"; do
			[ ! -e "$DEPLOY_DIR/$p" ] || cp -a "$DEPLOY_DIR/$p" "$STATE/pending.new/set/"
		done
		readlink "$DEPLOY_DIR/web/current" > "$STATE/pending.new/web" 2>/dev/null || : > "$STATE/pending.new/web"
	fi
	{
		printf 'MAKTABA_CLOUD_TAG=%s\n' "$MAKTABA_CLOUD_TAG"
		printf 'MAKTABA_CLOUD_IMAGE=%s\n' "${MAKTABA_CLOUD_IMAGE:-ghcr.io/hamza-labs-core/maktaba-cloud}"
	} > "$STATE/pending.new/release"
	printf '%s\n' "$(($(date +%s) + LEASE_SECONDS))" > "$STATE/pending.new/lease"
	# room kept for going back, should the disk fill meanwhile (restore gives it up first)
	mkdir -p "$DEPLOY_DIR"
	if ! fallocate -l 16M "$DEPLOY_DIR/.reserve" || ! fallocate -l 64M "$STATE/reserve"; then die "begin: no room to keep for going back: nothing was changed"; fi
	install_watch
	sync
	mv "$STATE/pending.new" "$STATE/pending"
	sync
	log "the present set is saved (deploy $DEPLOY_ID)"
}

cmd_setup() {
	need_this setup "${1:-}"
	lease
	log "setup.sh"
	(cd "$SRC_DIR" && SETUP_START=no bash setup.sh) || die "setup.sh failed"
}

cmd_web() {
	local t sha
	need_this web "${1:-}"
	lease
	t="$(mktemp -d "$DEPLOY_DIR/.web.XXXXXX")"
	# shellcheck disable=SC2064
	trap "rm -rf '$t'" EXIT
	cat > "$t/web.tar.gz"
	sha="$(sha256sum "$t/web.tar.gz" | cut -c1-16)"
	# the same bundle again: it is there whole (it arrived by one rename) and may be the one served
	if [ ! -d "$DEPLOY_DIR/web/releases/$sha" ]; then
		mkdir "$t/new"
		tar --no-same-owner -xzf "$t/web.tar.gz" -C "$t/new"
		[ -s "$t/new/index.html" ] || die "web: the app has no index.html"
		chmod -R a+rX "$t/new"
		mkdir -p "$DEPLOY_DIR/web/releases"
		mv -T "$t/new" "$DEPLOY_DIR/web/releases/$sha"
	fi
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
	need_this apply "${1:-}"
	touch "$STATE/pending/applying"
	log "the database and the cache"
	dc up -d --wait postgres redis </dev/null || fail "the database or the cache does not come up"
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
	lease || fail "the state folder can't be written"
	touch "$STATE/pending/live" || fail "the state folder can't be written"
	rm -f "$STATE/pending/applying"
	log "applied: $(env_of MAKTABA_CLOUD_TAG) (pending its tests)"
	echo "__maktaba_deploy_complete__"
}

STOPPED="The relay, api and worker are stopped."
NOT_STOPPED="NOT ALL of the relay, api and worker could be stopped yet (the node goes on trying): see docker ps."
# a held node whose app could not be stopped when it was held
stop_held() {
	local what
	[ -f "$STATE/not-stopped" ] || return 0
	mapfile -t what < "$STATE/not-stopped"
	stop_all "${what[@]}" || return 1
	sed -i "s/^NOT ALL of the relay.*see docker ps\.\( The database.*\)/$STOPPED\1/" "$STATE/held"
	rm -f "$STATE/not-stopped"
}
hold() { # hold WHY: stop the app, say why, leave the rest to a person
	local stopped="$STOPPED" what=("${APP[@]}")
	[ ! -f "$STATE/pending/first" ] || what+=(caddy)
	if ! stop_all "${what[@]}"; then
		# the watch, and a boot, go on trying
		stopped="$NOT_STOPPED"
		printf '%s\n' "${what[@]}" > "$STATE/not-stopped" || true
	fi
	{
		echo "HELD $(date -u +%FT%TZ): $1"
		echo "$stopped The database is as the migrations and any Down"
		echo "scripts left it; nothing was put back over it. The set from before the deploy is in"
		echo "$STATE/pending/set, its migrations in $STATE/pending/applied, and the backups in"
		echo "/var/backups/maktaba-cloud. When the node is right again: maktaba-rollout unhold."
	} > "$STATE/held.new"
	mv "$STATE/held.new" "$STATE/held"
	cat "$STATE/held" >&2
	return 1
}

put_back() { # the saved files and the web app's version, each copied whole before any replaces
	local p
	for p in "${SET[@]}"; do
		[ -e "$STATE/pending/set/$p" ] || continue
		rm -rf "${DEPLOY_DIR:?}/$p.restore" || return 1
		cp -a "$STATE/pending/set/$p" "$DEPLOY_DIR/$p.restore" || return 1
	done
	for p in "${SET[@]}"; do
		[ -e "$DEPLOY_DIR/$p.restore" ] || continue
		# a file by one rename (a backup reading .env finds the old or the new); a folder by two
		if [ -d "$DEPLOY_DIR/$p.restore" ] && [ -e "$DEPLOY_DIR/$p" ]; then
			rm -rf "${DEPLOY_DIR:?}/$p.replaced" || return 1
			mv -T "$DEPLOY_DIR/$p" "$DEPLOY_DIR/$p.replaced" || return 1
		fi
		mv -T "$DEPLOY_DIR/$p.restore" "$DEPLOY_DIR/$p" || return 1
		rm -rf "${DEPLOY_DIR:?}/$p.replaced" || return 1
	done
	if [ -s "$STATE/pending/web" ]; then
		web_to "$(cat "$STATE/pending/web")" || return 1
	else
		rm -f "$DEPLOY_DIR/web/current" || return 1
	fi
}

# gone in one rename: a half-removed saved set must never be found by a restore after a crash. What
# can't be removed afterwards is nobody's, and is tried again next time.
drop_pending() {
	mkdir -p "$STATE/dropped"
	sync # what was put back is on the disk before what it was put back from goes
	mv -T "$STATE/pending" "$STATE/dropped/$(date +%s).$$" || return 1
	sync
	rm -rf "$STATE/dropped" "$STATE/reserve" "$DEPLOY_DIR/.reserve" 2>/dev/null || true
}

WHY_NOT=""
# not_now WHY: this pass can't; another may (docker was restarted meanwhile)
not_now() { WHY_NOT="$1"; err "$1"; return 1; }

# one pass at going back: 0 done, 1 not now (WHY_NOT), 2 never (WHY_NOT)
restore_once() {
	local added rc release=()
	# the migrations this deploy applied (by apply, or by a role that started on its own), newest
	# first; once, not again after a crash further on
	if [ ! -f "$STATE/pending/settled" ]; then
		saved up -d --wait postgres </dev/null >/dev/null 2>&1 || { not_now "the database does not come up"; return 1; }
		# nothing may apply one again, or write to what goes: a starting role migrates
		stop_all "${APP[@]}" || { not_now "the app does not stop"; return 1; }
		applied saved "$STATE/pending/set/.env" > "$STATE/pending/applied.now" || { not_now "the applied migrations can't be read"; return 1; }
		added="$(comm -13 <(sort "$STATE/pending/applied") <(sort "$STATE/pending/applied.now") | tr '\n' ' ')" || { not_now "the migrations can't be compared"; return 1; }
		if [ -n "${added// /}" ]; then
			log "undoing the migrations it applied: $added"
			mapfile -t release < "$STATE/pending/release"
			rc=0
			# shellcheck disable=SC2086  # a list of numbers
			saved "${release[@]}" run --rm -T --no-deps maktaba-cloud migrate --config /etc/maktaba/cloud/cloud.toml undo $added </dev/null || rc=$?
			case "$rc" in
				0) log "undone by their Down scripts" ;;
				3) err "a Down script would delete rows written since: the schema is KEPT, and the previous release runs on it" ;;
				4) WHY_NOT="a migration it applied can't be undone, and the previous release may not run on what it left"; return 2 ;;
				*) not_now "a Down script failed (exit $rc)"; return 1 ;;
			esac
		fi
		touch "$STATE/pending/settled" || { not_now "the state folder can't be written"; return 1; }
		sync
	fi
	put_back || { not_now "the saved files can't be put back"; return 1; }
	dc up -d --wait --remove-orphans postgres redis </dev/null || { not_now "the previous database or cache does not come up"; return 1; }
	dc up -d --wait --force-recreate "${APP[@]}" caddy </dev/null || { not_now "the previous release does not come up"; return 1; }
	WATCH_SECONDS=10 verify || { not_now "the previous release is not healthy"; return 1; }
}

restore() { # restore WHY: back to the saved set; 0 restored (or nothing to), 1 held
	# every step checks itself: this runs where errexit does not (restore ... || ...). Each can be
	# done again: a crash part way is followed by another restore (the watch, a boot)
	local why="$1" rc
	[ -d "$STATE/pending" ] || { log "nothing to restore"; return 0; }
	log "restoring the set from before the deploy ($why)"
	rm -f "$STATE/reserve" "$DEPLOY_DIR/.reserve"
	touch "$STATE/pending/restoring" || { hold "the state folder can't be written ($why)"; return 1; }
	# a commit of it that was cut off before the saved set went is undone with it
	if [ "$(cat "$STATE/committed-id" 2>/dev/null)" = "$(pending_id)" ]; then rm -f "$STATE/committed.new" "$STATE/committed-id"; fi
	heal_docker || true
	if [ -f "$STATE/pending/first" ]; then
		if ! stop_all "${APP[@]}" caddy; then
			log "once more, when docker answers"
			docker_ready || true
			stop_all "${APP[@]}" caddy || { hold "a failed first deploy does not stop ($why)"; return 1; }
		fi
		touch "$STATE/no-release" || { hold "the state folder can't be written ($why)"; return 1; }
		drop_pending || { hold "the saved set can't be dropped ($why)"; return 1; }
		err "a first deploy failed: there is no earlier release to go back to, so the stack is stopped, and stays so at the next boot"
		return 0
	fi
	rc=0
	restore_once || rc=$?
	if [ "$rc" = 1 ]; then
		log "once more, when docker answers"
		docker_ready || true
		heal_docker || true
		rc=0
		restore_once || rc=$?
	fi
	[ "$rc" = 0 ] || { hold "$WHY_NOT ($why)"; return 1; }
	drop_pending || { hold "the saved set can't be dropped ($why)"; return 1; }
	log "restored: $(env_of MAKTABA_CLOUD_TAG) serves again"
	return 0
}

cmd_rollback() {
	valid_id "${1:-}" || die "rollback: name the deploy"
	[ ! -f "$STATE/held" ] || { cat "$STATE/held" >&2; exit 1; }
	[ -d "$STATE/pending" ] || { log "nothing to restore"; return 0; }
	if [ "$1" != "$(pending_id)" ]; then
		log "the deploy under way is $(pending_id), not $1: nothing to restore"
		return 0
	fi
	restore "${2:-tests failed, or the run stopped}"
}

# the last line of a commit, also after a crash just before it (the saved set is gone by then)
finish_commit() {
	if [ ! -s "$STATE/committed.new" ] || [ -d "$STATE/pending" ]; then return 0; fi
	mv "$STATE/committed.new" "$STATE/committed"
	rm -f "$STATE/no-release"
}

cmd_commit() {
	local cur
	valid_id "${1:-}" || die "commit: name the deploy"
	finish_commit
	# asked again after its answer was lost
	if [ ! -d "$STATE/pending" ] && [ "$(cat "$STATE/committed-id" 2>/dev/null)" = "$1" ]; then
		log "committed already: $(cat "$STATE/committed")"
		return 0
	fi
	need_this commit "$1"
	[ -f "$STATE/pending/live" ] || die "commit: the deploy was not applied"
	if [ -f "$STATE/pending/applying" ] || [ -f "$STATE/pending/restoring" ]; then die "commit: the deploy is being restored"; fi
	printf '%s\n' "$1" > "$STATE/committed-id"
	# the power may go at any moment from here: the saved set is there until both names are
	env_of MAKTABA_CLOUD_TAG > "$STATE/committed.new"
	if [ ! -s "$STATE/committed.new" ] || [ "$(cat "$STATE/committed-id")" != "$1" ]; then die "commit: the state folder can't be written"; fi
	sync
	drop_pending
	finish_commit
	# the web app's other versions, and the layout before versions (files right in web/)
	cur="$(readlink "$DEPLOY_DIR/web/current" 2>/dev/null || true)"
	if [ -n "$cur" ] && [ "$cur" != . ]; then
		find "$DEPLOY_DIR/web" -mindepth 1 -maxdepth 1 ! -name releases ! -name current -exec rm -rf {} + || true
		find "$DEPLOY_DIR/web/releases" -mindepth 1 -maxdepth 1 ! -name "${cur##*/}" -exec rm -rf {} + || true
	fi
	# images no container uses: the last release's, and any older
	docker image prune -af >/dev/null 2>&1 || true
	rm -rf "${SRC_DIR:?}"
	find "$STATE/jobs" -mindepth 1 -maxdepth 1 -mtime +14 -exec rm -rf {} + 2>/dev/null || true
	log "committed: $(cat "$STATE/committed")"
}

cmd_renew() {
	need_this renew "${1:-}"
	lease
}

# --- jobs: a command as a unit of the node's own ----------------------------------------------

# job ID CMD FROM: starts CMD of deploy ID once, then follows its output from line FROM to its end
cmd_job() {
	local id="${1:-}" cmd="${2:-}" from="${3:-0}" dir unit lines rc gone=0
	valid_id "$id" || die "job: name the deploy"
	case "$cmd" in apply | rollback | commit) ;; *) die "job: apply, rollback or commit" ;; esac
	[[ "$from" =~ ^[0-9]+$ ]] || die "job: from which line?"
	dir="$STATE/jobs/$id.$cmd"
	unit="maktaba-rollout-$cmd-$id"
	# going back must start on a full disk too: the room begin kept is for it
	if [ "$cmd" = rollback ] && [ "$(pending_id)" = "$id" ]; then rm -f "$STATE/reserve" "$DEPLOY_DIR/.reserve"; fi
	(umask 077; mkdir -p "$STATE/jobs") || die "job: the state folder can't be written"
	if mkdir "$dir" 2>/dev/null; then
		if ! : > "$dir/log" || ! systemd-run --quiet --collect --unit="$unit" --property=RuntimeMaxSec=3000 --property=KillMode=mixed \
			--setenv=DEPLOY_DIR="$DEPLOY_DIR" --setenv=WATCH_SECONDS="$WATCH_SECONDS" --setenv=LEASE_SECONDS="$LEASE_SECONDS" \
			--setenv=HOME="${HOME:-/root}" "$SELF" run-job "$dir" "$cmd" "$id"; then
			rm -rf "$dir"
			die "job: $cmd could not be started"
		fi
	fi
	[ -f "$dir/log" ] || die "job: $cmd could not be started"
	while :; do
		# the code first: lines written before it are all there once it is
		rc="$(cat "$dir/rc" 2>/dev/null || true)"
		lines="$(wc -l < "$dir/log")"
		if [ "$lines" -gt "$from" ]; then
			sed -n "$((from + 1)),${lines}p" "$dir/log"
			from="$lines"
		fi
		[ -z "$rc" ] || exit "$rc"
		# the unit ended without leaving a code (killed, or it never ran)
		if systemctl is-active --quiet "$unit" || [ -f "$dir/rc" ]; then gone=0; else gone=$((gone + 1)); fi
		[ "$gone" -lt 10 ] || { err "$cmd was stopped part way: the watch brings the node to a known state"; exit 70; }
		sleep 1
	done
}

# the unit's own command: CMD's output and code, kept for whoever follows it
cmd_run_job() {
	local dir="$1" cmd="$2" id="$3" rc=0
	touch "$dir/started"
	"$SELF" "$cmd" "$id" >> "$dir/log" 2>&1 </dev/null || rc=$?
	# above 128 is what a cut or killed ssh ends with (deploy.sh)
	[ "$rc" -le 125 ] || rc=1
	printf '%s\n' "$rc" > "$dir/rc.new"
	mv "$dir/rc.new" "$dir/rc"
}

cmd_watch() {
	if [ ! -d "$STATE/pending" ] && [ ! -f "$STATE/held" ]; then exit 0; fi
	if [ -f "$STATE/held" ]; then
		stop_held || true
		exit 0
	fi
	if [ -f "$STATE/pending/applying" ] || [ -f "$STATE/pending/restoring" ]; then
		restore "a command of the deploy was stopped part way" || true
	elif lease_over; then
		restore "no word from the deploy for $((LEASE_SECONDS / 60)) minutes" || true
	fi
}

cmd_boot() {
	if [ -f "$STATE/held" ]; then
		stop_held || true
		cat "$STATE/held" >&2
		exit 1
	fi
	if [ ! -d "$STATE/pending" ]; then
		finish_commit # the power went before a commit's last line reached the disk
		nothing_to_start
		heal_docker || true
		exit 0
	fi
	if [ "$(cat "$STATE/pending/boot_id")" = "$(cat /proc/sys/kernel/random/boot_id)" ] && [ ! -f "$STATE/pending/restoring" ]; then
		# started again in the same boot (docker restarted): what was applied goes on; what was not
		# yet, apply starts
		[ -f "$STATE/pending/live" ] || exit 1
		exit 0
	fi
	log "the node rebooted during a deploy"
	restore "the node rebooted during the deploy" || exit 1
	nothing_to_start
	exit 0
}

# a node whose only deploys failed: the release on it was refused, and starts for nobody
nothing_to_start() {
	if [ -f "$STATE/no-release" ] && [ ! -f "$STATE/committed" ]; then
		log "no release was ever committed here, and the last deploy failed: nothing is started"
		exit 1
	fi
}

cmd_unhold() {
	[ -f "$STATE/held" ] || die "the node is not held"
	if [ -d "$STATE/pending" ]; then
		rm -rf "$STATE/held-set"
		mv -T "$STATE/pending" "$STATE/held-set"
	fi
	# the node is as it is now, whether or not a first deploy failed there before
	rm -f "$STATE/held" "$STATE/not-stopped" "$STATE/no-release" "$STATE/reserve" "$DEPLOY_DIR/.reserve"
	log "not held any more; the set from before the failed deploy is in $STATE/held-set"
}

cmd_status() {
	if [ -f "$STATE/held" ]; then cat "$STATE/held"; return 0; fi
	if [ -d "$STATE/pending" ]; then echo "pending: deploy $(pending_id) began and is neither committed nor rolled back"; return 0; fi
	echo "committed: $(cat "$STATE/committed" 2>/dev/null || echo none)"
}

# begin alone: an earlier deploy that was cut off is undone first, as a unit of its own (this
# session may be cut too)
undo_cut_off() {
	local id
	if [ ! -d "$STATE/pending" ] || [ -f "$STATE/held" ]; then return 0; fi
	id="$(pending_id)"
	valid_id "$id" || id=unnamed
	log "an earlier deploy ($id) was cut off: restoring what was before it"
	[ "$id" != unnamed ] || printf '%s\n' "$id" > "$STATE/pending/id"
	# a rollback of it that ran and ended before is not followed again
	rm -rf "$STATE/jobs/$id.rollback.old"
	[ ! -f "$STATE/jobs/$id.rollback/rc" ] || mv -T "$STATE/jobs/$id.rollback" "$STATE/jobs/$id.rollback.old"
	"$SELF" job "$id" rollback 0 || exit 1
	[ ! -d "$STATE/pending" ] || die "begin: the earlier deploy is still under way: nothing was changed"
}

main() {
	[ "$(id -u)" -eq 0 ] || die "run as root on the node"
	(umask 077; mkdir -p "$STATE")
	chmod 0700 "$STATE"
	local c="${1:-}"
	if [ "$c" = begin ]; then
		install -m 0755 "${BASH_SOURCE[0]}" "$SELF.new" && mv "$SELF.new" "$SELF"
		undo_cut_off
	fi
	case "$c" in
		begin | setup | web | apply | commit | rollback | unhold)
			exec 9>"$STATE/lock"
			# a rollback waits for what is at work (an apply whose run was cancelled) to end
			flock -w "$([ "$c" = rollback ] && echo 2400 || echo "$LOCK_WAIT")" 9 || die "another deploy command is at work"
			;;
		watch)
			exec 9>"$STATE/lock"
			flock -n 9 || exit 0
			;;
		boot)
			# a command at work starts the stack itself, the one it is at; the unit's start would
			# start another. The watch's look takes a moment: it is waited for.
			exec 9>"$STATE/lock"
			# 255: systemd tries again (Restart=on-failure); any other code would skip the unit for good
			flock -w "${BOOT_WAIT:-30}" 9 || exit 255
			;;
	esac
	shift || true
	case "$c" in
		begin) cmd_begin ;;
		setup) cmd_setup "$@" ;;
		web) cmd_web "$@" ;;
		apply) cmd_apply "$@" ;;
		commit) cmd_commit "$@" ;;
		rollback) cmd_rollback "$@" ;;
		renew) cmd_renew "$@" ;;
		job) cmd_job "$@" ;;
		run-job) cmd_run_job "$@" ;;
		watch) cmd_watch ;;
		boot) cmd_boot ;;
		unhold) cmd_unhold ;;
		status) cmd_status ;;
		*) die "usage: rollout.sh begin | setup ID | web ID | job ID apply|rollback|commit FROM | renew ID | watch | boot | unhold | status" ;;
	esac
}

# read whole before it runs: restoring replaces files, and begin replaces this one
main "$@"
exit $?

#!/usr/bin/env bash
# Maktaba Cloud Relay — VPS provisioning.
#
# Idempotent. Re-running is safe: it skips work already done and never
# overwrites secrets it previously generated.
#
#   Ubuntu 22.04 / 24.04, run as root:
#     curl -fsSL https://.../setup.sh | sudo bash          # not recommended
#     # or, preferred — clone the repo and run from this dir:
#     sudo ./setup.sh
#
# What it does:
#   1. installs Docker Engine + compose plugin
#   2. creates the `maktaba` system user (in the docker group)
#   3. lays the stack down in /opt/maktaba-relay
#   4. generates the token secret, postgres password, and Ed25519
#      entitlement key if not already set
#   5. installs a systemd unit that runs `docker compose up`
#   6. caps container log growth (docker json-file rotation)
#   7. configures UFW to allow only SSH + 80 + 443
#   8. installs the nightly database backup (maktaba-cloud-backup.timer)
#   9. pulls images and brings the stack up (SETUP_START=no: only enables it at boot;
#      the CI deploy starts it, ops/rollout.sh)
#
# Settings a deploy hands in (.github/workflows/cloud-deploy.yml), written into .env when set
# in this script's environment, never printed: RELAY_DOMAIN, API_DOMAIN, APP_DOMAIN,
# ACME_EMAIL, RELAY_TLS, RELAY_PLANS_ENFORCE, MAKTABA_CLOUD_TAG, MAKTABA_CADDY_TAG (and
# MAKTABA_CLOUD_IMAGE, MAKTABA_CADDY_IMAGE: another registry, the rehearsal's), MAIL_ALLOW,
# MAIL_SUBJECT_PREFIX, MAIL_DAILY_LIMIT, MAIL_MONTHLY_LIMIT and the secrets CF_API_TOKEN,
# LICENSE_SIGNING_KEY and RESEND_API_KEY (set but empty: this node has none any more).
set -euo pipefail

DEPLOY_DIR=${DEPLOY_DIR:-/opt/maktaba-relay}
SERVICE_USER=maktaba
SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!! \033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mxx \033[0m %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "must run as root (use sudo)."

# ---------------------------------------------------------------------
# 0. Sanity: supported Ubuntu
# ---------------------------------------------------------------------
if [ -r /etc/os-release ]; then
	# shellcheck disable=SC1091
	. /etc/os-release
	case "${VERSION_ID:-}" in
		22.04|24.04) log "Ubuntu ${VERSION_ID} detected." ;;
		*) warn "Untested on '${PRETTY_NAME:-unknown}'. Proceeding anyway." ;;
	esac
else
	warn "Cannot read /etc/os-release; assuming a Debian-family host."
fi

# ---------------------------------------------------------------------
# 1. Docker Engine + compose plugin
# ---------------------------------------------------------------------
if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
	log "Docker + compose plugin already installed."
else
	log "Installing Docker Engine + compose plugin..."
	export DEBIAN_FRONTEND=noninteractive
	apt-get update -y
	apt-get install -y ca-certificates curl gnupg openssl ufw
	# Ubuntu's own packages first: they follow the release's security updates
	# (unattended-upgrades) and exist on a release Docker's repository has not reached yet
	# (26.04 when the cloud's servers were set up). Docker's convenience script otherwise.
	if apt-cache show docker-compose-v2 >/dev/null 2>&1; then
		apt-get install -y docker.io docker-compose-v2
	else
		curl -fsSL https://get.docker.com -o /tmp/get-docker.sh
		sh /tmp/get-docker.sh
		rm -f /tmp/get-docker.sh
	fi
	systemctl enable --now docker
fi
# a node's first deploy pulls its images before anything else is set up (ops/rollout.sh begin)
[ "${SETUP_DOCKER_ONLY:-no}" != yes ] || exit 0
# openssl/ufw may be needed even when docker was pre-installed.
command -v openssl >/dev/null 2>&1 || { apt-get update -y && apt-get install -y openssl; }
command -v ufw     >/dev/null 2>&1 || { apt-get update -y && apt-get install -y ufw; }

# ---------------------------------------------------------------------
# 2. Service user
# ---------------------------------------------------------------------
if id "$SERVICE_USER" >/dev/null 2>&1; then
	log "User '$SERVICE_USER' already exists."
else
	log "Creating system user '$SERVICE_USER'..."
	useradd --system --create-home --shell /usr/sbin/nologin "$SERVICE_USER"
fi
usermod -aG docker "$SERVICE_USER"

# ---------------------------------------------------------------------
# 3. Lay down the stack
# ---------------------------------------------------------------------
log "Staging stack in $DEPLOY_DIR ..."
mkdir -p "$DEPLOY_DIR/secrets"
mkdir -p "$DEPLOY_DIR/web" "$DEPLOY_DIR/ops"
install -m 0644 "$SRC_DIR/docker-compose.yml"  "$DEPLOY_DIR/docker-compose.yml"
install -m 0644 "$SRC_DIR/Caddyfile"           "$DEPLOY_DIR/Caddyfile"
install -m 0644 "$SRC_DIR/cloud.toml.template" "$DEPLOY_DIR/cloud.toml.template"
install -m 0755 "$SRC_DIR/ops/cloud-ops.py"    "$DEPLOY_DIR/ops/cloud-ops.py"
install -m 0755 "$SRC_DIR/ops/backup.sh"       "$DEPLOY_DIR/ops/backup.sh"
# the deploy that can go back, outside the stack it saves (a CI deploy's `begin` installed it)
install -m 0755 "$SRC_DIR/ops/rollout.sh"      /usr/local/sbin/maktaba-rollout
# the cloud web app's page until a deploy switches to its own (web/current, ops/rollout.sh)
if [ ! -e "$DEPLOY_DIR/web/current" ] && [ -s "$DEPLOY_DIR/web/index.html" ]; then
	# a node from before the web app had versions: its files, right in web/, are served until then
	ln -s . "$DEPLOY_DIR/web/current"
elif [ ! -e "$DEPLOY_DIR/web/current" ]; then
	mkdir -p "$DEPLOY_DIR/web/releases/none"
	printf '%s\n' '<!doctype html><title>Maktaba Cloud</title>' > "$DEPLOY_DIR/web/releases/none/index.html"
	ln -s releases/none "$DEPLOY_DIR/web/current"
fi

# .env: create from template on first run; never clobber an existing one.
if [ ! -f "$DEPLOY_DIR/.env" ]; then
	install -m 0600 "$SRC_DIR/.env.example" "$DEPLOY_DIR/.env"
	log "Created $DEPLOY_DIR/.env from template — EDIT RELAY_DOMAIN + ACME_EMAIL."
fi


# ---------------------------------------------------------------------
# 4. Generate secrets (only if unset / placeholder)
# ---------------------------------------------------------------------
ENV_FILE="$DEPLOY_DIR/.env"

# get_env KEY -> current value (empty if unset)
get_env() { grep -E "^$1=" "$ENV_FILE" | sed -n 1p | cut -d= -f2- || true; }

# set_env KEY VALUE — replace in place (value used verbatim, so base64
# `+/=` are safe) or append if absent.
set_env() {
	local k="$1" v="$2" tmp
	tmp="$(mktemp)"
	awk -v k="$k" -v v="$v" '
		BEGIN { FS = "="; done = 0 }
		$1 == k { print k "=" v; done = 1; next }
		{ print }
		END { if (!done) print k "=" v }
	' "$ENV_FILE" > "$tmp"
	mv "$tmp" "$ENV_FILE"
}

# is_placeholder VALUE -> true if empty or a known template stand-in.
is_placeholder() {
	case "$1" in
		""|CHANGEME|changeme) return 0 ;;
		*) return 1 ;;
	esac
}

# The settings a deploy hands in (see the header): each one set in this script's environment
# replaces its line in .env. CF_API_TOKEN is a secret: written, never echoed.
for k in RELAY_DOMAIN API_DOMAIN APP_DOMAIN ACME_EMAIL RELAY_TLS RELAY_PLANS_ENFORCE \
	MAKTABA_CLOUD_TAG MAKTABA_CADDY_TAG MAKTABA_CLOUD_IMAGE MAKTABA_CADDY_IMAGE CF_API_TOKEN; do
	if [ -n "${!k:-}" ]; then
		set_env "$k" "${!k}"
	fi
done
# (a mail setting handed in empty is none: a line taken out of the target's file does not stay)
for k in MAIL_ALLOW MAIL_SUBJECT_PREFIX MAIL_DAILY_LIMIT MAIL_MONTHLY_LIMIT; do
	if [ -n "${!k+x}" ]; then
		set_env "$k" "${!k}"
	fi
done
if [ -n "$(get_env RELAY_DOMAIN)" ]; then
	set_env MAKTABA_CLOUD_PUBLIC_URL "https://$(get_env RELAY_DOMAIN)"
fi
# the cloud's containers run as the maktaba user, which owns the secrets mount
set_env MAKTABA_UID "$(id -u "$SERVICE_USER")"
set_env MAKTABA_GID "$(id -g "$SERVICE_USER")"

# Token secret (>= 32 bytes). openssl base64 of 48 random bytes => 64
# chars, all ASCII.
if is_placeholder "$(get_env MAKTABA_CLOUD_TOKEN_SECRET)"; then
	log "Generating MAKTABA_CLOUD_TOKEN_SECRET..."
	set_env MAKTABA_CLOUD_TOKEN_SECRET "$(openssl rand -base64 48 | tr -d '\n')"
fi

# The operator console's staff keys (story 50.33): its access-token secret (a different key from
# the customers') and the 32-byte key that seals each staff TOTP secret. The api role needs
# both when it serves the admin listener; the relay role ignores them.
if is_placeholder "$(get_env MAKTABA_CLOUD_STAFF_TOKEN_SECRET)"; then
	log "Generating MAKTABA_CLOUD_STAFF_TOKEN_SECRET..."
	set_env MAKTABA_CLOUD_STAFF_TOKEN_SECRET "$(openssl rand -base64 48 | tr -d '\n')"
fi
if is_placeholder "$(get_env MAKTABA_CLOUD_STAFF_SECRET_KEY)"; then
	log "Generating MAKTABA_CLOUD_STAFF_SECRET_KEY..."
	set_env MAKTABA_CLOUD_STAFF_SECRET_KEY "$(openssl rand -hex 32)"
fi

# Postgres password — hex so it needs no URL-encoding in the DB URL.
# Keep POSTGRES_PASSWORD and MAKTABA_CLOUD_DB_URL in lock-step.
if is_placeholder "$(get_env POSTGRES_PASSWORD)"; then
	log "Generating POSTGRES_PASSWORD + matching MAKTABA_CLOUD_DB_URL..."
	pg_pass="$(openssl rand -hex 24)"
	pg_user="$(get_env POSTGRES_USER)"; pg_user="${pg_user:-maktaba}"
	pg_db="$(get_env POSTGRES_DB)";     pg_db="${pg_db:-maktaba}"
	set_env POSTGRES_PASSWORD "$pg_pass"
	set_env MAKTABA_CLOUD_DB_URL \
		"postgres://${pg_user}:${pg_pass}@postgres:5432/${pg_db}?sslmode=disable"
fi

# Ed25519 entitlement signing key. A seed is just 32 random bytes; the
# cloud loader (entitlement.LoadSignerFromFile) accepts a base64'd
# 32-byte seed. Used by --role api; harmless to pre-provision for relay.
ENTITLEMENT_KEY="$DEPLOY_DIR/secrets/entitlement.key"
# The licence signing key (docs/licensing.md, "The key"): releases embed its public half
# (MAKTABA_LICENSE_PUBKEY), so a home trusts only licences signed with it. A deploy hands it in
# as LICENSE_SIGNING_KEY (a hex seed, never printed); it replaces a generated key, and the api
# restarts on it below. Without it a node keeps its own key, which no released home trusts.
KEY_CHANGED=no
if [ -n "${LICENSE_SIGNING_KEY:-}" ]; then
	( umask 077; printf '%s' "$LICENSE_SIGNING_KEY" > "$ENTITLEMENT_KEY.new" )
	if [ -s "$ENTITLEMENT_KEY" ] && cmp -s "$ENTITLEMENT_KEY.new" "$ENTITLEMENT_KEY"; then
		rm -f "$ENTITLEMENT_KEY.new"
	else
		mv "$ENTITLEMENT_KEY.new" "$ENTITLEMENT_KEY"
		KEY_CHANGED=yes
		log "Installed the licence signing key -> secrets/entitlement.key"
	fi
fi
if [ ! -s "$ENTITLEMENT_KEY" ]; then
	log "Generating Ed25519 entitlement key -> secrets/entitlement.key..."
	openssl rand 32 | base64 | tr -d '\n' > "$ENTITLEMENT_KEY"
fi

# The cloud's log key (Epic 53 story 53.01): 32 raw random bytes the shared logger
# hashes ids with, so no log line names an account. Without it a line drops the id.
LOG_KEY="$DEPLOY_DIR/secrets/log.key"
if [ ! -s "$LOG_KEY" ]; then
	log "Generating the log key -> secrets/log.key..."
	openssl rand 32 > "$LOG_KEY"
fi

# --- mail: begin (tools/ci/tests/test_cloud_deploy.py runs this block)
# The mail provider's key (docs/cloud-mail.md), never printed: the api reads the file at each
# send. The node sends mail only with a key, a stated MAIL_ALLOW ("everyone", or the addresses
# and @domains it may write to) and a release that can (asked of its image: an older one refuses
# to start on a driver it does not know); else its driver stays log, and this says so.
RESEND_KEY="$DEPLOY_DIR/secrets/resend.key"
if [ -n "${RESEND_API_KEY:-}" ]; then
	[[ "$RESEND_API_KEY" == re_* ]] || die "RESEND_API_KEY is not a Resend key (it begins with re_)"
	( umask 077; printf '%s' "$RESEND_API_KEY" > "$RESEND_KEY.new" )
	mv "$RESEND_KEY.new" "$RESEND_KEY"
elif [ -n "${RESEND_API_KEY+x}" ]; then
	rm -f "$RESEND_KEY"
fi
# release_sends: the release's image says it has the driver (one from before the command serves,
# without a role: it exits)
release_sends() {
	local image tag out
	image="$(get_env MAKTABA_CLOUD_IMAGE)"; tag="$(get_env MAKTABA_CLOUD_TAG)"
	out="$(timeout 60 docker run --rm --network none "${image:-ghcr.io/hamza-labs-core/maktaba-cloud}:${tag:-latest}" mail drivers 2>/dev/null </dev/null)" || return 1
	[[ $'\n'"$out"$'\n' == *$'\nresend\n'* ]]
}
MAIL_DRIVER=log
MAIL_ALLOW_TOML=""
mail_to=everyone
mail_allow="$(get_env MAIL_ALLOW)"
mail_prefix="$(get_env MAIL_SUBJECT_PREFIX)"
mail_day="$(get_env MAIL_DAILY_LIMIT)"; mail_day="${mail_day:-80}"
mail_month="$(get_env MAIL_MONTHLY_LIMIT)"; mail_month="${mail_month:-2700}"
mail_entry='[A-Za-z0-9._+-]*@[A-Za-z0-9-]+([.][A-Za-z0-9-]+)+'
[ -z "$mail_allow" ] || [[ "$mail_allow" =~ ^(everyone|$mail_entry(,$mail_entry)*)$ ]] || die "MAIL_ALLOW is everyone, or addresses and @domains with commas"
[[ "$mail_prefix" =~ ^[][A-Za-z0-9\ ._-]*$ ]] || die "MAIL_SUBJECT_PREFIX holds a character it may not"
for n in "$mail_day" "$mail_month"; do
	[[ "$n" =~ ^[1-9][0-9]*$ ]] || die "MAIL_DAILY_LIMIT and MAIL_MONTHLY_LIMIT are numbers above 0"
done
if [ -n "$mail_allow" ] && [ "$mail_allow" != everyone ]; then
	mail_to="the listed ones only"
	MAIL_ALLOW_TOML="\"${mail_allow//,/\", \"}\""
fi
if [ ! -s "$RESEND_KEY" ]; then
	warn "mail: no RESEND_API_KEY for this node: the mail driver is log (mails go to the api's log, nothing is delivered)"
elif [ -z "$mail_allow" ]; then
	warn "mail: MAIL_ALLOW is not set: the mail driver is log (nothing is delivered)"
elif ! release_sends; then
	warn "mail: this release has no resend driver: the mail driver is log (nothing is delivered)"
else
	MAIL_DRIVER=resend
	log "mail: the driver is resend (recipients: $mail_to; at most $mail_day a day, $mail_month in 30 days)"
fi
# --- mail: end

# cloud.toml, from its template and .env's names (the compose mounts it read-only into every
# role). Only the named placeholders are replaced.
RELAY_PLANS_ENFORCE_V="$(get_env RELAY_PLANS_ENFORCE)"
case "${RELAY_PLANS_ENFORCE_V:-false}" in true|false) ;; *) die "RELAY_PLANS_ENFORCE is true or false" ;; esac
sed -e "s|\${RELAY_DOMAIN}|$(get_env RELAY_DOMAIN)|g" \
	-e "s|\${API_DOMAIN}|$(get_env API_DOMAIN)|g" \
	-e "s|\${APP_DOMAIN}|$(get_env APP_DOMAIN)|g" \
	-e "s|\${RELAY_PLANS_ENFORCE}|${RELAY_PLANS_ENFORCE_V:-false}|g" \
	-e "s|\${MAIL_DRIVER}|$MAIL_DRIVER|g" \
	-e "s|\${MAIL_ALLOW}|$MAIL_ALLOW_TOML|g" \
	-e "s|\${MAIL_SUBJECT_PREFIX}|$mail_prefix|g" \
	-e "s|\${MAIL_DAILY_LIMIT}|$mail_day|g" \
	-e "s|\${MAIL_MONTHLY_LIMIT}|$mail_month|g" \
	"$DEPLOY_DIR/cloud.toml.template" > "$DEPLOY_DIR/cloud.toml.new"
mv "$DEPLOY_DIR/cloud.toml.new" "$DEPLOY_DIR/cloud.toml"
# what the containers read as files: compose recreates a container when its file changed
set_env MAKTABA_CONFIG_SHA "$(sha256sum "$DEPLOY_DIR/cloud.toml" | cut -c1-16)"
set_env MAKTABA_CADDYFILE_SHA "$(sha256sum "$DEPLOY_DIR/Caddyfile" | cut -c1-16)"

# ---------------------------------------------------------------------
# 5. Ownership + permissions (secrets are sensitive)
# ---------------------------------------------------------------------
chown -R "$SERVICE_USER:$SERVICE_USER" "$DEPLOY_DIR"
# the operator tool's own state stays root's alone (ops/cloud-ops.py)
if [ -d "$DEPLOY_DIR/ops-state" ]; then
	chown -R root:root "$DEPLOY_DIR/ops-state"
	chmod 0700 "$DEPLOY_DIR/ops-state"
fi
chmod 0700 "$DEPLOY_DIR/secrets"
chmod 0600 "$ENV_FILE" "$ENTITLEMENT_KEY" "$LOG_KEY"

# ---------------------------------------------------------------------
# 6. Container log rotation (docker json-file driver caps)
# ---------------------------------------------------------------------
# merged into the file there may be: what it sets already stays, the caps are added when it has
# none. A log driver of its own is left alone, with a warning. Docker restarts on a change only.
DAEMON_JSON=/etc/docker/daemon.json
mkdir -p /etc/docker
case "$(python3 - "$DAEMON_JSON" <<'PY'
import json, os, sys
p = sys.argv[1]
text = open(p, encoding="utf-8").read() if os.path.exists(p) else ""
try:
    have = json.loads(text) if text.strip() else {}
except ValueError:
    sys.exit("daemon.json is not JSON: " + p)
if have.get("log-driver", "json-file") not in ("json-file", "local"):
    print("driver")
    sys.exit(0)
want = dict(have)
want.setdefault("log-driver", "json-file")
want["log-opts"] = {"max-size": "10m", "max-file": "3", **have.get("log-opts", {})}
if want == have:
    print("same")
    sys.exit(0)
with open(p + ".new", "w", encoding="utf-8") as f:
    json.dump(want, f, indent=2)
    f.write("\n")
os.replace(p + ".new", p)
print("changed")
PY
)" in
	changed)
		log "Configured docker log rotation (10m x 3 per container); restarting docker..."
		systemctl restart docker
		;;
	same) log "Docker's logs are capped already." ;;
	driver) warn "$DAEMON_JSON names another log driver; leaving it. Ensure it caps container logs." ;;
	*) die "could not read $DAEMON_JSON" ;;
esac

# ---------------------------------------------------------------------
# 7. Firewall — SSH + 80 + 443 only
# ---------------------------------------------------------------------
log "Configuring UFW (allow SSH, 80, 443; deny the rest)..."
ufw allow OpenSSH       >/dev/null 2>&1 || ufw allow 22/tcp
ufw allow 80/tcp        >/dev/null
ufw allow 443/tcp       >/dev/null
ufw allow 443/udp       >/dev/null   # HTTP/3 (QUIC)
ufw default deny incoming  >/dev/null
ufw default allow outgoing >/dev/null
ufw --force enable

# ---------------------------------------------------------------------
# 8. The nightly backup (docs/cloud-single-node.md, "Backups")
# ---------------------------------------------------------------------
log "Installing the nightly backup (maktaba-cloud-backup.timer)..."
mkdir -p /var/backups/maktaba-cloud
chmod 0700 /var/backups/maktaba-cloud
cat > /etc/systemd/system/maktaba-cloud-backup.service <<UNITEOF
[Unit]
Description=Maktaba Cloud: dump the database and keep the node's keys
Requires=docker.service
After=docker.service maktaba-relay.service

[Service]
Type=oneshot
Environment=DEPLOY_DIR=${DEPLOY_DIR}
ExecStart=${DEPLOY_DIR}/ops/backup.sh
UNITEOF
cat > /etc/systemd/system/maktaba-cloud-backup.timer <<'UNITEOF'
[Unit]
Description=Maktaba Cloud: the nightly backup

[Timer]
OnCalendar=*-*-* 03:17:00
RandomizedDelaySec=10m
Persistent=true

[Install]
WantedBy=timers.target
UNITEOF

# ---------------------------------------------------------------------
# 9. systemd unit
# ---------------------------------------------------------------------
UNIT=/etc/systemd/system/maktaba-relay.service
log "Installing systemd unit $UNIT ..."
cat > "$UNIT" <<UNITEOF
[Unit]
Description=Maktaba Cloud Relay (docker compose stack)
Requires=docker.service
After=docker.service network-online.target
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=${DEPLOY_DIR}
# a node that rebooted during a deploy goes back first; a held one does not start (ops/rollout.sh)
ExecCondition=/usr/local/sbin/maktaba-rollout boot
# --wait blocks until every service is healthy (or fails the start).
ExecStart=/usr/bin/docker compose --env-file .env up -d --wait
ExecStop=/usr/bin/docker compose --env-file .env down
ExecReload=/usr/bin/docker compose --env-file .env up -d --wait
TimeoutStartSec=900
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
UNITEOF
systemctl daemon-reload
systemctl enable --now maktaba-cloud-backup.timer >/dev/null

# a new signing key reaches a running api only by a restart (licences and home assertions)
if [ "$KEY_CHANGED" = yes ] && (cd "$DEPLOY_DIR" && docker compose --env-file .env ps --status running --services 2>/dev/null | grep -x api >/dev/null); then
	log "Restarting the api for the new signing key..."
	(cd "$DEPLOY_DIR" && docker compose --env-file .env restart api >/dev/null)
fi

# ---------------------------------------------------------------------
# 10. Pull + start (guard against an unedited template)
# ---------------------------------------------------------------------
domain="$(get_env RELAY_DOMAIN)"
acme="$(get_env ACME_EMAIL)"
tls="$(get_env RELAY_TLS)"; tls="${tls:-dns}"
case "$tls" in dns|http|internal) ;; *) die "RELAY_TLS is dns, http or internal" ;; esac
# The Cloudflare DNS-edit token for the wildcard certificate (Epic 50 story
# 50.02) is never generated: only the owner can make it. Caddy is not started
# on the template value, which would fail every DNS-01 challenge.
cf="$(get_env CF_API_TOKEN)"
if [ "$domain" = "relay.example.com" ] || is_placeholder "$domain" \
   || is_placeholder "$(get_env API_DOMAIN)" || is_placeholder "$(get_env APP_DOMAIN)" \
   || [ "$acme" = "ops@example.com" ] || is_placeholder "$acme" \
   || { [ "$tls" = dns ] && is_placeholder "$cf"; }; then
	warn "RELAY_DOMAIN / API_DOMAIN / APP_DOMAIN / ACME_EMAIL / CF_API_TOKEN still hold template values."
	warn "CF_API_TOKEN (RELAY_TLS=dns): a Cloudflare API token with Zone -> DNS -> Edit on the relay domain's zone only."
	warn "Edit $ENV_FILE, then: systemctl enable --now maktaba-relay"
	log  "Setup complete (stack NOT started — finish .env first)."
	exit 0
fi
if [ "${SETUP_START:-yes}" = no ]; then
	systemctl enable maktaba-relay >/dev/null
	log "Setup complete (enabled at boot; the deploy starts the stack)."
	exit 0
fi

log "Pulling images..."
( cd "$DEPLOY_DIR" && docker compose --env-file .env pull )
log "Enabling + starting maktaba-relay..."
systemctl enable --now maktaba-relay

log "Done. Check status with: systemctl status maktaba-relay"
log "Tail logs with:        cd $DEPLOY_DIR && docker compose logs -f"

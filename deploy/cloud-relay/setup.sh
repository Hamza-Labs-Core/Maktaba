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
#   8. pulls images and brings the stack up
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
	# Official convenience script — pulls Engine, CLI, and the compose
	# plugin from Docker's apt repo. Pinned behaviour for 22.04/24.04.
	curl -fsSL https://get.docker.com -o /tmp/get-docker.sh
	sh /tmp/get-docker.sh
	rm -f /tmp/get-docker.sh
	systemctl enable --now docker
fi
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
install -m 0644 "$SRC_DIR/docker-compose.yml" "$DEPLOY_DIR/docker-compose.yml"
install -m 0644 "$SRC_DIR/Caddyfile"          "$DEPLOY_DIR/Caddyfile"

# .env: create from template on first run; never clobber an existing one.
if [ ! -f "$DEPLOY_DIR/.env" ]; then
	install -m 0600 "$SRC_DIR/.env.example" "$DEPLOY_DIR/.env"
	log "Created $DEPLOY_DIR/.env from template — EDIT RELAY_DOMAIN + ACME_EMAIL."
fi

# cloud.toml: the compose mounts it read-only. Create an empty file so
# the bind mount doesn't materialise as a directory. Operators running
# --role api replace it with a real config.
[ -e "$DEPLOY_DIR/cloud.toml" ] || : > "$DEPLOY_DIR/cloud.toml"

# ---------------------------------------------------------------------
# 4. Generate secrets (only if unset / placeholder)
# ---------------------------------------------------------------------
ENV_FILE="$DEPLOY_DIR/.env"

# get_env KEY -> current value (empty if unset)
get_env() { grep -E "^$1=" "$ENV_FILE" | head -n1 | cut -d= -f2- || true; }

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

# Token secret (>= 32 bytes). openssl base64 of 48 random bytes => 64
# chars, all ASCII.
if is_placeholder "$(get_env MAKTABA_CLOUD_TOKEN_SECRET)"; then
	log "Generating MAKTABA_CLOUD_TOKEN_SECRET..."
	set_env MAKTABA_CLOUD_TOKEN_SECRET "$(openssl rand -base64 48 | tr -d '\n')"
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
if [ ! -s "$ENTITLEMENT_KEY" ]; then
	log "Generating Ed25519 entitlement key -> secrets/entitlement.key..."
	openssl rand 32 | base64 | tr -d '\n' > "$ENTITLEMENT_KEY"
fi

# ---------------------------------------------------------------------
# 5. Ownership + permissions (secrets are sensitive)
# ---------------------------------------------------------------------
chown -R "$SERVICE_USER:$SERVICE_USER" "$DEPLOY_DIR"
chmod 0700 "$DEPLOY_DIR/secrets"
chmod 0600 "$ENV_FILE" "$ENTITLEMENT_KEY"

# ---------------------------------------------------------------------
# 6. Container log rotation (docker json-file driver caps)
# ---------------------------------------------------------------------
DAEMON_JSON=/etc/docker/daemon.json
if [ ! -f "$DAEMON_JSON" ]; then
	log "Configuring docker log rotation (10m x 3 per container)..."
	mkdir -p /etc/docker
	cat > "$DAEMON_JSON" <<'JSON'
{
  "log-driver": "json-file",
  "log-opts": { "max-size": "10m", "max-file": "3" }
}
JSON
	systemctl restart docker
else
	warn "$DAEMON_JSON exists; leaving it untouched. Ensure log-opts cap container logs."
fi

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
# 8. systemd unit
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
# --wait blocks until every service is healthy (or fails the start).
ExecStart=/usr/bin/docker compose --env-file .env up -d --wait
ExecStop=/usr/bin/docker compose --env-file .env down
ExecReload=/usr/bin/docker compose --env-file .env up -d --wait
TimeoutStartSec=300
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
UNITEOF
systemctl daemon-reload

# ---------------------------------------------------------------------
# 9. Pull + start (guard against an unedited template)
# ---------------------------------------------------------------------
domain="$(get_env RELAY_DOMAIN)"
acme="$(get_env ACME_EMAIL)"
if [ "$domain" = "relay.example.com" ] || is_placeholder "$domain" \
   || [ "$acme" = "ops@example.com" ] || is_placeholder "$acme"; then
	warn "RELAY_DOMAIN / ACME_EMAIL still hold template values."
	warn "Edit $ENV_FILE, then: systemctl enable --now maktaba-relay"
	log  "Setup complete (stack NOT started — finish .env first)."
	exit 0
fi

log "Pulling images..."
( cd "$DEPLOY_DIR" && docker compose --env-file .env pull )
log "Enabling + starting maktaba-relay..."
systemctl enable --now maktaba-relay

log "Done. Check status with: systemctl status maktaba-relay"
log "Tail logs with:        cd $DEPLOY_DIR && docker compose logs -f"

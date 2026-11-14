#!/usr/bin/env bash
# The review home's provisioning (Epic 58 story 58.04), on an Ubuntu 22.04/24.04 machine, from a
# checkout of this repository. Idempotent: run it again at any time.
#
#   sudo deploy/review-home/setup.sh
#
# It installs Docker, ffmpeg and the firewall (SSH, 80, 443 only), stages the stack in
# /opt/maktaba-review, fetches and checks the content set (tools/review_home/content.py; each
# source against its published SHA-1) into REVIEW_MEDIA, and starts the home and Caddy. It
# stops before starting anything while .env still holds a template value. It never asks for or
# writes a password: the home's own first-run setup and seed.sh come after it (README.md).
set -euo pipefail

DEPLOY_DIR=${DEPLOY_DIR:-/opt/maktaba-review}
SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "${SRC_DIR}/../.." && pwd)"

log() { printf '==> %s\n' "$*"; }
die() { printf 'xx %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "run as root (sudo)"

export DEBIAN_FRONTEND=noninteractive
if ! { command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; }; then
	log "installing Docker Engine and the compose plugin"
	apt-get update -y
	apt-get install -y ca-certificates curl
	curl -fsSL https://get.docker.com -o /tmp/get-docker.sh
	sh /tmp/get-docker.sh
	rm -f /tmp/get-docker.sh
	systemctl enable --now docker
fi
for tool in ffmpeg python3 ufw; do
	command -v "${tool}" >/dev/null 2>&1 || { apt-get update -y && apt-get install -y "${tool}"; }
done

log "staging the stack in ${DEPLOY_DIR}"
mkdir -p "${DEPLOY_DIR}"
install -m 0644 "${SRC_DIR}/docker-compose.yml" "${DEPLOY_DIR}/docker-compose.yml"
install -m 0644 "${SRC_DIR}/Caddyfile" "${DEPLOY_DIR}/Caddyfile"
if [ ! -f "${DEPLOY_DIR}/.env" ]; then
	install -m 0600 "${SRC_DIR}/review-home.env.example" "${DEPLOY_DIR}/.env"
	log "made ${DEPLOY_DIR}/.env from the template: set ACME_EMAIL"
fi
env_get() { grep -E "^$1=" "${DEPLOY_DIR}/.env" | sed -n 1p | cut -d= -f2- || true; }
email="$(env_get ACME_EMAIL)"
domain="$(env_get REVIEW_DOMAIN)"
media="$(env_get REVIEW_MEDIA)"
media="${media:-/srv/review-media}"

log "the firewall: SSH, 80 and 443 only"
ufw default deny incoming >/dev/null
ufw default allow outgoing >/dev/null
ufw allow OpenSSH >/dev/null
ufw allow 80/tcp >/dev/null
ufw allow 443/tcp >/dev/null
ufw allow 443/udp >/dev/null
ufw --force enable >/dev/null

log "the content set in ${media}"
python3 -I "${REPO}/tools/review_home/content.py" validate
mkdir -p "${media}"
python3 -I "${REPO}/tools/review_home/content.py" fetch --dest "${media}"
# the home's user (1000) reads it; nothing writes it but this script
chown -R root:root "${media}"
chmod -R a+rX,go-w "${media}"

case "${email}" in "" | CHANGEME) die "set ACME_EMAIL in ${DEPLOY_DIR}/.env, then run this again" ;; esac
[ -n "${domain}" ] || die "set REVIEW_DOMAIN in ${DEPLOY_DIR}/.env"

log "starting the home and Caddy"
cd "${DEPLOY_DIR}"
docker compose --env-file .env pull
docker compose --env-file .env up -d
log "done. Next (README.md): the home's first-run setup, remote access, then seed.sh"

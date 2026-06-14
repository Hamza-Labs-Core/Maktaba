#!/usr/bin/env bash
# Maktaba Cloud Relay — remote deploy / rollout script.
#
# Rolls a new `maktaba-cloud` image onto the production VPS over SSH:
# pulls the image, runs pending migrations in a throwaway container,
# swaps the serving stack, verifies health, and rolls back to the
# previous tag if the health check fails.
#
# It is the SINGLE SOURCE OF TRUTH for a rollout: `make relay-deploy`
# and the `deploy` job in .github/workflows/relay.yml both invoke this
# script, so a maintainer can reproduce a CI deploy from a laptop.
#
# Usage:
#   RELAY_SSH_HOST=relay.example.com \
#   RELAY_SSH_USER=maktaba \
#   MAKTABA_CLOUD_TAG=v1.2.3 \
#   ./deploy.sh
#
# Required env:
#   RELAY_SSH_HOST   VPS hostname or IP.
#   RELAY_SSH_USER   SSH login user (must be in the `docker` group).
#
# Optional env:
#   MAKTABA_CLOUD_TAG  image tag to deploy (default: latest).
#   RELAY_SSH_KEY      private key *contents* (PEM). When set, written to
#                      a 0600 temp file and used as the SSH identity —
#                      this is how CI passes secrets.RELAY_SSH_KEY. When
#                      unset, the caller's ssh-agent / default keys apply.
#   RELAY_SSH_PORT     SSH port (default: 22).
#   DEPLOY_DIR         stack dir on the VPS (default: /opt/maktaba-relay,
#                      matching setup.sh).
#   HEALTH_URL         extra public health URL to curl from the VPS after
#                      rollout (e.g. https://relay.example.com/readyz).
#                      Optional; the in-container probe always runs.
set -euo pipefail

# --- config ---------------------------------------------------------------
: "${RELAY_SSH_HOST:?RELAY_SSH_HOST is required}"
: "${RELAY_SSH_USER:?RELAY_SSH_USER is required}"
NEW_TAG="${MAKTABA_CLOUD_TAG:-latest}"
DEPLOY_DIR="${DEPLOY_DIR:-/opt/maktaba-relay}"
RELAY_SSH_PORT="${RELAY_SSH_PORT:-22}"
HEALTH_URL="${HEALTH_URL:-}"

log() { printf '\033[1;34m[deploy]\033[0m %s\n' "$*"; }
err() { printf '\033[1;31m[deploy:error]\033[0m %s\n' "$*" >&2; }

# --- ssh identity ---------------------------------------------------------
# CI hands the key in via the RELAY_SSH_KEY secret (key *contents*). Stage
# it as a locked-down temp file and clean it up on exit. Without it, fall
# back to the caller's ambient SSH config (agent / ~/.ssh/id_*).
SSH_OPTS=(-o StrictHostKeyChecking=accept-new -o ConnectTimeout=15 -p "$RELAY_SSH_PORT")
KEY_FILE=""
cleanup() { [ -n "$KEY_FILE" ] && rm -f "$KEY_FILE"; }
trap cleanup EXIT

if [ -n "${RELAY_SSH_KEY:-}" ]; then
  KEY_FILE="$(mktemp)"
  chmod 600 "$KEY_FILE"
  printf '%s\n' "$RELAY_SSH_KEY" > "$KEY_FILE"
  SSH_OPTS+=(-i "$KEY_FILE" -o IdentitiesOnly=yes)
fi

SSH_TARGET="${RELAY_SSH_USER}@${RELAY_SSH_HOST}"

log "Deploying tag '${NEW_TAG}' to ${SSH_TARGET}:${DEPLOY_DIR}"

# --- remote payload -------------------------------------------------------
# Everything below runs ON THE VPS. We stream it over `bash -s` and pass
# the deploy dir, target tag, and optional health URL as positional args
# (never interpolated into the remote shell, so quoting is safe).
#
# The compose file reads ${MAKTABA_CLOUD_TAG} from .env, so the rollout is
# "edit .env -> pull -> migrate -> up --wait -> verify", with a revert of
# .env + `up --wait` on health failure.
ssh "${SSH_OPTS[@]}" "$SSH_TARGET" 'bash -s' -- "$DEPLOY_DIR" "$NEW_TAG" "$HEALTH_URL" <<'REMOTE'
set -euo pipefail
DEPLOY_DIR="$1"
NEW_TAG="$2"
HEALTH_URL="$3"

rlog() { printf '\033[1;36m[vps]\033[0m %s\n' "$*"; }
rerr() { printf '\033[1;31m[vps:error]\033[0m %s\n' "$*" >&2; }

cd "$DEPLOY_DIR" || { rerr "deploy dir $DEPLOY_DIR not found"; exit 1; }
[ -f docker-compose.yml ] || { rerr "no docker-compose.yml in $DEPLOY_DIR"; exit 1; }
[ -f .env ] || { rerr "no .env in $DEPLOY_DIR (run setup.sh first)"; exit 1; }

COMPOSE="docker compose --env-file .env"

# Set (or append) a KEY=VALUE in .env without disturbing other lines.
set_env() {
  local key="$1" val="$2"
  if grep -q "^${key}=" .env; then
    # Use a non-/ delimiter so tag values with slashes don't break sed.
    sed -i "s|^${key}=.*|${key}=${val}|" .env
  else
    printf '%s=%s\n' "$key" "$val" >> .env
  fi
}

# Capture the currently-deployed tag so we can roll back. Default to
# `latest` if the key was never pinned.
PREV_TAG="$(grep '^MAKTABA_CLOUD_TAG=' .env | head -1 | cut -d= -f2-)"
PREV_TAG="${PREV_TAG:-latest}"
rlog "current tag: ${PREV_TAG}  ->  new tag: ${NEW_TAG}"

set_env MAKTABA_CLOUD_TAG "$NEW_TAG"

rlog "pulling images..."
$COMPOSE pull

# Bring the datastores up first so migrations have a DB to talk to. The
# relay would migrate on serve anyway, but running it as a one-shot in a
# throwaway container surfaces a bad migration BEFORE we swap the live
# relay (avoids a crash-loop on the serving container).
rlog "ensuring postgres + redis are up..."
$COMPOSE up -d --wait postgres redis

rlog "running database migrations..."
$COMPOSE run --rm maktaba-cloud migrate up

# Swap the serving stack. --wait blocks until every service reports its
# Docker healthcheck healthy (or times out non-zero).
rlog "rolling out the stack..."
if ! $COMPOSE up -d --wait; then
  rerr "compose up did not reach healthy — rolling back to ${PREV_TAG}"
  set_env MAKTABA_CLOUD_TAG "$PREV_TAG"
  $COMPOSE up -d --wait || rerr "ROLLBACK to ${PREV_TAG} also failed — manual intervention required"
  exit 1
fi

# App-level health probe. The image is distroless (no shell/curl), so we
# run the bundled Go healthcheck binary inside the container; it hits the
# relay's own /healthz on the loopback listener.
rlog "verifying relay health..."
health_ok=1
if ! $COMPOSE exec -T maktaba-cloud /usr/local/bin/healthcheck; then
  health_ok=0
fi

# Optional public-endpoint check (readyz reports migrations_behind etc.).
if [ "$health_ok" = "1" ] && [ -n "$HEALTH_URL" ]; then
  rlog "checking public endpoint ${HEALTH_URL}..."
  if ! curl -fsS --max-time 10 "$HEALTH_URL" >/dev/null; then
    health_ok=0
  fi
fi

if [ "$health_ok" = "0" ]; then
  rerr "health check failed — rolling back to ${PREV_TAG}"
  set_env MAKTABA_CLOUD_TAG "$PREV_TAG"
  if $COMPOSE up -d --wait; then
    rerr "rolled back to ${PREV_TAG}"
  else
    rerr "ROLLBACK to ${PREV_TAG} also failed — manual intervention required"
  fi
  exit 1
fi

rlog "deploy of ${NEW_TAG} succeeded and is healthy."
REMOTE

log "Deploy finished successfully."

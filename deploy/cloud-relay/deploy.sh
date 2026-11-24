#!/usr/bin/env bash
# Maktaba Cloud, one node: the rollout step of a CI deploy (.github/workflows/cloud-deploy.yml).
#
# Runs after `ops/rollout.sh begin` (the node's present set saved, the images pulled) and setup.sh
# (the new set written): hands the node the symbol uploaders' token, then runs `maktaba-rollout
# apply` there, which rolls the release out and restores the saved set on any failure. The
# workflow then tests the node and commits or rolls back.
#
# Required env:
#   RELAY_SSH_HOST   the node's hostname or IP.
#   RELAY_SSH_USER   SSH login user (root).
#
# Optional env:
#   RELAY_SSH_KEY      private key *contents* (PEM). When set, written to
#                      a 0600 temp file and used as the SSH identity. When
#                      unset, the caller's ssh-agent / default keys apply.
#   RELAY_SSH_PORT     SSH port (default: 22).
#   RELAY_SSH_KNOWN_HOSTS  the host's known_hosts line(s). When set (CI always sets it, from
#                      the QA_SSH_KNOWN_HOSTS / RELAY_SSH_KNOWN_HOSTS variables), the host
#                      key must match it (StrictHostKeyChecking=yes); unset, a laptop's own
#                      known_hosts applies and a new host is accepted on first use.
#   DEPLOY_DIR         stack dir on the node (default: /opt/maktaba-relay,
#                      matching setup.sh).
#   MAKTABA_SYMBOLS_TOKEN  the token release symbol tables are uploaded with
#                      (story 53.16; the repository secret the release
#                      workflows upload with). Written to secrets/symbols.token
#                      (0600, owned like secrets/log.key) and named in .env as
#                      MAKTABA_CLOUD_SYMBOLS_TOKEN_FILE; never echoed. Empty: no
#                      file, so uploads stay off (503 symbols_off).
set -euo pipefail

# --- config ---------------------------------------------------------------
: "${RELAY_SSH_HOST:?RELAY_SSH_HOST is required}"
: "${RELAY_SSH_USER:?RELAY_SSH_USER is required}"
DEPLOY_DIR="${DEPLOY_DIR:-/opt/maktaba-relay}"
RELAY_SSH_PORT="${RELAY_SSH_PORT:-22}"

log() { printf '\033[1;34m[deploy]\033[0m %s\n' "$*"; }
err() { printf '\033[1;31m[deploy:error]\033[0m %s\n' "$*" >&2; }

# --- ssh identity ---------------------------------------------------------
# CI hands the key in via the RELAY_SSH_KEY secret (key *contents*). Stage
# it as a locked-down temp file and clean it up on exit. Without it, fall
# back to the caller's ambient SSH config (agent / ~/.ssh/id_*).
SSH_OPTS=(-o ConnectTimeout=15 -o BatchMode=yes -p "$RELAY_SSH_PORT")
KEY_FILE=""
KH_FILE=""
# (an if, not `[ ] && rm`: an EXIT trap's last status is the script's, so a run with no
# key file would end 1 after a good deploy)
cleanup() {
  if [ -n "$KEY_FILE" ]; then rm -f "$KEY_FILE"; fi
  if [ -n "$KH_FILE" ]; then rm -f "$KH_FILE"; fi
}
trap cleanup EXIT

if [ -n "${RELAY_SSH_KNOWN_HOSTS:-}" ]; then
  KH_FILE="$(mktemp)"
  printf '%s\n' "$RELAY_SSH_KNOWN_HOSTS" > "$KH_FILE"
  SSH_OPTS+=(-o StrictHostKeyChecking=yes -o UserKnownHostsFile="$KH_FILE")
else
  SSH_OPTS+=(-o StrictHostKeyChecking=accept-new)
fi

if [ -n "${RELAY_SSH_KEY:-}" ]; then
  KEY_FILE="$(mktemp)"
  chmod 600 "$KEY_FILE"
  printf '%s\n' "$RELAY_SSH_KEY" > "$KEY_FILE"
  SSH_OPTS+=(-i "$KEY_FILE" -o IdentitiesOnly=yes)
fi

SSH_TARGET="${RELAY_SSH_USER}@${RELAY_SSH_HOST}"

log "Deploying to ${SSH_TARGET}:${DEPLOY_DIR}"

# --- the symbol uploaders' token ------------------------------------------
# As deploy/cloud-ha/deploy.sh does on the pair: over SSH's stdin (never an
# argument, never echoed), into the read-only secrets mount, readable by its
# owner only and owned like the log key; replaced only when it changed. The
# api reads MAKTABA_CLOUD_SYMBOLS_TOKEN_FILE when the rollout below restarts it.
if [ -n "${MAKTABA_SYMBOLS_TOKEN:-}" ]; then
  # the remote script is single-quoted on purpose: it expands on the VPS, with the
  # deploy dir as its one (quoted) argument
  # shellcheck disable=SC2016,SC2029
  printf '%s' "$MAKTABA_SYMBOLS_TOKEN" | ssh "${SSH_OPTS[@]}" "$SSH_TARGET" \
    "bash -c $(printf '%q' '
set -euo pipefail
cd "$1"
[ -d secrets ] || { echo "no secrets/ in $1 (run setup.sh first)" >&2; exit 1; }
t=secrets/symbols.token
n=$(umask 077; mktemp secrets/.symbols.token.XXXXXX)
trap "rm -f \"$n\"" EXIT
cat >"$n"
chmod 0600 "$n"
if [ -f secrets/log.key ]; then
  own=$(ls -n secrets/log.key | awk "{print \$3\":\"\$4}")
  [ "$(id -u):$(id -g)" = "$own" ] || chown "$own" "$n"
fi
if [ -f "$t" ] && cmp -s "$n" "$t"; then rm -f "$n"; else mv "$n" "$t"; fi
f=/etc/maktaba/cloud/secrets/symbols.token
l="MAKTABA_CLOUD_SYMBOLS_TOKEN_FILE=$f"
if ! grep -qx "$l" .env; then
  # keep .env itself (its owner and mode): the other lines, then this one, written back
  e=$(grep -v "^MAKTABA_CLOUD_SYMBOLS_TOKEN_FILE=" .env || true)
  { [ -z "$e" ] || printf "%s\n" "$e"; printf "%s\n" "$l"; } >.env
fi
') _ $(printf '%q' "$DEPLOY_DIR")"
  log "the symbols token is in ${DEPLOY_DIR}/secrets"
fi

# --- the rollout ------------------------------------------------------------
# On the node: ops/rollout.sh apply (installed by `begin` as maktaba-rollout). It migrates, brings
# the stack up, watches its health, switches the web app, and restores what was before on any
# failure. Its last line is a marker: a remote command cut short can end at 0 too, so only the
# marker says it ran to its end.
REMOTE_LOG="$(mktemp)"
# shellcheck disable=SC2029  # expands here, on purpose: the quoted argument
ssh "${SSH_OPTS[@]}" "$SSH_TARGET" "DEPLOY_DIR=$(printf '%q' "$DEPLOY_DIR") /usr/local/sbin/maktaba-rollout apply" </dev/null | tee "$REMOTE_LOG"

if ! grep -qx '__maktaba_deploy_complete__' "$REMOTE_LOG"; then
  rm -f "$REMOTE_LOG"
  err "the remote script stopped before its end: nothing is known to be rolled out"
  exit 1
fi
rm -f "$REMOTE_LOG"
log "Applied. It is the node's once its tests pass (maktaba-rollout commit)."

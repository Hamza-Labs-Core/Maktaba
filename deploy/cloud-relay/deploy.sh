#!/usr/bin/env bash
# Maktaba Cloud, one node: a CI deploy's rollout, commit and rollback on the node
# (.github/workflows/cloud-deploy.yml), each as a unit of the node's own.
#
#   deploy.sh apply      after `ops/rollout.sh begin` (the node's present set saved, the images
#                        pulled) and setup.sh (the new set written): hands the node the symbol
#                        uploaders' token, then rolls the release out there; any failure restores
#                        the saved set. The workflow then tests the node and
#   deploy.sh commit     keeps it, or
#   deploy.sh rollback   goes back.
#
# Each is started once on the node and followed from here; a cut session is picked up again, and
# asking again after a lost answer gives the same one. Nothing on the node stops when this does.
#
# Required env:
#   DEPLOY_ID        the deploy's name, the one `begin` was given.
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
#   FOLLOW_SECONDS     how long to go on trying to reach the node (default: 2700)
#   RETRY_SECONDS      between two tries (default: 5)
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
: "${DEPLOY_ID:?DEPLOY_ID is required}"
ACTION="${1:-apply}"
case "$ACTION" in apply | commit | rollback) ;; *) echo "usage: deploy.sh apply | commit | rollback" >&2; exit 2 ;; esac
FOLLOW_SECONDS="${FOLLOW_SECONDS:-2700}"
RETRY_SECONDS="${RETRY_SECONDS:-5}"
DEPLOY_DIR="${DEPLOY_DIR:-/opt/maktaba-relay}"
RELAY_SSH_PORT="${RELAY_SSH_PORT:-22}"

log() { printf '\033[1;34m[deploy]\033[0m %s\n' "$*"; }
err() { printf '\033[1;31m[deploy:error]\033[0m %s\n' "$*" >&2; }

# --- ssh identity ---------------------------------------------------------
# CI hands the key in via the RELAY_SSH_KEY secret (key *contents*). Stage
# it as a locked-down temp file and clean it up on exit. Without it, fall
# back to the caller's ambient SSH config (agent / ~/.ssh/id_*).
SSH_OPTS=(-o ConnectTimeout=15 -o ServerAliveInterval=15 -o ServerAliveCountMax=3 -o BatchMode=yes -p "$RELAY_SSH_PORT")
KEY_FILE=""
KH_FILE=""
OUT=""
ROUND=""
# (an if, not `[ ] && rm`: an EXIT trap's last status is the script's, so a run with no
# key file would end 1 after a good deploy)
cleanup() {
  if [ -n "$KEY_FILE" ]; then rm -f "$KEY_FILE"; fi
  if [ -n "$KH_FILE" ]; then rm -f "$KH_FILE"; fi
  if [ -n "$OUT" ]; then rm -f "$OUT" "$ROUND"; fi
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

log "${ACTION} on ${SSH_TARGET}:${DEPLOY_DIR} (deploy ${DEPLOY_ID})"

# --- the symbol uploaders' token ------------------------------------------
# As deploy/cloud-ha/deploy.sh does on the pair: over SSH's stdin (never an
# argument, never echoed), into the read-only secrets mount, readable by its
# owner only and owned like the log key; replaced only when it changed. The
# api reads MAKTABA_CLOUD_SYMBOLS_TOKEN_FILE when the rollout below restarts it.
if [ "$ACTION" = apply ] && [ -n "${MAKTABA_SYMBOLS_TOKEN:-}" ]; then
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
# On the node, as a unit of its own (ops/rollout.sh job, installed by `begin` as maktaba-rollout): it
# outlives this session, so a cut one is picked up again from the line it ended at. apply migrates,
# brings the stack up, watches its health, switches the web app, and restores what was before on
# any failure. Its last line is a marker: only it says apply ran to its end.
OUT="$(mktemp)"
ROUND="$(mktemp)"
from=0
end=$((SECONDS + FOLLOW_SECONDS))
while :; do
  rc=0
  # shellcheck disable=SC2029  # expands here, on purpose: the quoted arguments
  ssh "${SSH_OPTS[@]}" "$SSH_TARGET" "if [ ! -x /usr/local/sbin/maktaba-rollout ]; then echo '[rollout] no deploy has begun on this node'; exit $([ "$ACTION" = rollback ] && echo 0 || echo 1); fi; \
DEPLOY_DIR=$(printf '%q' "$DEPLOY_DIR") /usr/local/sbin/maktaba-rollout job $(printf '%q' "$DEPLOY_ID") $ACTION $from" </dev/null | tee "$ROUND" || rc="${PIPESTATUS[0]}"
  cat "$ROUND" >>"$OUT"
  from=$((from + $(wc -l <"$ROUND")))
  # 255 is ssh's own (the session was cut or not made), above 128 is an ssh that was killed: the
  # unit goes on. Its own codes are below (rollout.sh run-job)
  [ "$rc" -gt 128 ] || break
  if [ "$SECONDS" -ge "$end" ]; then
    err "the node could not be reached to its end: $ACTION goes on there, and the node comes to a known state by itself (ops/rollout.sh, \"A lost runner\")"
    break
  fi
  log "the session was cut: following $ACTION again from line $from"
  sleep "$RETRY_SECONDS"
done
if [ "$rc" = 0 ] && [ "$ACTION" = apply ] && ! grep -qx '__maktaba_deploy_complete__' "$OUT"; then
  err "the remote script stopped before its end: nothing is known to be rolled out"
  rc=1
fi
[ "$rc" = 0 ] || exit "$rc"
case "$ACTION" in
  apply) log "Applied. It is the node's once its tests pass (deploy.sh commit)." ;;
  *) log "$ACTION: done." ;;
esac

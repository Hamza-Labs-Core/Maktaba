#!/usr/bin/env bash
# End to end on a real systemd (Epic 39 story 39.11): the Linux app's server, set up the way
# a machine without a desktop sets it up, runs every role as this user's systemd unit, comes
# back from a restart, a crash and a pause, and takes an update.
#
#   1. `maktaba-server setup --answers` from the payload, as `install.sh --app` runs it: check,
#      the runtime copy (owner headless), the admin, a library folder, the models (no second
#      listen: CI has no Hugging Face token), Plex skipped, the venv, the models' download,
#      finish (server.toml, the database created and migrated), then the user unit with
#      linger. A server.toml written first keeps every listener on 127.0.0.1.
#   2. every role running (the supervisor's /processes); the web app and the API answer
#   3. `systemctl --user restart`, a SIGKILL of the launcher (Restart=always), and
#      `service stop` (stays stopped) / `service start`
#   4. an update: once the payload ran healthy (state/payload.json), the same payload under
#      a newer version is installed and activated (`runtime install --activate`, what the
#      app runs after its updater). The next start dumps the database before the new
#      version's migrations (backups/pre-<old>-to-<new>.dump), runs them, and keeps the venv
#      (the constraints did not change)
#
# Hermetic: XDG_CONFIG_HOME, XDG_DATA_HOME, XDG_STATE_HOME and XDG_CACHE_HOME point into a
# scratch directory (the unit pins them), so this user's own Maktaba files are never read or
# written. On exit the unit is stopped, disabled and removed, linger is turned off again if
# this script turned it on, and the scratch directory is removed. It runs on CI (CI=true),
# elsewhere only with --force, and never as root or when this user already has a
# maktaba-server.service (it would replace it).
#
# Usage: e2e.sh [--payload DIR] [--force]
#   --payload  the payload (default: build/linux-app/server)
#   --force    run outside CI (a throwaway machine)
# Needs a systemd user manager for this user (linger: turned on with `sudo -n loginctl` when
# it is off), curl, and the network (the venv's wheels, the models).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
PAYLOAD="$REPO_ROOT/build/linux-app/server"
FORCE=0
while [ $# -gt 0 ]; do
    case "$1" in
        --payload) [ $# -ge 2 ] || { echo "--payload needs a value" >&2; exit 2; }; PAYLOAD="$2"; shift 2 ;;
        --force)   FORCE=1; shift ;;
        -h|--help) sed -n '2,/^set -e/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//'; exit 0 ;;
        *)         echo "unknown option '$1'" >&2; exit 2 ;;
    esac
done
if [ "${CI:-}" != true ] && [ "$FORCE" -ne 1 ]; then
    echo "e2e.sh installs and starts a systemd user service: it runs on CI, or with --force on a throwaway machine" >&2
    exit 2
fi
[ "$(id -u)" -ne 0 ] || { echo "e2e.sh runs as a user, not root (the server is a user service)" >&2; exit 2; }
PAYLOAD="$(cd "$PAYLOAD" && pwd)"
S="$PAYLOAD/bin/maktaba-server"
PY="$PAYLOAD/python/bin/python3"
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
SUPERVISOR=127.0.0.1:9103
# the update's version: newer than any the payload builds (a release or a development build)
NEXT_VERSION=v99.0.0

log() { printf '\033[1;36m[e2e]\033[0m %s\n' "$*"; }

# json_field KEY: KEY of the `data` of the --json event on stdin
json_field() {
    "$PY" -I -c 'import json, sys
d = json.load(sys.stdin).get("data") or {}
v = d.get(sys.argv[1], "")
print(str(v).lower() if isinstance(v, bool) else v)' "$1"
}

# --- the user manager, and no unit of this user's own ---------------------------------
LINGER_ON=0
if [ "$("$S" service status --json | json_field linger)" != true ]; then
    sudo -n loginctl enable-linger "$USER" ||
        { echo "linger is off and this user cannot turn it on (sudo -n loginctl enable-linger)" >&2; exit 1; }
    LINGER_ON=1
fi
for _ in $(seq 1 30); do
    [ -S "$XDG_RUNTIME_DIR/systemd/private" ] && break
    sleep 1
done
st="$("$S" service status --json)"
if [ "$(printf '%s' "$st" | json_field manager)" != ok ]; then
    echo "no systemd user manager: $(printf '%s' "$st" | json_field reason)" >&2
    [ "$LINGER_ON" -eq 0 ] || sudo -n loginctl disable-linger "$USER" || true
    exit 1
fi
for key in installed taken system_unit; do
    v="$(printf '%s' "$st" | json_field "$key")"
    if [ -n "$v" ] && [ "$v" != false ]; then
        echo "this user already has a maktaba-server.service ($key: $v): e2e.sh would replace it" >&2
        [ "$LINGER_ON" -eq 0 ] || sudo -n loginctl disable-linger "$USER" || true
        exit 1
    fi
done
log "systemd user manager: ok (linger turned on here: $LINGER_ON)"

# --- a scratch home for everything Maktaba writes ----------------------------------------
SCRATCH="$(mktemp -d "${TMPDIR:-/tmp}/linux-app-e2e.XXXXXX")"
export XDG_CONFIG_HOME="$SCRATCH/config" XDG_DATA_HOME="$SCRATCH/data" \
    XDG_STATE_HOME="$SCRATCH/state" XDG_CACHE_HOME="$SCRATCH/cache"
DATA="$XDG_DATA_HOME/maktaba"
CONFIG="$XDG_CONFIG_HOME/maktaba"
LOGS="$XDG_STATE_HOME/maktaba/logs"

cleanup() {
    set +e
    "$S" service uninstall >/dev/null 2>&1 # stop, disable and remove the unit (only the app's)
    [ "$LINGER_ON" -eq 0 ] || sudo -n loginctl disable-linger "$USER"
    rm -rf "$SCRATCH"
}
trap cleanup EXIT

diag() {
    echo "--- service status";  "$S" service status 2>&1 || true
    echo "--- runtime status";  "$S" runtime status 2>&1 || true
    echo "--- /processes";      curl -fsS "http://$SUPERVISOR/processes" 2>&1 || true; echo
    echo "--- setup";           tail -n 40 "$SCRATCH/setup.log" 2>/dev/null || true
    echo "--- server.log";      tail -n 80 "$LOGS/server.log" 2>/dev/null || true
    echo "--- journal";         journalctl --user -u maktaba-server --no-pager -n 40 2>/dev/null || true
}
die() { printf '\033[1;31m[e2e] FAIL\033[0m %s\n' "$*"; diag; exit 1; }

# --- 1. setup --answers ------------------------------------------------------------------
mkdir -p "$CONFIG" "$SCRATCH/media"
cat >"$CONFIG/server.toml" <<'EOF'
# e2e.sh: every listener on 127.0.0.1; no second listen (no Hugging Face token on CI);
# no language-model server (a 5 GB download the test does not need)
[server]
listen = "127.0.0.1:8080"
web_listen = "127.0.0.1:8088"
admin_addr = "127.0.0.1:9100"
stream_addr = "127.0.0.1:8081"
streaming_admin_addr = "127.0.0.1:9101"

[transcription]
refine = "off"

[llm]
enabled = "off"
EOF
password="$(head -c 24 /dev/urandom | base64 | tr -d '/+=')"
answers="$SCRATCH/answers.json"
(umask 077 && "$PY" -I -c 'import json, sys
json.dump({
    "admin": {"username": "e2e", "password": sys.argv[3]},
    "libraries": {"roots": [sys.argv[2]], "path_map": []},
    "models": {"source": "huggingface", "hf_token": ""},
    "plex": {"action": "skip"},
}, open(sys.argv[1], "w"))' "$answers" "$SCRATCH/media" "$password")

log "setup --answers (check, runtime, admin, libraries, models, plex, venv, download, finish, service)"
"$S" setup --answers "$answers" --lang en </dev/null >"$SCRATCH/setup.log" 2>&1 || die "setup --answers"
tail -n 6 "$SCRATCH/setup.log"
rs="$("$S" runtime status --json)"
[ "$(printf '%s' "$rs" | json_field owner)" = headless ] || die "the runtime's owner is not headless: $rs"
first="$(printf '%s' "$rs" | json_field current)"
[ -n "$first" ] || die "setup left no current runtime: $rs"
log "runtime: $first (owner headless)"

# --- 2. every role running ----------------------------------------------------------------
roles="postgres api streaming pipeline"

# wait_running SECONDS: every role in $roles is running
wait_running() {
    local deadline=$((SECONDS + $1)) snap=""
    while [ "$SECONDS" -lt "$deadline" ]; do
        snap="$(curl -fsS --max-time 5 "http://$SUPERVISOR/processes" 2>/dev/null || true)"
        if [ -n "$snap" ] && printf '%s' "$snap" | "$PY" -I -c '
import json, sys
want = set(sys.argv[1].split())
procs = json.load(sys.stdin).get("processes", [])
running = {p["role"] for p in procs if p.get("state") == "running"}
sys.exit(0 if want <= running else 1)' "$roles"; then
            return 0
        fi
        sleep 3
    done
    die "not every role ($roles) running after $1 s"
}

wait_running 600
log "running: $roles"
curl -fsS --max-time 10 http://127.0.0.1:8088/ | grep -qi '<html' || die "the web app does not answer on 127.0.0.1:8088"
curl -fsS --max-time 10 http://127.0.0.1:8080/healthz >/dev/null || die "the API does not answer on 127.0.0.1:8080/healthz"
log "the web app and the API answer"
st="$("$S" service status --json)"
for key in installed enabled linger; do
    [ "$(printf '%s' "$st" | json_field "$key")" = true ] || die "service status: $key is not true: $st"
done
[ "$(printf '%s' "$st" | json_field active)" = active ] || die "service status: not active: $st"

# --- 3. restart, crash, pause ---------------------------------------------------------------
log "systemctl --user restart"
systemctl --user restart maktaba-server.service
wait_running 300

log "the launcher killed (SIGKILL): Restart=always brings it back"
systemctl --user kill --kill-whom=main --signal=SIGKILL maktaba-server.service
sleep 8
wait_running 300

log "service stop: stays stopped"
"$S" service stop || die "service stop"
[ "$(systemctl --user is-active maktaba-server.service || true)" = inactive ] || die "the unit is not inactive after stop"
sleep 10
[ "$(systemctl --user is-active maktaba-server.service || true)" = inactive ] || die "a stopped unit started again"
if curl -fsS --max-time 3 "http://$SUPERVISOR/processes" >/dev/null 2>&1; then die "the supervisor still answers after stop"; fi

log "service start"
"$S" service start || die "service start"
wait_running 300

# --- 4. an update ---------------------------------------------------------------------------
log "waiting for the payload to be confirmed (every role healthy for 3 minutes)"
deadline=$((SECONDS + 600))
until [ -f "$DATA/state/payload.json" ]; do
    [ "$SECONDS" -lt "$deadline" ] || die "the payload was not confirmed within 600 s"
    sleep 10
done
venv_cfg="$DATA/venv/pyvenv.cfg"
[ -f "$venv_cfg" ] || die "no venv at $DATA/venv"
venv_before="$(stat -c %Y "$venv_cfg")"

log "the same payload as $NEXT_VERSION: runtime install --activate"
mkdir -p "$SCRATCH/next"
cp -a "$PAYLOAD" "$SCRATCH/next/server"
"$PY" -I -c 'import json, sys
p = sys.argv[1]
d = json.load(open(p))
d["version"] = sys.argv[2]
json.dump(d, open(p, "w"), indent=2)' "$SCRATCH/next/server/payload.json" "$NEXT_VERSION"
"$S" runtime install --from "$SCRATCH/next/server" --activate || die "runtime install --activate"
wait_running 900

dump="$(find "$DATA" -name "pre-*-to-$NEXT_VERSION.dump" -print -quit)"
[ -n "$dump" ] || die "no database dump was taken before the update"
log "the database was saved before the update: ${dump#"$DATA"/}"
[ "$(stat -c %Y "$venv_cfg")" = "$venv_before" ] || die "the venv was rebuilt although the constraints did not change"
log "the venv was kept"
rs="$("$S" runtime status --json)"
[ "$(printf '%s' "$rs" | json_field version)" = "$NEXT_VERSION" ] || die "current is not $NEXT_VERSION: $rs"
[ "$(printf '%s' "$rs" | json_field previous)" = "$first" ] || die "previous is not $first: $rs"
curl -fsS --max-time 10 http://127.0.0.1:8080/healthz >/dev/null || die "the API does not answer after the update"
log "updated to $NEXT_VERSION (previous: $first); migrations ran and the API answers"

log "all end-to-end steps passed"

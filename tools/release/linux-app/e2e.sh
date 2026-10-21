#!/usr/bin/env bash
# End to end on a real systemd (Epic 39 story 39.11): the Linux app's server, installed the
# way the app installs it, runs every role as this user's systemd unit and comes back from
# a restart, a crash and a pause.
#
#   1. a copy of the payload's maktaba-server (the app runs one when a package lost its mode)
#   2. `runtime install --activate --owner app`: the payload copied into
#      ~/.local/share/maktaba/runtime, every file checked
#   3. the pipeline's venv from the runtime's Python, wheel and constraints (venv_bootstrap)
#   4. server.toml with every listener on 127.0.0.1
#   5. `service install`: the user unit, started; then postgres, api, streaming and the
#      pipeline all running (the supervisor's /processes), the web app and the API answer
#   6. `systemctl --user restart`, a SIGKILL of the launcher (Restart=always), and
#      `service stop` (stays stopped) / `service start`: running again after each
#
# Usage: e2e.sh [--payload DIR] [--skip-venv]
#   --payload    the payload (default: build/linux-app/server)
#   --skip-venv  no pipeline venv (the pipeline role is then not waited for)
# Needs a systemd user manager for this user with linger on (CI: sudo loginctl
# enable-linger), and curl. It changes this user's ~/.local/share/maktaba, ~/.config/maktaba
# and user unit: run it on a CI runner or a throwaway machine.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
PAYLOAD="$REPO_ROOT/build/linux-app/server"
VENV=1
while [ $# -gt 0 ]; do
    case "$1" in
        --payload)   [ $# -ge 2 ] || { echo "--payload needs a value" >&2; exit 2; }; PAYLOAD="$2"; shift 2 ;;
        --skip-venv) VENV=0; shift ;;
        -h|--help)   sed -n '2,/^set -e/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//'; exit 0 ;;
        *)           echo "unknown option '$1'" >&2; exit 2 ;;
    esac
done
PAYLOAD="$(cd "$PAYLOAD" && pwd)"
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
DATA="${XDG_DATA_HOME:-$HOME/.local/share}/maktaba"
CONFIG="${XDG_CONFIG_HOME:-$HOME/.config}/maktaba"
LOGS="${XDG_STATE_HOME:-$HOME/.local/state}/maktaba/logs"
SUPERVISOR=127.0.0.1:9103

log()  { printf '\033[1;36m[e2e]\033[0m %s\n' "$*"; }
diag() {
    echo "--- service status";  "$S" service status 2>&1 || true
    echo "--- /processes";      curl -fsS "http://$SUPERVISOR/processes" 2>&1 || true; echo
    echo "--- server.log";      tail -n 80 "$LOGS/server.log" 2>/dev/null || true
    echo "--- journal";         journalctl --user -u maktaba-server --no-pager -n 40 2>/dev/null || true
}
die()  { printf '\033[1;31m[e2e] FAIL\033[0m %s\n' "$*"; diag; exit 1; }

SCRATCH="$(mktemp -d "${TMPDIR:-/tmp}/linux-app-e2e.XXXXXX")"
trap 'rm -rf "$SCRATCH"' EXIT
S="$SCRATCH/maktaba-server"
cp "$PAYLOAD/bin/maktaba-server" "$S"
chmod 0755 "$S"
PY="$PAYLOAD/python/bin/python3"

# the user manager: linger starts it; wait for its socket
for _ in $(seq 1 30); do
    [ -S "$XDG_RUNTIME_DIR/systemd/private" ] && break
    sleep 1
done
manager="$("$S" service status --json | "$PY" -I -c 'import json,sys; d=json.load(sys.stdin)["data"]; print(d["manager"], d.get("reason",""))')"
[ "${manager%% *}" = ok ] || die "no systemd user manager: $manager"
log "systemd user manager: ok"

log "runtime install from $PAYLOAD"
"$S" runtime install --from "$PAYLOAD" --activate --owner app || die "runtime install"
R="$DATA/runtime/current"
[ -x "$R/bin/maktaba-launcher" ] || die "no launcher in $R"
"$S" runtime status --from "$PAYLOAD"

if [ "$VENV" -eq 1 ]; then
    log "the pipeline venv (downloads the wheels the constraints pin)"
    whl=("$R"/pipeline/maktaba_pipeline-*-py3-none-any.whl)
    PIP_NO_CACHE_DIR=1 PYTHONPATH="${whl[0]}" "$R/python/bin/python3" -m maktaba_pipeline.venv_bootstrap \
        --payload "$R/pipeline" --venv "$DATA/venv" >"$SCRATCH/venv.log" 2>&1 ||
        { tail -n 20 "$SCRATCH/venv.log"; die "venv_bootstrap"; }
    tail -n 1 "$SCRATCH/venv.log"
fi

log "server.toml: every listener on 127.0.0.1"
mkdir -p "$CONFIG"
cat >"$CONFIG/server.toml" <<'EOF'
[server]
listen = "127.0.0.1:8080"
web_listen = "127.0.0.1:8088"
admin_addr = "127.0.0.1:9100"
stream_addr = "127.0.0.1:8081"
streaming_admin_addr = "127.0.0.1:9101"
EOF

roles="postgres api streaming"
[ "$VENV" -eq 1 ] && roles="$roles pipeline"

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

log "service install (enabled and started)"
"$S" service install || die "service install"
wait_running 600
log "running: $roles"
curl -fsS --max-time 10 http://127.0.0.1:8088/ | grep -qi '<html' || die "the web app does not answer on 127.0.0.1:8088"
curl -fsS --max-time 10 http://127.0.0.1:8080/healthz >/dev/null || die "the API does not answer on 127.0.0.1:8080/healthz"
log "the web app and the API answer"
st="$("$S" service status --json)"
for key in '"installed":true' '"enabled":true' '"active":"active"' '"linger":true'; do
    printf '%s' "$st" | grep -q "$key" || die "service status lacks $key: $st"
done

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

log "all end-to-end steps passed"

#!/usr/bin/env bash
# End to end on a real systemd (Epic 39 story 39.11): the Linux app's server, set up the way
# a machine without a desktop sets it up, runs every role as this user's systemd unit, comes
# back from a restart, a crash and a pause, and takes an update.
#
#   1. `maktaba-server setup --answers`, as `install.sh --app` runs it: check, the runtime
#      copy (owner headless), the admin, a library folder, the models (no second listen: CI
#      has no Hugging Face token), Plex skipped, the venv, the models' download, finish
#      (server.toml, the database created and migrated), then the user unit with linger. A
#      server.toml written first keeps every listener on 127.0.0.1. The setup runs from the
#      latest release's payload when that release has one for this machine (checked against
#      its checksums.txt), so step 4 is an update from a real previous release; else from
#      the payload under test.
#   2. every role running (the supervisor's /processes); the web app and the API answer
#   3. `systemctl --user restart`, a SIGKILL of the launcher (Restart=always), and
#      `service stop` (stays stopped) / `service start`
#   4. an update to the payload under test, as a version newer than any release (v99.0.0),
#      installed and activated (`runtime install --activate`, what the app runs after its
#      updater). The next start dumps the database before the new version's migrations
#      (backups/pre-<old>-to-<new>.dump), runs them and records it (state/database.json:
#      migrated, from the old version), and keeps the venv when the constraints are the same;
#      the updated server takes the distribution's ffmpeg (no ffmpeg in the payload): the
#      supervisor's check of it says ok, from the PATH
#
# Hermetic: XDG_CONFIG_HOME, XDG_DATA_HOME, XDG_STATE_HOME and XDG_CACHE_HOME point into a
# scratch directory (the unit pins them), so this user's own Maktaba files are never read or
# written. On exit the unit is stopped, disabled and removed, linger is turned off again if
# this script turned it on, the scratch directory is removed, and each of those is checked:
# a cleanup that failed fails the run. It runs on CI (CI=true), elsewhere only with --force,
# and never as root or when this user already has a maktaba-server.service.
#
# Usage: e2e.sh [--payload DIR] [--force]
#   --payload  the payload under test (default: build/linux-app/server)
#   --force    run outside CI (a throwaway machine)
# Needs a systemd user manager for this user (linger: turned on with `sudo -n loginctl` when
# it is off), curl, and the network (the venv's wheels, the models; GITHUB_TOKEN, when set,
# for the release lookup).
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
PY="$PAYLOAD/python/bin/python3"
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
SUPERVISOR=127.0.0.1:9103
REPO="${GITHUB_REPOSITORY:-Hamza-Labs-Core/Maktaba}"
# the update's version: newer than any release and any development build
NEXT_VERSION=v99.0.0

log() { printf '\033[1;36m[e2e]\033[0m %s\n' "$*"; }

# json_field KEY: KEY of the `data` of the --json event on stdin
json_field() {
    "$PY" -I -c 'import json, sys
d = json.load(sys.stdin).get("data") or {}
v = d.get(sys.argv[1], "")
print(str(v).lower() if isinstance(v, bool) else v)' "$1"
}

# --- a scratch home, and the cleanup, before anything is changed ------------------------------
SCRATCH="$(mktemp -d "${TMPDIR:-/tmp}/linux-app-e2e.XXXXXX")"
export XDG_CONFIG_HOME="$SCRATCH/config" XDG_DATA_HOME="$SCRATCH/data" \
    XDG_STATE_HOME="$SCRATCH/state" XDG_CACHE_HOME="$SCRATCH/cache"
DATA="$XDG_DATA_HOME/maktaba"
CONFIG="$XDG_CONFIG_HOME/maktaba"
LOGS="$XDG_STATE_HOME/maktaba/logs"
LINGER_ON=0  # this script turned linger on
INSTALLED=0  # this script installed the unit (setup ran)

# cli: the maktaba-server the app would run now: the runtime's, else the payload's
cli() {
    if [ -x "$DATA/runtime/current/bin/maktaba-server" ]; then
        "$DATA/runtime/current/bin/maktaba-server" "$@"
    else
        "$PAYLOAD/bin/maktaba-server" "$@"
    fi
}

cleanup() {
    local rc=$? problems=0 out
    set +e
    if [ "$INSTALLED" -eq 1 ]; then
        if out="$(cli service uninstall 2>&1)"; then
            log "cleanup: $out"
        else
            echo "[e2e] cleanup: service uninstall failed: $out" >&2
            problems=1
        fi
        if systemctl --user cat maktaba-server.service >/dev/null 2>&1; then
            echo "[e2e] cleanup: maktaba-server.service is still installed" >&2
            problems=1
        fi
    fi
    if [ "$LINGER_ON" -eq 1 ]; then
        if out="$(sudo -n loginctl disable-linger "$USER" 2>&1)"; then
            log "cleanup: linger turned off again"
        else
            echo "[e2e] cleanup: linger could not be turned off: $out" >&2
            problems=1
        fi
    fi
    rm -rf "$SCRATCH" || problems=1
    [ ! -e "$SCRATCH" ] || { echo "[e2e] cleanup: $SCRATCH is still there" >&2; problems=1; }
    if [ "$problems" -ne 0 ] && [ "$rc" -eq 0 ]; then
        rc=1
    fi
    exit "$rc"
}
trap cleanup EXIT

diag() {
    echo "--- service status";  cli service status 2>&1 || true
    echo "--- runtime status";  cli runtime status 2>&1 || true
    echo "--- /processes";      curl -fsS "http://$SUPERVISOR/processes" 2>&1 || true; echo
    echo "--- setup";           tail -n 40 "$SCRATCH/setup.log" 2>/dev/null || true
    echo "--- server.log";      tail -n 80 "$LOGS/server.log" 2>/dev/null || true
    echo "--- journal";         journalctl --user -u maktaba-server --no-pager -n 40 2>/dev/null || true
}
die() { printf '\033[1;31m[e2e] FAIL\033[0m %s\n' "$*"; diag; exit 1; }

# --- the user manager, and no unit of this user's own -----------------------------------------
if [ "$(cli service status --json | json_field linger)" != true ]; then
    sudo -n loginctl enable-linger "$USER" ||
        { echo "linger is off and this user cannot turn it on (sudo -n loginctl enable-linger)" >&2; exit 1; }
    LINGER_ON=1
fi
for _ in $(seq 1 30); do
    [ -S "$XDG_RUNTIME_DIR/systemd/private" ] && break
    sleep 1
done
st="$(cli service status --json)"
if [ "$(printf '%s' "$st" | json_field manager)" != ok ]; then
    echo "no systemd user manager: $(printf '%s' "$st" | json_field reason)" >&2
    exit 1
fi
for key in installed taken system_unit; do
    v="$(printf '%s' "$st" | json_field "$key")"
    if [ -n "$v" ] && [ "$v" != false ]; then
        echo "this user already has a maktaba-server.service ($key: $v): e2e.sh would replace it" >&2
        exit 1
    fi
done
log "systemd user manager: ok (linger turned on here: $LINGER_ON)"

# --- the first payload: the latest release's, when it has one for this machine ----------------
# previous_release DIR: the latest release's Linux payload unpacked in DIR (its server/
# directory printed), checked against the release's checksums.txt; nothing when the latest
# release has none for this machine. A payload it lists but cannot give is a failure (on
# stderr, status 1).
previous_release() {
    local dir="$1" arch tag asset base api auth=()
    arch="$(uname -m)"
    [ -n "${GITHUB_TOKEN:-}" ] && auth=(-H "Authorization: Bearer $GITHUB_TOKEN")
    api="$(curl -fsSL "${auth[@]}" "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null)" || return 0
    tag="$(printf '%s' "$api" | "$PY" -I -c 'import json, sys; print(json.load(sys.stdin).get("tag_name", ""))')"
    [ -n "$tag" ] || return 0
    asset="maktaba-linux-payload-${tag#v}-$arch.tar.gz"
    printf '%s' "$api" | "$PY" -I -c 'import json, sys
sys.exit(0 if any(a.get("name") == sys.argv[1] for a in json.load(sys.stdin).get("assets", [])) else 1)' "$asset" || return 0
    base="https://github.com/$REPO/releases/download/$tag"
    mkdir -p "$dir"
    if ! curl -fsSL -o "$dir/$asset" "$base/$asset" || ! curl -fsSL -o "$dir/checksums.txt" "$base/checksums.txt"; then
        echo "the release $tag lists $asset but it could not be downloaded" >&2
        return 1
    fi
    if ! (cd "$dir" && sha256sum --check --ignore-missing --quiet checksums.txt); then
        echo "$asset does not match the release's checksums.txt" >&2
        return 1
    fi
    tar -C "$dir" -xzf "$dir/$asset"
    rm -f "$dir/$asset"
    printf '%s\n' "$dir/server"
}
FIRST="$(previous_release "$SCRATCH/previous")" || die "the latest release's payload"
# a release from before the terminal setup (story 39.6) cannot be set up by this test
if [ -n "$FIRST" ]; then
    help="$("$FIRST/bin/maktaba-server" setup --help 2>&1 || true)"
    case "$help" in
        *--answers*) ;;
        *)
            log "the latest release's payload has no setup --answers (it is older than the terminal setup)"
            FIRST=""
            ;;
    esac
fi
if [ -n "$FIRST" ]; then
    log "set up from the latest release's payload ($("$PY" -I -c 'import json, sys; print(json.load(open(sys.argv[1]))["version"])' "$FIRST/payload.json"))"
else
    FIRST="$PAYLOAD"
    log "the latest release has no Linux payload for this machine: set up from the payload under test"
fi

# --- 1. setup --answers ------------------------------------------------------------------------
mkdir -p "$CONFIG" "$SCRATCH/media"
write_config() {
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
}
write_config
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
INSTALLED=1
setup_from() { "$1/bin/maktaba-server" setup --answers "$answers" --lang en </dev/null >"$SCRATCH/setup.log" 2>&1; }
if ! setup_from "$FIRST"; then
    [ "$FIRST" != "$PAYLOAD" ] || die "setup --answers"
    # the latest release's setup may want an older form of these answers: what it left is
    # removed, and the payload under test sets up instead (the update then runs from it)
    log "the latest release's setup --answers failed; setting up from the payload under test instead:"
    tail -n 6 "$SCRATCH/setup.log" >&2
    "$FIRST/bin/maktaba-server" service uninstall >/dev/null 2>&1 || true
    rm -rf "$DATA" "$XDG_STATE_HOME/maktaba" "$XDG_CACHE_HOME/maktaba"
    write_config
    FIRST="$PAYLOAD"
    setup_from "$FIRST" || die "setup --answers"
fi
tail -n 6 "$SCRATCH/setup.log"
rs="$(cli runtime status --json)"
[ "$(printf '%s' "$rs" | json_field owner)" = headless ] || die "the runtime's owner is not headless: $rs"
first="$(printf '%s' "$rs" | json_field current)"
[ -n "$first" ] || die "setup left no current runtime: $rs"
log "runtime: $first (owner headless)"

# --- 2. every role running ---------------------------------------------------------------------
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
st="$(cli service status --json)"
for key in installed enabled linger; do
    [ "$(printf '%s' "$st" | json_field "$key")" = true ] || die "service status: $key is not true: $st"
done
[ "$(printf '%s' "$st" | json_field active)" = active ] || die "service status: not active: $st"

# --- 3. restart, crash, pause ------------------------------------------------------------------
log "systemctl --user restart"
systemctl --user restart maktaba-server.service
wait_running 300

log "the launcher killed (SIGKILL): Restart=always brings it back"
pid="$(systemctl --user show --property=MainPID --value maktaba-server.service)"
if [ -z "$pid" ] || [ "$pid" = 0 ]; then die "the unit has no main process"; fi
kill -KILL "$pid"
sleep 8
wait_running 300
[ "$(systemctl --user show --property=MainPID --value maktaba-server.service)" != "$pid" ] || die "the launcher was not started again"

log "service stop: stays stopped"
cli service stop || die "service stop"
[ "$(systemctl --user is-active maktaba-server.service || true)" = inactive ] || die "the unit is not inactive after stop"
sleep 10
[ "$(systemctl --user is-active maktaba-server.service || true)" = inactive ] || die "a stopped unit started again"
if curl -fsS --max-time 3 "http://$SUPERVISOR/processes" >/dev/null 2>&1; then die "the supervisor still answers after stop"; fi

log "service start"
cli service start || die "service start"
wait_running 300

# --- 4. an update ------------------------------------------------------------------------------
# state/database.json: the release whose migrations the library database holds
db_record() {
    "$PY" -I -c 'import json, sys
d = json.load(open(sys.argv[1]))
print(d.get(sys.argv[2], ""))' "$DATA/state/database.json" "$1"
}
before_version="$(db_record version)" || die "no database record (state/database.json) after the first start"
[ "$(db_record state)" = migrated ] || die "the database is not recorded as migrated: $(cat "$DATA/state/database.json")"
# The venv: every update moves the runtime to a new folder, so the bootstrap repoints the venv
# at the new runtime's Python (`venv --upgrade`, which rewrites pyvenv.cfg) and keeps its
# packages when the constraints are the same. A rebuild reinstalls them: a dependency's RECORD
# (written by pip when it installs the package, by nothing else) and the venv.log lines the
# update adds tell the two apart.
[ -f "$DATA/venv/pyvenv.cfg" ] || die "no venv at $DATA/venv"
record="$(find "$DATA/venv/lib" -path '*/site-packages/*.dist-info/RECORD' ! -path '*/maktaba_pipeline-*' -print 2>/dev/null |
    LC_ALL=C sort | head -n 1)"
[ -n "$record" ] || die "no installed package in the venv"
record_before="$(stat -c %y "$record")"
venv_log_size="$(stat -c %s "$LOGS/venv.log" 2>/dev/null || echo 0)"
same_constraints=0
cmp -s "$FIRST/pipeline/constraints.txt" "$PAYLOAD/pipeline/constraints.txt" && same_constraints=1

log "the payload under test as $NEXT_VERSION: runtime install --activate"
mkdir -p "$SCRATCH/next"
cp -a "$PAYLOAD" "$SCRATCH/next/server"
"$PY" -I -c 'import json, sys
p = sys.argv[1]
d = json.load(open(p))
d["version"] = sys.argv[2]
json.dump(d, open(p, "w"), indent=2)' "$SCRATCH/next/server/payload.json" "$NEXT_VERSION"
"$SCRATCH/next/server/bin/maktaba-server" runtime install --from "$SCRATCH/next/server" --activate ||
    die "runtime install --activate"
wait_running 900

# the payload under test is the same release relabelled, so it adds no migration: no dump
# (the switch is a restart), and the record keeps the release whose migrations the
# database holds, naming the one that now runs on it (docs/updates.md, "The database
# during an update")
dump="$(find "$DATA" -name "pre-*-to-$NEXT_VERSION.dump" -print -quit)"
[ -z "$dump" ] || die "a database dump was taken for an update that adds no migration: ${dump#"$DATA"/}"
log "no dump for an update that adds no migration"
if [ "$(db_record version)" != "$before_version" ] || [ "$(db_record state)" != migrated ] ||
    [ "$(db_record release)" != "$NEXT_VERSION" ]; then
    die "the database is not recorded as $before_version's, run by $NEXT_VERSION: $(cat "$DATA/state/database.json")"
fi
if [ "$same_constraints" -eq 1 ]; then
    if [ ! -f "$record" ] || [ "$(stat -c %y "$record")" != "$record_before" ]; then
        die "the venv's packages were installed again although the constraints did not change (${record#"$DATA"/})"
    fi
    if tail -c +"$((venv_log_size + 1))" "$LOGS/venv.log" 2>/dev/null | grep -q 'pinned packages'; then
        die "the venv was rebuilt although the constraints did not change (venv.log)"
    fi
    log "the venv was kept (the same constraints): repointed, its packages untouched"
else
    log "the constraints changed: the venv was brought up to date"
fi
rs="$(cli runtime status --json)"
[ "$(printf '%s' "$rs" | json_field version)" = "$NEXT_VERSION" ] || die "current is not $NEXT_VERSION: $rs"
[ "$(printf '%s' "$rs" | json_field previous)" = "$first" ] || die "previous is not $first: $rs"
curl -fsS --max-time 10 http://127.0.0.1:8080/healthz >/dev/null || die "the API does not answer after the update"
log "updated to $NEXT_VERSION (previous: $first); migrations ran and the API answers"

# the distribution's ffmpeg (the payload carries none): the supervisor checks it at start
deadline=$((SECONDS + 120)) ff=""
while [ "$SECONDS" -lt "$deadline" ]; do
    ff="$(curl -fsS --max-time 5 "http://$SUPERVISOR/processes" 2>/dev/null | "$PY" -I -c 'import json, sys
f = json.load(sys.stdin).get("ffmpeg") or {}
p = f.get("problem") or {}
print("ok" if f.get("ok") else "problem " + p.get("code", "none yet"), f.get("source", ""), f.get("ffmpeg", ""), f.get("version", ""))' 2>/dev/null || true)"
    case "$ff" in "ok "* | "problem ffmpeg"* | "problem ffprobe"*) break ;; esac
    sleep 2
done
case "$ff" in
    "ok path /"*) log "ffmpeg: the distribution's (${ff#ok path })" ;;
    *) die "the supervisor's check of ffmpeg: ${ff:-no answer}" ;;
esac

log "all end-to-end steps passed"

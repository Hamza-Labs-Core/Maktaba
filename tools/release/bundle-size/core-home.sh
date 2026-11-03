#!/usr/bin/env bash
# A Core home, idle (Epic 51 story 51.17, N8 and N9): sets up a home from a Core Mac app's
# payload the way the desktop app does when Core is chosen (setup --json, one step per run:
# status, check, admin, libraries, bundle core, plex skipped, venv, finish; no models and no
# llm step, as the wizard skips them for Core), starts it as launchd does
# (maktaba-launcher serve, from Maktaba.app/Contents/Resources/server), waits until
# postgres, api, streaming and pipeline run and the API answers, idles, and measures:
#
#   processes    every process besides the launcher and the supervisor: the launcher's
#                children other than the supervisor, and the supervisor's children (N9:
#                exactly 4, the four above; Core runs no media worker)
#   idle_rss_mb  the resident memory of the launcher's whole tree, the supervisor and
#                postgres's own workers included (N8: at most 700 MB)
#
# Written as JSON to --out; tools/release/bundle-size/bundle_size.py judges the numbers.
# Hermetic: HOME is a scratch folder (the data, the config, the logs, the venv), removed at
# the end, the processes stopped first. The logs are copied to --logs before that. It
# starts servers on this machine's default ports, so it runs on CI (CI=true), elsewhere
# only with --force on a throwaway machine.
#
# Usage: core-home.sh --app DIR [--idle SECONDS] [--out FILE] [--logs DIR] [--force]
#   --app   the built Maktaba.app whose payload is the core flavour
#   --idle  seconds of idle before measuring (default 120)
#   --out   the measures' JSON (default: core-home.json)
#   --logs  where the home's logs are copied (default: core-home-logs)
set -euo pipefail

APP="" IDLE=120 OUT=core-home.json LOGS=core-home-logs FORCE=0
while [ $# -gt 0 ]; do
    case "$1" in
        --app)   [ $# -ge 2 ] || { echo "--app needs a value" >&2; exit 2; }; APP="$2"; shift 2 ;;
        --idle)  [ $# -ge 2 ] || { echo "--idle needs a value" >&2; exit 2; }; IDLE="$2"; shift 2 ;;
        --out)   [ $# -ge 2 ] || { echo "--out needs a value" >&2; exit 2; }; OUT="$2"; shift 2 ;;
        --logs)  [ $# -ge 2 ] || { echo "--logs needs a value" >&2; exit 2; }; LOGS="$2"; shift 2 ;;
        --force) FORCE=1; shift ;;
        -h|--help) sed -n '2,/^set -e/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//'; exit 0 ;;
        *)       echo "unknown option '$1'" >&2; exit 2 ;;
    esac
done
if [ "${CI:-}" != true ] && [ "$FORCE" -ne 1 ]; then
    echo "core-home.sh starts a Maktaba home on the default ports: it runs on CI, or with --force on a throwaway machine" >&2
    exit 2
fi
[ -n "$APP" ] || { echo "--app is required" >&2; exit 2; }
APP="$(cd "$APP" && pwd)"
PAYLOAD="$APP/Contents/Resources/server"
PY=/usr/bin/python3
SUPERVISOR=127.0.0.1:9103
ROLES="postgres api streaming pipeline"

log() { printf '\033[1;36m[core-home]\033[0m %s\n' "$*"; }
die() { printf '[core-home] error: %s\n' "$*" >&2; exit 1; }

[ -x "$PAYLOAD/bin/maktaba-launcher" ] || die "no payload in $APP"
flavour="$("$PY" -I -c 'import json,sys; print(json.load(open(sys.argv[1])).get("flavour","full"))' "$PAYLOAD/payload.json")"
[ "$flavour" = core ] || die "the app's payload is the $flavour flavour, not core"
[ ! -e "$PAYLOAD/ollama" ] || die "the core payload holds ollama/"

SCRATCH="$(mktemp -d "${TMPDIR:-/tmp}/core-home.XXXXXX")"
export HOME="$SCRATCH/home"
mkdir -p "$HOME" "$SCRATCH/media" "$LOGS"
LOGS="$(cd "$LOGS" && pwd)"
LPID=""

# tree PID: PID and every process under it, one per line
tree() {
    ps -A -o pid=,ppid= | "$PY" -I -c '
import sys
root = int(sys.argv[1])
kids = {}
for line in sys.stdin:
    pid, ppid = (int(x) for x in line.split())
    kids.setdefault(ppid, []).append(pid)
out, todo = [], [root]
while todo:
    p = todo.pop()
    out.append(p)
    todo.extend(kids.get(p, []))
print("\n".join(map(str, out)))' "$1"
}

cleanup() {
    local rc=$? pids
    set +e
    if [ -n "$LPID" ] && kill -0 "$LPID" 2>/dev/null; then
        pids="$(tree "$LPID")"
        kill -TERM "$LPID"
        for _ in $(seq 1 90); do kill -0 "$LPID" 2>/dev/null || break; sleep 1; done
        # anything left of the tree (a stop that hung) is killed: the runner is reused by nothing
        # shellcheck disable=SC2086
        kill -KILL $pids 2>/dev/null
    fi
    # the home's logs (the launcher's, the server's, the setup steps', the venv's), never its data
    find "$HOME" -name '*.log' -type f -size -50M -exec cp {} "$LOGS/" \; 2>/dev/null
    rm -rf "$SCRATCH"
    exit "$rc"
}
trap cleanup EXIT

# step NAME JSON: one setup step as the desktop app runs it; prints its result's data
step() {
    local name="$1" input="$2" out
    out="$(printf '%s' "$input" | "$PAYLOAD/bin/maktaba-server" setup --json --step "$name" 2>>"$LOGS/setup-steps.log")" || true
    printf '%s\n' "$out" >>"$LOGS/setup-steps.ndjson"
    printf '%s' "$out" | "$PY" -I -c '
import json, sys
name = sys.argv[1]
res = err = None
for line in sys.stdin:
    try:
        ev = json.loads(line)
    except ValueError:
        continue
    if ev.get("type") == "result":
        res = ev.get("data")
    elif ev.get("type") == "error":
        err = ev
if err is not None or res is None:
    print(f"setup step {name} failed: {json.dumps(err)}", file=sys.stderr)
    sys.exit(1)
print(json.dumps(res))' "$name" || die "setup step $name (see $LOGS/setup-steps.log)"
}

# field JSON KEY: KEY of a step's result (a boolean as true or false)
field() { printf '%s' "$1" | "$PY" -I -c 'import json, sys; v = json.load(sys.stdin).get(sys.argv[1]); print(str(v).lower() if isinstance(v, bool) else v)' "$2"; }

log "setup as the app runs it, Core chosen (HOME=$HOME)"
step status '{}' >/dev/null
check="$(step check '{}')"
[ "$(field "$check" ok)" = true ] || die "check: this machine cannot run the server: $check"
password="$(head -c 24 /dev/urandom | base64 | tr -d '/+=')"
step admin "{\"username\": \"ci\", \"password\": \"$password\"}" >/dev/null
step libraries "{\"roots\": [\"$SCRATCH/media\"], \"path_map\": []}" >/dev/null
choice="$(step bundle '{"bundle": "core"}')"
log "bundle: $choice"
[ "$(field "$choice" bundle)" = core ] || die "the bundle step chose $(field "$choice" bundle)"
[ "$(field "$choice" needs_models)" = false ] || die "Core needs a model: $choice"
[ "$(field "$choice" needs_llm)" = false ] || die "Core needs the language model: $choice"
[ "$(field "$choice" venv_core)" = true ] || die "Core's venv is not the core one: $choice"
step plex '{"action": "skip"}' >/dev/null
log "venv (constraints-core.txt, no ML extras)"
step venv '{}' >/dev/null
finish="$(step finish '{}')"
log "finished: $finish"

log "maktaba-launcher serve, as launchd runs it"
"$PAYLOAD/bin/maktaba-launcher" serve >"$LOGS/launcher.out" 2>&1 &
LPID=$!

# snapshot: the supervisor's /processes, or nothing
snapshot() { curl -fsS --max-time 5 "http://$SUPERVISOR/processes" 2>/dev/null || true; }

deadline=$((SECONDS + 900))
while :; do
    kill -0 "$LPID" 2>/dev/null || die "the launcher exited: $(tail -n 20 "$LOGS/launcher.out")"
    snap="$(snapshot)"
    if [ -n "$snap" ] && printf '%s' "$snap" | "$PY" -I -c '
import json, sys
want = set(sys.argv[1].split())
procs = json.load(sys.stdin).get("processes", [])
running = {p["role"] for p in procs if p.get("state") == "running"}
sys.exit(0 if want <= running else 1)' "$ROLES"; then
        break
    fi
    [ "$SECONDS" -lt "$deadline" ] || die "not every role ($ROLES) running after 900 s: $snap"
    sleep 3
done
api="$(printf '%s' "$snap" | "$PY" -I -c '
import json, sys
for p in json.load(sys.stdin).get("processes", []):
    if p["role"] == "api" and p.get("probe_url"):
        print(p["probe_url"]); break')"
if [ -n "$api" ]; then
    for _ in $(seq 1 60); do curl -fsS --max-time 5 "$api" >/dev/null 2>&1 && break; sleep 2; done
    curl -fsS --max-time 5 "$api" >/dev/null || die "the API does not answer at $api"
    log "the API answers at $api"
fi

log "running: $ROLES; idle for $IDLE s"
sleep "$IDLE"
snap="$(snapshot)"
[ -n "$snap" ] || die "the supervisor does not answer after the idle"
printf '%s\n' "$snap" >"$LOGS/processes.json"

ps -A -o pid=,ppid=,rss=,comm= >"$SCRATCH/ps.txt"
"$PY" -I - "$LPID" "$SCRATCH/ps.txt" "$LOGS/processes.json" "$OUT" "$ROLES" "$(cd "$(dirname "$0")" && pwd)" <<'EOF'
import json, sys

launcher, ps_path, snap_path, out_path = int(sys.argv[1]), sys.argv[2], sys.argv[3], sys.argv[4]
want = sys.argv[5].split()
sys.path.insert(0, sys.argv[6])
import bundle_size  # noqa: E402  (process_tree: what N9 counts, tested there)

with open(snap_path) as f:
    snap = json.load(f)
with open(ps_path) as f:
    try:
        t = bundle_size.process_tree(launcher, f.read())
    except ValueError as e:
        sys.exit(str(e))
rows = t["rows"]
rss_kb = sum(rows[p][1] for p in t["tree"])
live = ("starting", "running", "unhealthy", "backoff")
roles = sorted(p["role"] for p in snap.get("processes", []) if p.get("state") in live)
doc = {
    "processes": len(t["processes"]),
    "process_names": t["processes"],
    "helpers": t["helpers"],
    "roles": roles,
    "roles_expected": sorted(want),
    "idle_rss_mb": round(rss_kb * 1024 / 1_000_000, 1),
    "rss_mb_by_process": {f"{rows[p][2].rsplit('/', 1)[-1]}[{p}]": round(rows[p][1] * 1024 / 1_000_000, 1) for p in t["tree"]},
}
with open(out_path, "w") as f:
    json.dump(doc, f, indent=2)
print(json.dumps(doc, indent=2))
if roles != sorted(want):
    sys.exit(f"the supervisor runs {roles}, not exactly {sorted(want)}")
EOF
log "measures in $OUT"

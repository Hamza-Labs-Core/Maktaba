#!/usr/bin/env bash
# Smoke-tests the Maktaba.app server payload (Epic 36, story 36.7) and, with --app, the
# built app bundle. Runs on macOS arm64 (CI: the mac-app gate and desktop-release).
#
#   every binary starts (--version), every Mach-O is arm64 and links nothing outside the
#   payload but the system, Postgres initdb + start + the extensions Maktaba creates,
#   Ollama serves its API on a loopback port,
#   the bundled Python builds a venv offline, and running it writes nothing into the
#   payload (a new file would break the app's signature seal).
#
# Usage: check.sh [--payload DIR] [--app Maktaba.app] [--signed] [--notarized]
#   --payload  the payload (default: build/mac-app/server)
#   --app      also check the bundle: Resources/server, the LaunchAgent (when the app shell
#              provides one), the minimum macOS, and the launcher's app-bundle mode
#   --signed     also require valid signatures (codesign --verify --deep --strict)
#   --notarized  also require Gatekeeper to accept the app (spctl; needs notarization)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"

PAYLOAD="$REPO_ROOT/build/mac-app/server"
APP=""
SIGNED=0
NOTARIZED=0
while [ $# -gt 0 ]; do
    case "$1" in
        --payload) [ $# -ge 2 ] || { echo "--payload needs a value" >&2; exit 2; }; PAYLOAD="$2"; shift 2 ;;
        --app)     [ $# -ge 2 ] || { echo "--app needs a value" >&2; exit 2; }; APP="$2"; shift 2 ;;
        --signed)  SIGNED=1; shift ;;
        --notarized) SIGNED=1; NOTARIZED=1; shift ;;
        -h|--help) sed -n '2,/^set -e/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//'; exit 0 ;;
        *)         echo "unknown option '$1'" >&2; exit 2 ;;
    esac
done
if [ -n "$APP" ]; then
    APP="$(cd "$APP" && pwd)"
    PAYLOAD="$APP/Contents/Resources/server"
fi
PAYLOAD="$(cd "$PAYLOAD" && pwd)"

fails=0
ok()   { printf '  ok    %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }
# run NAME CMD...: CMD exits 0 within 30 s; its first output line is shown
run() {
    local name="$1" out rc=0; shift
    out="$(perl -e 'alarm 30; exec @ARGV' "$@" 2>&1)" || rc=$?
    if [ "$rc" -eq 0 ]; then ok "$name: $(printf '%s\n' "$out" | head -1)"; else fail "$name (exit $rc): $(printf '%s\n' "$out" | tail -3)"; fi
}

SCRATCH="$(mktemp -d "${TMPDIR:-/tmp}/mac-app-check.XXXXXX")"
trap 'rm -rf "$SCRATCH"' EXIT
touch "$SCRATCH/before"
sleep 1   # mtime granularity: anything the checks write into the payload is newer

echo "payload: $PAYLOAD"
echo "binaries"
for b in maktaba-server maktaba-api maktaba-streaming maktaba-launcher; do
    run "$b" "$PAYLOAD/bin/$b" --version
done
run "maktaba-cloudlink" "$PAYLOAD/bin/maktaba-cloudlink" version
for b in postgres initdb pg_ctl pg_dump pg_restore psql; do
    run "$b" "$PAYLOAD/postgres/bin/$b" --version
done
run ffmpeg "$PAYLOAD/ffmpeg/ffmpeg" -hide_banner -version
run ffprobe "$PAYLOAD/ffmpeg/ffprobe" -hide_banner -version
# HOME in the scratch folder: ollama keeps its key in ~/.ollama
run ollama env HOME="$SCRATCH" "$PAYLOAD/ollama/ollama" --version
run llama-server "$PAYLOAD/ollama/llama-server" --version
run python3 "$PAYLOAD/python/bin/python3" -I -c 'import sys, ssl, sqlite3, venv, ensurepip; print(sys.version.split()[0], ssl.OPENSSL_VERSION)'

echo "Mach-O files"
n=0 bad=0
while IFS= read -r f; do
    n=$((n + 1))
    archs="$(lipo -archs "$f" 2>/dev/null || true)"
    case " $archs " in *" arm64 "*) ;; *) fail "${f#"$PAYLOAD"/}: not arm64 ($archs)"; bad=$((bad + 1)) ;; esac
    # nothing from Homebrew, /usr/local or a build machine: only the system and the payload
    if otool -L "$f" | tail -n +2 | awk '{print $1}' |
        grep -vE '^(/usr/lib/|/System/Library/|@loader_path/|@rpath/|@executable_path/)' | grep -q .; then
        fail "${f#"$PAYLOAD"/}: links outside the payload: $(otool -L "$f" | tail -n +2 | awk '{print $1}' |
            grep -vE '^(/usr/lib/|/System/Library/|@loader_path/|@rpath/|@executable_path/)' | head -2 | tr '\n' ' ')"
        bad=$((bad + 1))
    fi
done < <(find "$PAYLOAD" -type f -print0 | xargs -0 file | awk -F': ' '/Mach-O/ {print $1}')
[ "$bad" -eq 0 ] && ok "$n Mach-O files, all arm64, self-contained"

echo "PostgreSQL"
# the only logical decoding output plugin is pgoutput: another one (test_decoding) would let a
# machine's replication role decode every table, past what is published to it (Epic 37)
plugins="$(find "$PAYLOAD/postgres/lib" -type f ! -name 'pgoutput.*' -exec grep -l -a -F _PG_output_plugin_init {} + 2>/dev/null || true)"
if [ -z "$plugins" ]; then ok "no output plugin but pgoutput"; else fail "output plugins besides pgoutput: $(printf '%s\n' "$plugins" | tr '\n' ' ')"; fi
pg="$PAYLOAD/postgres/bin"
port=$((54000 + RANDOM % 900))
if "$pg/initdb" -D "$SCRATCH/pgdata" -U maktaba --auth=trust -E UTF8 >"$SCRATCH/initdb.log" 2>&1 &&
    "$pg/pg_ctl" -D "$SCRATCH/pgdata" -l "$SCRATCH/pg.log" -w -t 30 \
        -o "-p $port -k '' -c listen_addresses=127.0.0.1" start >/dev/null 2>&1; then
    run "extensions" "$pg/psql" -h 127.0.0.1 -p "$port" -U maktaba -d postgres -Atq -v ON_ERROR_STOP=1 \
        -c "CREATE EXTENSION pg_trgm; CREATE EXTENSION pgcrypto; CREATE EXTENSION unaccent; SELECT 'pg_trgm, pgcrypto, unaccent on ' || version();"
    run "pg_dump" "$pg/pg_dump" -h 127.0.0.1 -p "$port" -U maktaba -Fc -f "$SCRATCH/dump" postgres
    "$pg/pg_ctl" -D "$SCRATCH/pgdata" -m fast -w stop >/dev/null 2>&1 || fail "pg_ctl stop"
else
    fail "initdb/start: $(tail -3 "$SCRATCH/initdb.log" "$SCRATCH/pg.log" 2>/dev/null)"
fi

echo "Ollama"
# the way the supervisor runs it (llmserver): loopback, its own port, the models folder
# outside the payload; it answers its API
oport=$((11600 + RANDOM % 300))
env HOME="$SCRATCH" OLLAMA_HOST="127.0.0.1:$oport" OLLAMA_MODELS="$SCRATCH/ollama-models" OLLAMA_NOPRUNE=1 \
    "$PAYLOAD/ollama/ollama" serve >"$SCRATCH/ollama.log" 2>&1 &
opid=$!
up=""
for _ in $(seq 1 100); do
    if up="$(curl -fsS "http://127.0.0.1:$oport/api/version" 2>/dev/null)"; then break; fi
    up=""
    sleep 0.2
done
if [ -n "$up" ]; then ok "ollama serve answers: $up"; else fail "ollama serve did not answer: $(tail -3 "$SCRATCH/ollama.log")"; fi
if [ -n "$up" ] && curl -fsS "http://127.0.0.1:$oport/api/tags" | grep -q '"models"'; then ok "ollama /api/tags"; else fail "ollama /api/tags"; fi
kill "$opid" 2>/dev/null || true
wait "$opid" 2>/dev/null || true

echo "Python"
run "venv (offline, with pip)" "$PAYLOAD/python/bin/python3" -I -m venv "$SCRATCH/venv"
if [ -x "$SCRATCH/venv/bin/pip" ]; then ok "venv has pip"; else fail "venv has no pip"; fi

echo "pipeline"
whl=("$PAYLOAD"/pipeline/maktaba_pipeline-*-py3-none-any.whl)
if [ -f "${whl[0]}" ]; then ok "wheel ${whl[0]##*/}"; else fail "no pipeline wheel"; fi
if [ -s "$PAYLOAD/pipeline/constraints.txt" ] && grep -q -- '--hash=sha256:' "$PAYLOAD/pipeline/constraints.txt"; then
    ok "constraints.txt (hashed)"
else
    fail "constraints.txt missing or without hashes"
fi
for heavy in mlx torch; do
    if find "$PAYLOAD" -iname "${heavy}*" -print -quit | grep -q .; then fail "$heavy is in the payload (it downloads on first run)"; fi
done
echo "database migrations"
# maktaba-api migrates from <payload>/migrations (the app runs from the user's data, never a checkout)
nsql=$(find "$PAYLOAD/migrations" -maxdepth 1 -name '*.sql' 2>/dev/null | wc -l | tr -d ' ')
if [ "$nsql" -gt 0 ]; then ok "migrations/ ($nsql files)"; else fail "no migrations/ beside bin/: the api cannot migrate the database"; fi
run "payload.json" "$PAYLOAD/python/bin/python3" -I -c \
    'import json,sys; d=json.load(open(sys.argv[1])); print("version", d["version"])' "$PAYLOAD/payload.json"

echo "payload untouched by the checks"
written="$(find "$PAYLOAD" -newer "$SCRATCH/before" | sed -n '1,5p')"
if [ -z "$written" ]; then ok "nothing written into the payload"; else fail "the checks wrote into the payload: $written"; fi

if [ -n "$APP" ]; then
    echo "app bundle: $APP"
    exe="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "$APP/Contents/Info.plist" 2>/dev/null || true)"
    if [ -n "$exe" ] && [ -x "$APP/Contents/MacOS/$exe" ]; then ok "Contents/MacOS/$exe"; else fail "no executable in Contents/MacOS ('$exe')"; fi
    minos="$(/usr/libexec/PlistBuddy -c 'Print :LSMinimumSystemVersion' "$APP/Contents/Info.plist" 2>/dev/null || true)"
    if [ "$minos" = "15.0" ]; then ok "LSMinimumSystemVersion $minos"; else fail "LSMinimumSystemVersion '$minos', want 15.0"; fi
    agent="Library/LaunchAgents/com.hamzalabs.maktaba.server.plist"
    if [ -f "$REPO_ROOT/apps/desktop/src-tauri/LaunchAgents/com.hamzalabs.maktaba.server.plist" ]; then
        if plutil -lint "$APP/Contents/$agent" >/dev/null 2>&1; then ok "Contents/$agent"; else fail "no valid Contents/$agent"; fi
    else
        ok "no LaunchAgent in the app shell yet (36.5): not expected"
    fi
    # the launcher recognises the bundle: app-bundle mode, updates with the app
    run "launcher app-bundle mode" "$PAYLOAD/bin/maktaba-launcher" --root "$SCRATCH/state" --status
    # the output first, then the match: grep -q ending the pipe early made the launcher
    # die of SIGPIPE, which pipefail reported as no match
    status="$("$PAYLOAD/bin/maktaba-launcher" --root "$SCRATCH/state" --status 2>/dev/null || true)"
    case "$status" in
        *'updates:    with the app'*) ok "launcher: updates come with the app" ;;
        *) fail "the launcher did not detect the app bundle" ;;
    esac
    # signed as a bundle (ad hoc at least): the server's login item registers only for one
    ident="$(codesign -dv "$APP" 2>&1 | sed -n 's/^Identifier=//p')"
    if [ "$ident" = "com.hamzalabs.maktaba" ]; then
        ok "the bundle is signed (com.hamzalabs.maktaba)"
    else
        fail "the bundle is not signed as com.hamzalabs.maktaba ('$ident'): its server login item cannot register"
    fi
    # a stable identity (MAKTABA_MAC_SIGN_IDENTITY): the designated requirement names
    # the certificate, not this build's hash, so macOS keeps its grants across updates
    if [ -n "${MAKTABA_MAC_SIGN_IDENTITY:-}" ]; then
        dr="$(codesign -d -r- "$APP" 2>&1 | sed -n 's/^designated => //p')"
        case "$dr" in
            *certificate*) ok "designated requirement: $dr" ;;
            *) fail "not signed with $MAKTABA_MAC_SIGN_IDENTITY (designated requirement: '$dr')" ;;
        esac
    fi
    if [ "$SIGNED" -eq 1 ]; then
        run "codesign --verify --deep --strict" codesign --verify --deep --strict "$APP"
    fi
    if [ "$NOTARIZED" -eq 1 ]; then
        run "Gatekeeper (spctl)" spctl --assess --type execute "$APP"
    fi
fi

if [ "$fails" -ne 0 ]; then
    echo "$fails check(s) failed"
    exit 1
fi
echo "all checks passed"

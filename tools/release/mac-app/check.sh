#!/usr/bin/env bash
# Smoke-tests the Maktaba.app server payload (Epic 36, story 36.7) and, with --app, the
# built app bundle. Runs on macOS arm64 (CI: the mac-app gate and desktop-release).
#
#   every binary starts (--version), every Mach-O is arm64 and links nothing outside the
#   payload but the system, Postgres initdb + start + the extensions Maktaba creates,
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
for b in postgres initdb pg_ctl pg_dump pg_restore psql; do
    run "$b" "$PAYLOAD/postgres/bin/$b" --version
done
run ffmpeg "$PAYLOAD/ffmpeg/ffmpeg" -hide_banner -version
run ffprobe "$PAYLOAD/ffmpeg/ffprobe" -hide_banner -version
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
    if "$PAYLOAD/bin/maktaba-launcher" --root "$SCRATCH/state" --status 2>/dev/null | grep -q 'updates:    with the app'; then
        ok "launcher: updates come with the app"
    else
        fail "the launcher did not detect the app bundle"
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

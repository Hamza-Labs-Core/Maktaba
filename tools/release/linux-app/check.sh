#!/usr/bin/env bash
# Smoke-tests the Linux server payload (Epic 39, story 39.2). It needs nothing from the
# machine but bash and coreutils, so CI runs it in minimal containers (ubuntu:22.04,
# debian:12-slim, fedora:40) as well as on the build machine:
#
#   files.sha256 matches the tree, every ELF file needs no glibc above the payload's limit
#   and finds each library it links (in the payload or the baseline), every binary starts
#   (--version), Postgres initdb + start + the extensions Maktaba creates + pg_dump, ffmpeg
#   encodes and ffprobe reads it back, the bundled Python makes a venv with pip offline,
#   the pipeline's wheel and hashed constraints are there (and no torch), the venv bootstrap
#   plans a build from them, and nothing the checks run writes into the payload.
#
# Usage: check.sh [--payload DIR]
#   --payload  the payload (default: build/linux-app/server)
# Postgres refuses to run as root: run it as an ordinary user.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"

PAYLOAD="$REPO_ROOT/build/linux-app/server"
while [ $# -gt 0 ]; do
    case "$1" in
        --payload) [ $# -ge 2 ] || { echo "--payload needs a value" >&2; exit 2; }; PAYLOAD="$2"; shift 2 ;;
        -h|--help) sed -n '2,/^set -e/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//'; exit 0 ;;
        *)         echo "unknown option '$1'" >&2; exit 2 ;;
    esac
done
PAYLOAD="$(cd "$PAYLOAD" && pwd)"
[ "$(id -u)" -ne 0 ] || { echo "check.sh: run it as an ordinary user (Postgres refuses root)" >&2; exit 2; }

fails=0
ok()   { printf '  ok    %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }
# run NAME CMD...: CMD exits 0 within 60 s; its first output line is shown
run() {
    local name="$1" out rc=0; shift
    out="$(timeout 60 "$@" 2>&1)" || rc=$?
    if [ "$rc" -eq 0 ]; then ok "$name: $(printf '%s\n' "$out" | head -1)"; else fail "$name (exit $rc): $(printf '%s\n' "$out" | tail -3)"; fi
}

SCRATCH="$(mktemp -d "${TMPDIR:-/tmp}/linux-app-check.XXXXXX")"
trap 'rm -rf "$SCRATCH"' EXIT
touch "$SCRATCH/before"
sleep 1   # mtime granularity: anything the checks write into the payload is newer

PY="$PAYLOAD/python/bin/python3"
tool() { "$PY" -I "$SCRIPT_DIR/payload_tool.py" "$@"; }

echo "payload: $PAYLOAD ($(sed -n 's/^PRETTY_NAME=//p' /etc/os-release 2>/dev/null | tr -d '"'), $(uname -m))"
echo "files"
run "files.sha256" "$PY" -I "$SCRIPT_DIR/payload_tool.py" verify "$PAYLOAD"
max_glibc="$("$PY" -I -c 'import json,sys; print(json.load(open(sys.argv[1]))["min_glibc"])' "$PAYLOAD/payload.json" 2>/dev/null || echo "")"
if [ -n "$max_glibc" ]; then
    if out="$(tool elf "$PAYLOAD" --max-glibc "$max_glibc" 2>&1)"; then
        ok "ELF files: $(printf '%s\n' "$out" | tail -1)"
    else
        fail "ELF files: $(printf '%s\n' "$out" | head -5)"
    fi
else
    fail "payload.json has no min_glibc"
fi
have="$(getconf GNU_LIBC_VERSION 2>/dev/null | awk '{print $2}' || true)"
ok "this system's glibc: ${have:-unknown} (the payload needs $max_glibc)"

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
run python3 "$PY" -I -c 'import sys, ssl, sqlite3, lzma, ctypes, venv, ensurepip; print(sys.version.split()[0], ssl.OPENSSL_VERSION)'

echo "PostgreSQL"
# the only logical decoding output plugin is pgoutput: another one (test_decoding) would let a
# machine's replication role decode every table, past what is published to it (Epic 37)
plugins="$(find "$PAYLOAD/postgres/lib" -type f ! -name 'pgoutput.*' -exec grep -l -a -F _PG_output_plugin_init {} + 2>/dev/null || true)"
if [ -z "$plugins" ]; then ok "no output plugin but pgoutput"; else fail "output plugins besides pgoutput: $(printf '%s\n' "$plugins" | tr '\n' ' ')"; fi
pg="$PAYLOAD/postgres/bin"
port=$((54000 + RANDOM % 900))
if "$pg/initdb" -D "$SCRATCH/pgdata" -U maktaba --auth=trust -E UTF8 --locale=C >"$SCRATCH/initdb.log" 2>&1 &&
    "$pg/pg_ctl" -D "$SCRATCH/pgdata" -l "$SCRATCH/pg.log" -w -t 30 \
        -o "-p $port -k '' -c listen_addresses=127.0.0.1" start >/dev/null 2>&1; then
    run "extensions" "$pg/psql" -h 127.0.0.1 -p "$port" -U maktaba -d postgres -Atq -v ON_ERROR_STOP=1 \
        -c "CREATE EXTENSION pg_trgm; CREATE EXTENSION pgcrypto; CREATE EXTENSION unaccent;
            SELECT 'pg_trgm, pgcrypto (' || encode(digest('x', 'sha256'), 'hex') || '), unaccent ('
                   || unaccent('Mak' || chr(233)) || ') on ' || version();"
    run "TLS (OpenSSL in the server)" "$pg/psql" -h 127.0.0.1 -p "$port" -U maktaba -d postgres -Atq \
        -c "SELECT 'ssl library: ' || current_setting('ssl_library');"
    run "pg_dump" "$pg/pg_dump" -h 127.0.0.1 -p "$port" -U maktaba -Fc -f "$SCRATCH/dump" postgres
    run "pg_restore --list" "$pg/pg_restore" --list "$SCRATCH/dump"
    "$pg/pg_ctl" -D "$SCRATCH/pgdata" -m fast -w stop >/dev/null 2>&1 || fail "pg_ctl stop"
else
    fail "initdb/start: $(tail -3 "$SCRATCH/initdb.log" "$SCRATCH/pg.log" 2>/dev/null)"
fi

echo "ffmpeg"
ff="$PAYLOAD/ffmpeg"
if timeout 60 "$ff/ffmpeg" -hide_banner -loglevel error -f lavfi -i testsrc=duration=1:size=320x240:rate=25 \
        -f lavfi -i sine=frequency=440:duration=1 -c:v libx264 -c:a aac -shortest "$SCRATCH/t.mp4" 2>"$SCRATCH/ff.log"; then
    run "ffprobe reads it back" "$ff/ffprobe" -v error -show_entries stream=codec_name -of csv=p=0 "$SCRATCH/t.mp4"
else
    fail "ffmpeg h264/aac encode: $(tail -2 "$SCRATCH/ff.log")"
fi

echo "Python"
run "venv (offline, with pip)" "$PY" -I -m venv "$SCRATCH/venv"
if [ -x "$SCRATCH/venv/bin/pip" ]; then ok "venv has pip"; else fail "venv has no pip"; fi

echo "pipeline"
whl=("$PAYLOAD"/pipeline/maktaba_pipeline-*-py3-none-any.whl)
if [ -f "${whl[0]}" ]; then ok "wheel ${whl[0]##*/}"; else fail "no pipeline wheel"; fi
if [ -s "$PAYLOAD/pipeline/constraints.txt" ] && grep -q -- '--hash=sha256:' "$PAYLOAD/pipeline/constraints.txt"; then
    ok "constraints.txt (hashed)"
else
    fail "constraints.txt missing or without hashes"
fi
for heavy in torch nvidia mlx; do
    if find "$PAYLOAD" -iname "${heavy}*" -print -quit | grep -q .; then fail "$heavy is in the payload (it downloads on first run)"; fi
done
# the bootstrap plans a build (exit 10: a run would change the venv) without touching anything
rc=0
out="$(PYTHONPATH="${whl[0]}" timeout 60 "$PY" -m maktaba_pipeline.venv_bootstrap --payload "$PAYLOAD/pipeline" \
    --venv "$SCRATCH/pipeline-venv" --check 2>&1)" || rc=$?
if [ "$rc" -eq 10 ] && printf '%s' "$out" | grep -q '"action": *"build"'; then
    ok "venv bootstrap plans: $(printf '%s\n' "$out" | tail -1)"
else
    fail "venv bootstrap --check (exit $rc): $(printf '%s\n' "$out" | tail -2)"
fi
run "payload.json" "$PY" -I -c \
    'import json,sys; d=json.load(open(sys.argv[1])); print(d["version"], d["platform"], "glibc", d["min_glibc"], d["files"]["count"], "entries")' \
    "$PAYLOAD/payload.json"
case "$(uname -m)" in
    x86_64)  want=linux-x86_64 ;;
    aarch64) want=linux-aarch64 ;;
    *)       want="linux-$(uname -m)" ;;
esac
if grep -q "\"platform\": \"$want\"" "$PAYLOAD/payload.json"; then ok "platform $want"; else fail "payload.json is not for $want"; fi

echo "payload untouched by the checks"
written="$(find "$PAYLOAD" -newer "$SCRATCH/before" | sed -n '1,5p')"
if [ -z "$written" ]; then ok "nothing written into the payload"; else fail "the checks wrote into the payload: $written"; fi

if [ "$fails" -ne 0 ]; then
    echo "$fails check(s) failed"
    exit 1
fi
echo "all checks passed"

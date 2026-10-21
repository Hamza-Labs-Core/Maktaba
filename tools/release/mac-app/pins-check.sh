#!/usr/bin/env bash
# Dry run of the payload's pinned downloads (Epic 36, story 36.7): checks pins.env (every
# piece has an https URL naming its version and a SHA-256) and the fetch payload.sh uses
# (lib.sh), offline, against a local file: a matching download is kept and used again, a
# cached file that does not match is fetched again, and a download whose checksum differs
# from the pin stops the build. Downloads nothing; runs anywhere bash, curl and shasum do.
#
# Usage: pins-check.sh [--online]
#   --online  also ask each pinned URL whether it is there (HEAD, following redirects)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ONLINE=0
case "${1:-}" in
    --online) ONLINE=1 ;;
    "") ;;
    -h|--help) sed -n '2,/^set -e/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option '$1'" >&2; exit 2 ;;
esac

fails=0
ok()   { printf '  ok    %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }
die() { printf 'fetch: error: %s\n' "$*" >&2; exit 1; }
log() { :; }

# shellcheck source-path=SCRIPTDIR source=pins.env
. "$SCRIPT_DIR/pins.env"
# shellcheck source-path=SCRIPTDIR source=lib.sh
. "$SCRIPT_DIR/lib.sh"

echo "pins.env"
if problems="$(check_pins)"; then
    for p in "${PINNED[@]}"; do
        k="${p}_URL"
        ok "$p ${!k##*/}"
    done
else
    fail "$problems"
fi
if [[ "${OLLAMA_URL:-}" == *"/v${OLLAMA_VERSION:-}/"* ]]; then
    ok "Ollama pinned to release v$OLLAMA_VERSION"
else
    fail "OLLAMA_URL does not point at release v${OLLAMA_VERSION:-?}"
fi

echo "fetch (offline)"
SCRATCH="$(mktemp -d "${TMPDIR:-/tmp}/pins-check.XXXXXX")"
trap 'rm -rf "$SCRATCH"' EXIT
CACHE="$SCRATCH/cache"
mkdir -p "$CACHE"
printf 'the release asset\n' >"$SCRATCH/asset"
url="file://$SCRATCH/asset"
want="$(sha256 "$SCRATCH/asset")"

if got="$(fetch "$url" "$want" asset.tgz 2>/dev/null)" && [ "$got" = "$CACHE/asset.tgz" ] && cmp -s "$got" "$SCRATCH/asset"; then
    ok "a matching download is cached"
else
    fail "a matching download was not cached ($got)"
fi
rm "$SCRATCH/asset"   # the cache alone serves it now
if (fetch "$url" "$want" asset.tgz >/dev/null 2>&1); then ok "the cached file is used (no download)"; else fail "the cached file was not used"; fi

printf 'a partial or tampered file\n' >"$CACHE/asset.tgz"
printf 'the release asset\n' >"$SCRATCH/asset"
if (fetch "$url" "$want" asset.tgz >/dev/null 2>&1) && cmp -s "$CACHE/asset.tgz" "$SCRATCH/asset"; then
    ok "a cached file that does not match is fetched again"
else
    fail "a bad cached file was kept"
fi

printf 'not what was pinned\n' >"$SCRATCH/other"
if out="$( (fetch "file://$SCRATCH/other" "$want" other.tgz) 2>&1)"; then
    fail "a download that does not match the pin was accepted"
elif [[ "$out" == *"pins.env says $want"* ]]; then
    ok "a download that does not match the pin stops the build"
else
    fail "a mismatch stopped with: $out"
fi
if (fetch "file://$SCRATCH/missing" "$want" missing.tgz >/dev/null 2>&1); then
    fail "a failed download was accepted"
else
    ok "a failed download stops the build"
fi

if [ "$ONLINE" -eq 1 ]; then
    echo "pinned URLs (online)"
    for p in "${PINNED[@]}"; do
        k="${p}_URL"
        if curl -fsIL --retry 2 -o /dev/null "${!k}"; then ok "$p is there"; else fail "$p: ${!k} is not there"; fi
    done
fi

if [ "$fails" -ne 0 ]; then
    echo "$fails check(s) failed"
    exit 1
fi
echo "all pin checks passed"

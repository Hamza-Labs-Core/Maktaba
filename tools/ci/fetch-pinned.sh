#!/usr/bin/env bash
# A pinned download for a CI job, checked against its SHA-256 and retried with backoff.
#
#   fetch-pinned.sh URL SHA256 DEST    DEST holds URL's bytes, checked; prints DEST
#   fetch-pinned.sh --key URL SHA256   the actions/cache key for that file
#
# A DEST already there (a restored cache) is checked first: with the pinned SHA-256 it is
# kept and nothing is downloaded; with other bytes it is removed and fetched again. A
# download is tried up to 5 times, 10, 20, 40 then 80 s apart (FETCH_PINNED_DELAYS
# overrides the waits, for the tests): a 5xx, a 408 or 429, a reset, a refused connection
# or a timeout is tried again; any other 4xx stops at once (the pin is wrong, not the
# host). A download whose SHA-256 is not the pinned one fails the job, as it always did.
# Every failure ends in an `::error::` that names the URL.
#
# GitHub's release downloads answered 504 for minutes at a time (2026-11-08): curl's
# --retry 3 waits 1, 2 then 4 s and gave up 11 s in. The release scripts' own fetch
# (tools/release/*/payload.sh) finds the file this put in their download folder, checks
# it and downloads nothing.
#
# The key is pinned-<runner OS>-<file name in the URL>-<first 16 hex of the SHA-256>: the
# file name carries the tool, its version and its platform, and a new pin misses.
# tools/ci/tests/test_fetch_pinned.py tests it against a local server.
set -euo pipefail

err() { printf '::error::%s\n' "$*"; exit 1; }
log() { printf 'fetch-pinned: %s\n' "$*" >&2; }

sha256() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

if [ "${1:-}" = --key ]; then
    [ $# -eq 3 ] || err "usage: fetch-pinned.sh --key URL SHA256"
    name="${2%%\?*}"; name="${name##*/}"
    os="$(printf '%s' "${RUNNER_OS:-$(uname -s)}" | tr '[:upper:]' '[:lower:]')"
    printf 'pinned-%s-%s-%s\n' "$os" "$name" "${3:0:16}"
    exit 0
fi

[ $# -eq 3 ] || err "usage: fetch-pinned.sh URL SHA256 DEST"
url="$1" want="$2" dst="$3"
[[ "$want" =~ ^[0-9a-f]{64}$ ]] || err "$url: pinned SHA-256 '$want' is not 64 hex digits"
mkdir -p "$(dirname "$dst")"
name="${dst##*/}"

if [ -f "$dst" ]; then
    if [ "$(sha256 "$dst")" = "$want" ]; then
        log "$name: cached copy has the pinned SHA-256 ($want), not downloaded"
        printf '%s\n' "$dst"
        exit 0
    fi
    log "$name: cached copy has other bytes, fetching it again"
    rm -f "$dst"
fi

read -r -a delays <<<"${FETCH_PINNED_DELAYS:-10 20 40 80}"
attempts=$((${#delays[@]} + 1))
part="$dst.part"
for ((i = 1; i <= attempts; i++)); do
    log "$name: downloading $url (attempt $i of $attempts)"
    rc=0
    code="$(curl -sSL --connect-timeout 30 --max-time 900 -o "$part" -w '%{http_code}' "$url")" || rc=$?
    if [ "$rc" -eq 0 ] && [ "$code" -ge 200 ] && [ "$code" -lt 300 ]; then
        got="$(sha256 "$part")"
        if [ "$got" != "$want" ]; then
            rm -f "$part"
            err "$name: $url gave SHA-256 $got, the pin says $want"
        fi
        mv "$part" "$dst"
        log "$name: downloaded, SHA-256 checked"
        printf '%s\n' "$dst"
        exit 0
    fi
    rm -f "$part"
    [ "$code" != 000 ] || code=
    if [ "$rc" -eq 0 ]; then
        case "$code" in
            408 | 429 | 5??) why="HTTP $code" ;;
            *) err "$name: $url answered HTTP $code, which another try will not change" ;;
        esac
    else
        why="curl exit $rc${code:+ (HTTP $code)}"
    fi
    if [ "$i" -lt "$attempts" ]; then
        d="${delays[$((i - 1))]}"
        log "$name: $why, again in $d s"
        sleep "$d"
    else
        err "$name: $url not downloaded after $attempts attempts (last: $why)"
    fi
done

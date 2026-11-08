#!/usr/bin/env bash
# A pinned download, checked against its SHA-256 and retried with backoff: the fetch of the
# app payloads (tools/release/{mac,linux,win}-app/payload.sh) and of CI jobs.
#
#   fetch-pinned.sh URL SHA256 DEST    DEST holds URL's bytes, checked; prints DEST
#   fetch-pinned.sh --key URL SHA256   the actions/cache key for that file
#
# A DEST already there (a restored cache) is checked first: with the pinned SHA-256 it is
# kept and nothing is downloaded; with other bytes it is removed and fetched again. A
# download is tried up to 5 times, 10, 20, 40 then 80 s apart (FETCH_PINNED_DELAYS
# overrides the waits, for the tests): a 5xx, a 408 or 429, a reset, a refused connection
# or a timeout is tried again; any other 4xx stops at once (the pin is wrong, not the
# host). A github.com release download goes by the REST API's route to the same asset
# first, the github.com URL second, in each try (see api_asset_url). A download whose
# SHA-256 is not the pinned one fails the job, as it always did, whichever route gave it.
# Every failure ends in an `::error::` that names the URL.
#
# GitHub's release downloads answered 504 for minutes at a time (2026-11-08): curl's
# --retry 3 waits 1, 2 then 4 s and gave up 11 s in.
#
# The key is pinned-<runner OS>-<file name in the URL>-<first 16 hex of the SHA-256>: the
# file name carries the tool, its version and its platform, and a new pin misses.
# tools/ci/tests/test_fetch_pinned.py tests it against a local server.
set -euo pipefail

# on stderr: the payload scripts read the path from stdout (Actions reads commands on both)
err() { printf '::error::%s\n' "$*" >&2; exit 1; }
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

# A github.com release download (/OWNER/REPO/releases/download/TAG/FILE) has a second route
# to the same asset: the REST API's asset endpoint (the release by its tag, the asset by its
# name, then the asset with Accept: application/octet-stream). github.com's front door
# answered 504 to the first request for minutes (2026-11-08) while the API served the file
# at once, so the API's route goes first and the URL itself second, in every try, with
# $GITHUB_TOKEN when the job passes one (never printed); the SHA-256 decides either way.
# pins.env keeps the github.com URL: owner, repo, tag and name come from it.
# FETCH_PINNED_GITHUB and FETCH_PINNED_API point both at a test server.
gh_base="${FETCH_PINNED_GITHUB:-https://github.com}"
api_base="${FETCH_PINNED_API:-https://api.github.com}"
api_url="" api_looked=""
# api_asset_url: sets api_url to the asset's API URL, or leaves it empty (looked up once)
api_asset_url() {
    [ -n "$api_looked" ] || {
        api_looked=1
        local rest repo tag file json auth=()
        case "$url" in "$gh_base"/*/*/releases/download/*/*) ;; *) return ;; esac
        command -v jq >/dev/null 2>&1 || { log "$name: no jq, so no API route"; return; }
        rest="${url#"$gh_base"/}"
        repo="${rest%%/releases/download/*}"
        rest="${rest#*/releases/download/}"
        tag="${rest%%/*}" file="${rest#*/}"
        file="$(printf '%b' "${file//%/\\x}")" # the asset's name, %2B as +
        [ -z "${GITHUB_TOKEN:-}" ] || auth=(-H "Authorization: Bearer $GITHUB_TOKEN")
        json="$(curl -fsS --connect-timeout 30 --max-time 60 ${auth[@]+"${auth[@]}"} \
            -H 'Accept: application/vnd.github+json' "$api_base/repos/$repo/releases/tags/$tag")" ||
            { api_looked=""; log "$name: the API's release $repo $tag did not answer"; return; }
        api_url="$(jq -r --arg f "$file" 'first(.assets[] | select(.name == $f) | .url) // empty' <<<"$json")"
        [ -n "$api_url" ] || log "$name: the API's release $repo $tag has no asset $file"
    }
}

# get FROM [CURL ARGS...]: one try into $part; sets code and rc
get() {
    local from="$1"
    shift
    rc=0
    code="$(curl -sSL --connect-timeout 30 --max-time 900 ${1+"$@"} -o "$part" -w '%{http_code}' "$from")" || rc=$?
    # 000 with exit 0: not HTTP (file://, which pins-check.sh's offline test uses)
    if [ "$rc" -eq 0 ] && { [ "$code" = 000 ] || { [ "$code" -ge 200 ] && [ "$code" -lt 300 ]; }; }; then
        return 0
    fi
    rm -f "$part"
    [ "$code" != 000 ] || code=
    return 1
}

keep() {
    local got
    got="$(sha256 "$part")"
    if [ "$got" != "$want" ]; then
        rm -f "$part"
        err "$name: $1 gave SHA-256 $got, the pin says $want"
    fi
    mv "$part" "$dst"
    log "$name: downloaded from $1, SHA-256 checked"
    printf '%s\n' "$dst"
    exit 0
}

read -r -a delays <<<"${FETCH_PINNED_DELAYS:-10 20 40 80}"
attempts=$((${#delays[@]} + 1))
part="$dst.part"
auth=()
[ -z "${GITHUB_TOKEN:-}" ] || auth=(-H "Authorization: Bearer $GITHUB_TOKEN")
for ((i = 1; i <= attempts; i++)); do
    api_why=""
    api_asset_url
    if [ -n "$api_url" ]; then
        log "$name: downloading by the GitHub API, $api_url (attempt $i of $attempts)"
        get "$api_url" -H 'Accept: application/octet-stream' ${auth[@]+"${auth[@]}"} && keep "$api_url"
        if [ "$rc" -eq 0 ]; then api_why="API route HTTP $code"; else api_why="API route curl exit $rc"; fi
        log "$name: $api_why; trying $url"
    else
        log "$name: downloading $url (attempt $i of $attempts)"
    fi
    get "$url" && keep "$url"
    if [ "$rc" -eq 0 ]; then
        case "$code" in
            408 | 429 | 5??) why="HTTP $code" ;;
            *) err "$name: $url answered HTTP $code, which another try will not change" ;;
        esac
    else
        case "$rc" in
            # an unsupported scheme, a malformed URL, a local file that is not there
            1 | 3 | 37) err "$name: $url failed with curl exit $rc, which another try will not change" ;;
        esac
        why="curl exit $rc${code:+ (HTTP $code)}"
    fi
    why="${api_why:+$api_why; }$why"
    if [ "$i" -lt "$attempts" ]; then
        d="${delays[$((i - 1))]}"
        log "$name: $why, again in $d s"
        sleep "$d"
    else
        err "$name: $url not downloaded after $attempts attempts (last: $why)"
    fi
done

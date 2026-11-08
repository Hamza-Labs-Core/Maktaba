# shellcheck shell=bash
# The pinned downloads of the Maktaba.app payload (Epic 36, story 36.7): shared by
# payload.sh and pins-check.sh. Sourced; needs $CACHE and the die/log functions.

# The pieces pins.env pins: <PREFIX>_URL and <PREFIX>_SHA256 each (and <PREFIX>_VERSION
# for the ones with a version of their own; ffprobe shares ffmpeg's).
PINNED=(POSTGRES PGVECTOR FFMPEG FFPROBE PYTHON OLLAMA)

sha256() { shasum -a 256 "$1" | awk '{print $1}'; }

# fetch URL SHA256 NAME: the file in the cache, downloaded once, always checked. A
# cached file that does not match (a partial or a tampered download) is fetched again;
# a download that does not match stops the build. tools/release/fetch-pinned.sh does it:
# 5 tries 10-80 s apart for a 5xx, 408, 429, reset or timeout, and for a github.com
# release asset the API's route to it too (GitHub's release downloads answered 504 for
# minutes on 2026-11-08, past curl's --retry 3).
LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fetch() {
    "$LIB_DIR/../fetch-pinned.sh" "$1" "$2" "$CACHE/$3" || die "$3: download failed ($1)"
}

# check_pins: every piece has an https URL and a SHA-256, and its URL names its version.
# Prints one line per problem; returns non-zero when there is one.
check_pins() {
    local p k url sum ver bad=0
    for p in "${PINNED[@]}"; do
        k="${p}_URL";    url="${!k:-}"
        k="${p}_SHA256"; sum="${!k:-}"
        k="${p}_VERSION"; ver="${!k:-}"
        [ "$p" = FFPROBE ] && ver="${FFMPEG_VERSION:-}"
        case "$url" in https://*) ;; *) echo "$p: URL '$url' is not https"; bad=1 ;; esac
        [[ "$sum" =~ ^[0-9a-f]{64}$ ]] || { echo "$p: SHA-256 '$sum' is not 64 hex digits"; bad=1; }
        if [ -z "$ver" ]; then
            echo "$p: no version"; bad=1
        elif [[ "$url" != *"$ver"* ]]; then
            echo "$p: URL does not name version $ver"; bad=1
        fi
    done
    return "$bad"
}

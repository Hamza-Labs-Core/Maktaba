# shellcheck shell=bash
# The pinned downloads of the Maktaba.app payload (Epic 36, story 36.7): shared by
# payload.sh and pins-check.sh. Sourced; needs $CACHE and the die/log functions.

# The pieces pins.env pins: <PREFIX>_URL and <PREFIX>_SHA256 each (and <PREFIX>_VERSION
# for the ones with a version of their own; ffprobe shares ffmpeg's).
PINNED=(POSTGRES FFMPEG FFPROBE PYTHON OLLAMA)

sha256() { shasum -a 256 "$1" | awk '{print $1}'; }

# fetch URL SHA256 NAME: the file in the cache, downloaded once, always checked. A
# cached file that does not match (a partial or a tampered download) is fetched again;
# a download that does not match stops the build.
fetch() {
    local url="$1" want="$2" dst="$CACHE/$3" got
    if [ ! -f "$dst" ] || [ "$(sha256 "$dst")" != "$want" ]; then
        log "downloading $3"
        curl -fsSL --retry 3 -o "$dst.part" "$url" || die "$3: download failed ($url)"
        mv "$dst.part" "$dst"
    fi
    got="$(sha256 "$dst")"
    [ "$got" = "$want" ] || die "$3: sha256 $got, pins.env says $want"
    printf '%s\n' "$dst"
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

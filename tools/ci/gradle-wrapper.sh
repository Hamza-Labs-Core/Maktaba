#!/usr/bin/env bash
# The Gradle wrapper in CI (the Android apps, the Android TV app, the desktop Player, and their
# release workflows): its jar checked, its distribution fetched before the build, both retried.
#
#   gradle-wrapper.sh check PROJECT...    each PROJECT's gradle/wrapper/gradle-wrapper.jar is
#                                         Gradle's own for the version its properties name
#                                         (Gradle's published SHA-256, fetched with retries)
#   gradle-wrapper.sh --key PROJECT       the actions/cache key for its distribution zip
#   gradle-wrapper.sh dist PROJECT DIR    the distribution zip in DIR, fetched by
#                                         tools/release/fetch-pinned.sh (5 tries with backoff,
#                                         checked against distributionSha256Sum; a zip already
#                                         in DIR with that SHA-256 is kept), then put where
#                                         the wrapper looks for it, so ./gradlew downloads
#                                         nothing; nothing at all when the wrapper already has
#                                         that distribution installed
#
# The wrapper's own download (one try, 10 s timeout) of services.gradle.org failed the desktop
# Player on Linux and Windows on 2026-11-08. The wrapper keeps a distribution in
# $GRADLE_USER_HOME/wrapper/dists/<zip name without .zip>/<hash>/, the hash being the MD5 of
# distributionUrl in base 36 (Gradle's PathAssembler); a zip there with no .ok beside it is
# unpacked (and checked against distributionSha256Sum) rather than downloaded.
# GRADLE_WRAPPER_DELAYS overrides the waits between the jar-checksum tries, for
# tools/ci/tests/test_gradle_wrapper.py.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fetch="$here/../release/fetch-pinned.sh"

err() { printf '::error::%s\n' "$*" >&2; exit 1; }

# prop PROJECT NAME: NAME's value in PROJECT's wrapper properties, with \: as :
prop() {
    local file="$1/gradle/wrapper/gradle-wrapper.properties"
    [ -f "$file" ] || err "$file is not there"
    sed -n "s/^$2=//p" "$file" | tr -d '\r' | sed 's/\\:/:/g'
}

sha256() {
    if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi
}

md5hex() {
    if command -v md5sum >/dev/null 2>&1; then printf '%s' "$1" | md5sum | awk '{print $1}'; else printf '%s' "$1" | md5 -q; fi
}

# base36 HEX: the unsigned number HEX in base 36, as Java's BigInteger(1, bytes).toString(36)
base36() {
    local hex="$1" digits=() out="" alphabet=0123456789abcdefghijklmnopqrstuvwxyz i rem acc nz
    for ((i = 0; i < ${#hex}; i++)); do digits+=($((16#${hex:i:1}))); done
    while :; do
        rem=0 nz=0
        for ((i = 0; i < ${#digits[@]}; i++)); do
            acc=$((rem * 16 + digits[i]))
            digits[i]=$((acc / 36))
            rem=$((acc % 36))
            [ "${digits[i]}" -eq 0 ] || nz=1
        done
        out="${alphabet:rem:1}$out"
        [ "$nz" -eq 1 ] || break
    done
    printf '%s\n' "$out"
}

# text URL: URL's body, tried up to 5 times, 10, 20, 40 then 80 s apart
text() {
    local delays i body
    read -r -a delays <<<"${GRADLE_WRAPPER_DELAYS:-10 20 40 80}"
    for ((i = 0; ; i++)); do
        body="$(curl -fsSL --connect-timeout 30 --max-time 60 "$1")" && { printf '%s\n' "$body"; return 0; }
        [ "$i" -lt "${#delays[@]}" ] || err "$1 did not answer after $((i + 1)) tries"
        printf 'gradle-wrapper: %s did not answer, again in %s s\n' "$1" "${delays[$i]}" >&2
        sleep "${delays[$i]}"
    done
}

case "${1:-}" in
    check)
        shift
        [ $# -ge 1 ] || err "usage: gradle-wrapper.sh check PROJECT..."
        for project in "$@"; do
            url="$(prop "$project" distributionUrl)"
            base="${url%.zip}"
            base="${base%-bin}"
            jar_sums="${base%-all}-wrapper.jar.sha256"
            want="$(text "$jar_sums")"
            got="$(sha256 "$project/gradle/wrapper/gradle-wrapper.jar")"
            [ "$want" = "$got" ] || err "the Gradle wrapper jar in $project is not Gradle's (it is $got, $jar_sums says $want)"
            echo "$project/gradle/wrapper/gradle-wrapper.jar is Gradle's ($got)"
        done
        ;;
    --key)
        [ $# -eq 2 ] || err "usage: gradle-wrapper.sh --key PROJECT"
        "$fetch" --key "$(prop "$2" distributionUrl)" "$(prop "$2" distributionSha256Sum)"
        ;;
    dist)
        [ $# -eq 3 ] || err "usage: gradle-wrapper.sh dist PROJECT DIR"
        url="$(prop "$2" distributionUrl)"
        want="$(prop "$2" distributionSha256Sum)"
        [ -n "$want" ] || err "$2's gradle-wrapper.properties has no distributionSha256Sum"
        zip="${url##*/}"
        dists="${GRADLE_USER_HOME:-$HOME/.gradle}/wrapper/dists/${zip%.zip}/$(base36 "$(md5hex "$url")")"
        if [ -f "$dists/$zip.ok" ]; then
            echo "the wrapper has $zip installed in $dists: nothing to fetch"
            exit 0
        fi
        got="$("$fetch" "$url" "$want" "$3/$zip")"
        mkdir -p "$dists"
        cp "$got" "$dists/$zip"
        echo "$zip put in $dists for the wrapper (SHA-256 $want)"
        ;;
    *) err "usage: gradle-wrapper.sh check PROJECT... | --key PROJECT | dist PROJECT DIR" ;;
esac

#!/usr/bin/env bash
# The Android version of a release tag: the native apps' bundles (play-bundles.yml,
# docs/google-play.md) and the Capacitor app's APK and AAB (mobile-release.yml):
#
#   play-version.sh vMAJOR.MINOR.PATCH[-PRE]
#   name=MAJOR.MINOR.PATCH[-PRE]                     the versionName (the tag without its v)
#   code=(MAJOR*1000000+MINOR*1000+PATCH)*100+STEP   the versionCode, the same for every app
#
# STEP orders a version's pre-releases below the version itself, as semver does:
#   -alpha.N  N      (N 1..32: 1..32)
#   -beta.N   32+N   (N 1..32: 33..64)
#   -rc.N     64+N   (N 1..34: 65..98)
#   (none)    99     the stable release
# so v0.4.0-rc.1 is 400065, v0.4.0-rc.2 400066 and v0.4.0 400099: no two tags share a code, and
# every tag's code is above the one cut before it. Play refuses a bundle whose versionCode it
# already has, or one not above the last one uploaded. MINOR and PATCH stay under 1000, the code
# at or under Play's limit of 2100000000 (MAJOR up to 20). Until v0.3.54 the code was
# MAJOR*1000000+MINOR*1000+PATCH (v0.3.54 was 3054, its pre-releases sharing it; the Capacitor
# app kept it to v0.3.55, 3055); every code here is above those, so no app needs an offset. Any other tag is refused with a message, so a tag Play would refuse fails
# before any build.
set -euo pipefail

tag="${1:-}"
err() { echo "play-version: $*" >&2; exit 1; }

[[ "${tag}" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-([0-9A-Za-z.-]+))?$ ]] \
    || err "'${tag}' is not vMAJOR.MINOR.PATCH[-PRE]"
major="${BASH_REMATCH[1]}"
minor="${BASH_REMATCH[2]}"
patch="${BASH_REMATCH[3]}"
pre="${BASH_REMATCH[5]}"
[ "${#major}" -le 2 ] || err "${tag}: MAJOR is too large for a versionCode"
[ "${minor}" -lt 1000 ] || err "${tag}: MINOR must be under 1000 for the versionCode"
[ "${patch}" -lt 1000 ] || err "${tag}: PATCH must be under 1000 for the versionCode"
[ "$((major + minor + patch))" -ge 1 ] || err "${tag}: v0.0.0 has no versionCode"

step=99
if [ -n "${pre}" ]; then
    [[ "${pre}" =~ ^(alpha|beta|rc)\.([1-9][0-9]?)$ ]] \
        || err "${tag}: a pre-release must be -alpha.N, -beta.N or -rc.N (N from 1) for the versionCode"
    kind="${BASH_REMATCH[1]}"
    n="${BASH_REMATCH[2]}"
    case "${kind}" in
        alpha) max=32; step=$((n)) ;;
        beta)  max=32; step=$((32 + n)) ;;
        rc)    max=34; step=$((64 + n)) ;;
    esac
    [ "${n}" -le "${max}" ] || err "${tag}: -${kind}.N goes up to ${max} for the versionCode"
fi

code=$(((major * 1000000 + minor * 1000 + patch) * 100 + step))
[ "${code}" -le 2100000000 ] || err "${tag}: versionCode ${code} is above Google Play's limit (2100000000)"

echo "name=${tag#v}"
echo "code=${code}"

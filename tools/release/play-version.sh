#!/usr/bin/env bash
# The Google Play version of a release tag (play-bundles.yml, docs/google-play.md):
#
#   play-version.sh vMAJOR.MINOR.PATCH[-PRE]
#   name=MAJOR.MINOR.PATCH[-PRE]          the apps' versionName (the tag without its v)
#   code=MAJOR*1000000+MINOR*1000+PATCH   the versionCode, the same for the three apps
#
# Play refuses a bundle whose versionCode is not above the last one uploaded, so the code grows
# with every tag: MINOR and PATCH stay under 1000, and the code at or under Play's limit of
# 2100000000 (MAJOR up to 2099). A pre-release has its version's code (v0.4.0-rc.1 and v0.4.0
# are both 4000): only the first of them uploaded can reach Play. Anything else is refused with
# a message, so a tag Play would refuse fails before any build.
set -euo pipefail

tag="${1:-}"
err() { echo "play-version: $*" >&2; exit 1; }

[[ "${tag}" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$ ]] \
    || err "'${tag}' is not vMAJOR.MINOR.PATCH[-PRE]"
major="${BASH_REMATCH[1]}"
minor="${BASH_REMATCH[2]}"
patch="${BASH_REMATCH[3]}"
[ "${#major}" -le 4 ] || err "${tag}: MAJOR is too large for a versionCode"
[ "${minor}" -lt 1000 ] || err "${tag}: MINOR must be under 1000 for the versionCode"
[ "${patch}" -lt 1000 ] || err "${tag}: PATCH must be under 1000 for the versionCode"
code=$((major * 1000000 + minor * 1000 + patch))
[ "${code}" -ge 1 ] || err "${tag}: the versionCode must be at least 1"
[ "${code}" -le 2100000000 ] || err "${tag}: versionCode ${code} is above Google Play's limit (2100000000)"

echo "name=${tag#v}"
echo "code=${code}"

#!/usr/bin/env bash
# The latest release's Linux payload, for e2e.sh's update from a real previous release.
#
#   previous-release.sh REPO DIR
#
# Unpacks the latest release's maktaba-linux-payload-<version>-<arch>.tar.gz in DIR and
# prints DIR/server; prints nothing (status 0) when the latest release has no payload for
# this machine, or the release lookup never answers (the setup then runs from the payload
# under test, as it always did). A payload the release lists but that cannot be fetched or
# checked is a failure (status 1, the reason on stderr).
#
# Both files go through tools/release/fetch-pinned.sh (the GitHub API's route first, then
# github.com, 5 tries with backoff): checksums.txt pinned by the SHA-256 digest the release
# lists for it, the payload by its line in that checksums.txt. A plain curl of github.com's
# release download got a 504 and failed the Linux app job (2026-11-08). The lookup of the
# latest release is retried the same way (PREVIOUS_RELEASE_DELAYS overrides the waits,
# FETCH_PINNED_API and FETCH_PINNED_GITHUB the hosts, for
# tools/ci/tests/test_previous_release.py). GITHUB_TOKEN, when set, is sent to the API only
# and never printed.
set -euo pipefail

[ $# -eq 2 ] || { echo "usage: previous-release.sh REPO DIR" >&2; exit 2; }
repo="$1" dir="$2"
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fetch="$here/../fetch-pinned.sh"
api_base="${FETCH_PINNED_API:-https://api.github.com}"
gh_base="${FETCH_PINNED_GITHUB:-https://github.com}"
py="${PY:-python3}"
read -r -a delays <<<"${PREVIOUS_RELEASE_DELAYS:-10 20 40 80}"
auth=()
[ -z "${GITHUB_TOKEN:-}" ] || auth=(-H "Authorization: Bearer $GITHUB_TOKEN")

for ((i = 0; ; i++)); do
    api="$(curl -fsSL --connect-timeout 30 --max-time 60 ${auth[@]+"${auth[@]}"} \
        -H 'Accept: application/vnd.github+json' "$api_base/repos/$repo/releases/latest" 2>/dev/null)" && break
    if [ "$i" -ge "${#delays[@]}" ]; then
        echo "previous-release: the latest release's lookup did not answer after $((i + 1)) tries: no previous release" >&2
        exit 0
    fi
    echo "previous-release: the latest release's lookup did not answer, again in ${delays[$i]} s" >&2
    sleep "${delays[$i]}"
done

tag="$(printf '%s' "$api" | "$py" -I -c 'import json, sys; print(json.load(sys.stdin).get("tag_name", ""))')"
[ -n "$tag" ] || exit 0
asset="maktaba-linux-payload-${tag#v}-$(uname -m).tar.gz"
# the asset's presence, and checksums.txt's digest ("sha256:<hex>", or nothing)
read -r has sums < <(printf '%s' "$api" | "$py" -I -c 'import json, sys
assets = json.load(sys.stdin).get("assets", [])
has = any(a.get("name") == sys.argv[1] for a in assets)
d = next((a.get("digest") or "" for a in assets if a.get("name") == "checksums.txt"), "")
print("yes" if has else "no", d.removeprefix("sha256:") if d.startswith("sha256:") else "-")' "$asset")
[ "$has" = yes ] || exit 0
if [ "$sums" = - ]; then
    echo "the release $tag lists no SHA-256 digest for checksums.txt" >&2
    exit 1
fi

mkdir -p "$dir"
base="$gh_base/$repo/releases/download/$tag"
if ! "$fetch" "$base/checksums.txt" "$sums" "$dir/checksums.txt" >/dev/null; then
    echo "the release $tag lists checksums.txt but it could not be downloaded" >&2
    exit 1
fi
# sha256sum's lines: "<hex>  <name>", the name as given (./name) or in binary mode (*name)
want="$(awk -v f="$asset" '$2 == f || $2 == "./" f || $2 == "*" f || $2 == "*./" f { print $1; exit }' "$dir/checksums.txt")"
if [ -z "$want" ]; then
    echo "the release $tag's checksums.txt has no line for $asset" >&2
    exit 1
fi
if ! "$fetch" "$base/$asset" "$want" "$dir/$asset" >/dev/null; then
    echo "the release $tag lists $asset but it could not be downloaded with its checksums.txt SHA-256" >&2
    exit 1
fi
tar -C "$dir" -xzf "$dir/$asset"
rm -f "$dir/$asset"
printf '%s\n' "$dir/server"

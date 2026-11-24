#!/usr/bin/env bash
# Gathers what the release feed is written from (update-feed.yml; tools/release/releasefeed):
# the newest published releases as `gh release view` prints them, and each one's three signed
# files.
#
#   release-feed-inputs.sh OUT [COUNT]
#
# writes OUT/views.json (a JSON array, newest first) and OUT/signed/<tag>/{checksums.txt,
# checksums.txt.sigstore.json,release.json}. COUNT (default 30) is how many published releases
# it reads: the feed lists 10 a channel, and this leaves room for pulled ones and pre-releases.
# A release without its signed files (before story 35.1) gets an empty folder; release-feed
# generate leaves it out. Every GitHub call goes through gh-retry.sh.
#
# Env: GH_TOKEN, GITHUB_REPOSITORY.
set -euo pipefail

out="${1:?OUT}"
count="${2:-30}"
here="$(cd "$(dirname "$0")" && pwd)"
retry="${here}/gh-retry.sh"
mkdir -p "${out}/signed" "${out}/views"

"${retry}" gh release list --repo "${GITHUB_REPOSITORY:?}" --exclude-drafts --limit "${count}" \
    --json tagName --jq '.[].tagName' >"${out}/tags.txt"
n=0
while IFS= read -r tag; do
    case "${tag}" in v[0-9]*.[0-9]*.[0-9]*) ;; *) continue ;; esac
    "${retry}" gh release view "${tag}" --repo "${GITHUB_REPOSITORY}" \
        --json tagName,isDraft,isPrerelease,publishedAt,url,assets >"${out}/views/${tag}.json"
    mkdir -p "${out}/signed/${tag}"
    # a release from before story 35.1 has none of them: an empty folder
    "${retry}" gh release download "${tag}" --repo "${GITHUB_REPOSITORY}" --dir "${out}/signed/${tag}" --clobber \
        --pattern checksums.txt --pattern checksums.txt.sigstore.json --pattern release.json ||
        echo "release-feed-inputs: ${tag} has no signed files"
    n=$((n + 1))
done <"${out}/tags.txt"
if [ "${n}" -eq 0 ]; then
    echo "::error::release-feed-inputs: no published release" >&2
    exit 1
fi
jq -s '.' "${out}"/views/*.json >"${out}/views.json"
echo "release-feed-inputs: ${n} releases read"

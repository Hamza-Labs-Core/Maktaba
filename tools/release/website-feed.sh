#!/usr/bin/env bash
# Puts the release feed into maktaba.tv's build before it deploys (website-deploy.yml; the feed:
# update-feed.yml, docs/updates.md "How a home finds a release").
#
#   website-feed.sh DEST          (DEST: web/packages/website/dist/updates/v1)
#
# The newest update-feed artifact of a run on main goes in DEST as update-feed.yml signed it.
# Without one (none yet, or expired), the feed maktaba.tv serves now is carried over file by
# file (each channel's index.json names its files), so a deploy of the site never drops the
# feed; homes verify every file's signature whichever way it came. With neither, the site
# deploys without a feed and says so: homes then read GitHub's list.
#
# Env: GH_TOKEN (actions: read), REPO (owner/name); FEED_LIVE (default
# https://maktaba.tv/updates/v1).
set -euo pipefail

dest="${1:?DEST}"
repo="${REPO:?REPO}"
live="${FEED_LIVE:-https://maktaba.tv/updates/v1}"
here="$(cd "$(dirname "$0")" && pwd)"
mkdir -p "${dest}"

summary() { echo "$1"; echo "$1" >>"${GITHUB_STEP_SUMMARY:-/dev/null}"; }

run="$("${here}/gh-retry.sh" gh api "repos/${repo}/actions/artifacts?name=update-feed&per_page=50" \
    --jq '[.artifacts[] | select(.expired | not) | select(.workflow_run.head_branch == "main")]
          | sort_by(.created_at) | last | .workflow_run.id // empty' || true)"
if [ -n "${run}" ] && "${here}/gh-retry.sh" gh run download "${run}" --repo "${repo}" -n update-feed -D "${dest}"; then
    if [ -f "${dest}/stable/index.json" ]; then
        summary "The release feed: update-feed.yml run ${run}'s, under /updates/v1/."
        exit 0
    fi
    echo "::warning::website-feed: run ${run}'s update-feed artifact has no stable/index.json" >&2
    rm -rf "${dest:?}"/*
fi

# carry over what is live
n=0
for ch in stable beta; do
    mkdir -p "${dest}/${ch}"
    if ! curl -fsS --retry 3 -o "${dest}/${ch}/index.json" "${live}/${ch}/index.json"; then
        rm -f "${dest}/${ch}/index.json"
        continue
    fi
    files="$(jq -r '.platforms[].file' "${dest}/${ch}/index.json")"
    for f in index.json ${files}; do
        case "${f}" in *[!A-Za-z0-9._-]* | "" | .*) echo "::error::website-feed: a bad file name ${f}" >&2; exit 1 ;; esac
        [ "${f}" = index.json ] || curl -fsS --retry 3 -o "${dest}/${ch}/${f}" "${live}/${ch}/${f}"
        curl -fsS --retry 3 -o "${dest}/${ch}/${f}.sigstore.json" "${live}/${ch}/${f}.sigstore.json"
        n=$((n + 1))
    done
done
if [ "${n}" -gt 0 ]; then
    summary "The release feed: no update-feed artifact; the ${n} files maktaba.tv serves now are carried over."
else
    rm -rf "${dest:?}"
    echo "::warning title=release feed::no update-feed artifact and none live: maktaba.tv deploys without a release feed (homes read GitHub's list until update-feed.yml runs)"
fi

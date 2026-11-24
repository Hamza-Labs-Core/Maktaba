#!/usr/bin/env bash
# Puts the release feed into maktaba.tv's build before it deploys (website-deploy.yml; the feed:
# update-feed.yml, docs/updates.md "How a home finds a release").
#
#   website-feed.sh DEST          (DEST: web/packages/website/dist/updates/v1)
#
# The newest update-feed artifact of an update-feed.yml run on main (its own triggers only, never
# a pull request's or another workflow's) goes in DEST as update-feed.yml signed it, when it holds
# only feed files.
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

# only a file a feed has: <channel>/<platform or index>.json and its .sigstore.json
feed_names_ok() {
    local bad
    bad="$(cd "$1" && find . -type f | grep -Ev '^\./(stable|beta)/[a-z0-9-]+\.json(\.sigstore\.json)?$' || true)"
    if [ -n "${bad}" ]; then
        echo "::warning::website-feed: files a feed never has: ${bad}" >&2
        return 1
    fi
    [ -f "$1/stable/index.json" ]
}

# the newest run of update-feed.yml itself, on main, started by its own triggers (no pull
# request, no other workflow): the artifact a run of this repository's main uploaded
runs="$("${here}/gh-retry.sh" gh run list --repo "${repo}" --workflow update-feed.yml --branch main --limit 20 \
    --json databaseId,event --jq '.[] | select(.event == "workflow_run" or .event == "workflow_dispatch" or .event == "schedule") | .databaseId' || true)"
for run in ${runs}; do
    rm -rf "${dest:?}" && mkdir -p "${dest}"
    if "${here}/gh-retry.sh" gh run download "${run}" --repo "${repo}" -n update-feed -D "${dest}" 2>/dev/null; then
        if feed_names_ok "${dest}"; then
            summary "The release feed: update-feed.yml run ${run}'s, under /updates/v1/."
            exit 0
        fi
        echo "::warning::website-feed: run ${run}'s update-feed artifact is not a feed: passed over" >&2
    fi
done
rm -rf "${dest:?}" && mkdir -p "${dest}"

# carry over what is live: a file that cannot be fetched fails the deploy rather than drop
# the feed (homes would then read GitHub's list)
n=0
for ch in stable beta; do
    mkdir -p "${dest}/${ch}"
    if ! curl -fsS --retry 3 -o "${dest}/${ch}/index.json" "${live}/${ch}/index.json"; then
        rm -f "${dest}/${ch}/index.json"
        continue
    fi
    files="$(jq -r '.platforms[].file' "${dest}/${ch}/index.json")"
    # no globbing of what the live index names (each name is checked below)
    set -f
    for f in index.json ${files}; do
        case "${f}" in *[!A-Za-z0-9._-]* | "" | .*) echo "::error::website-feed: a bad file name ${f}" >&2; exit 1 ;; esac
        [ "${f}" = index.json ] || curl -fsS --retry 3 -o "${dest}/${ch}/${f}" "${live}/${ch}/${f}"
        curl -fsS --retry 3 -o "${dest}/${ch}/${f}.sigstore.json" "${live}/${ch}/${f}.sigstore.json"
        n=$((n + 1))
    done
    set +f
done
if [ "${n}" -gt 0 ]; then
    summary "The release feed: no update-feed artifact; the ${n} files maktaba.tv serves now are carried over."
else
    rm -rf "${dest:?}"
    echo "::warning title=release feed::no update-feed artifact and none live: maktaba.tv deploys without a release feed (homes read GitHub's list until update-feed.yml runs)"
fi

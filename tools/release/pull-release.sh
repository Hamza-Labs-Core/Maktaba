#!/usr/bin/env bash
# Pulls a release from the release feed (docs/updates.md, "Pulling a release"), or puts one back.
#
#   pull-release.sh VERSION REASON     every home marks VERSION withdrawn and never installs it
#   pull-release.sh --restore VERSION  takes it back out (homes that marked it keep it withdrawn)
#
# The pulled releases are the repository variable UPDATE_FEED_WITHDRAWN, a JSON object of
# version to reason; update-feed.yml reads it and writes the feed again, which this starts.
# Needs a gh signed in with admin rights on the repository (variables); no secret is written.
#
# Env: REPO (default Hamza-Labs-Core/Maktaba).
set -euo pipefail

repo="${REPO:-Hamza-Labs-Core/Maktaba}"
restore=false
if [ "${1:-}" = "--restore" ]; then
    restore=true
    shift
fi
ver="${1:?VERSION}"
ver="${ver#v}"
if ! [[ "${ver}" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]]; then
    echo "pull-release: ${ver} is not a version (X.Y.Z)" >&2
    exit 2
fi
# the pulls so far: none only when the variable does not exist; any other failure stops here,
# so an earlier pull is never undone by a read that failed
errf="$(mktemp)"
trap 'rm -f "${errf}"' EXIT
if ! cur="$(gh variable get UPDATE_FEED_WITHDRAWN --repo "${repo}" 2>"${errf}")"; then
    if grep -qi "not found" "${errf}"; then
        cur='{}'
    else
        cat "${errf}" >&2
        echo "pull-release: UPDATE_FEED_WITHDRAWN could not be read: nothing changed" >&2
        exit 1
    fi
fi
[ -n "${cur}" ] || cur='{}'
if ! jq -e 'type == "object"' >/dev/null <<<"${cur}"; then
    echo "pull-release: UPDATE_FEED_WITHDRAWN is not a JSON object: fix it by hand" >&2
    exit 1
fi
if "${restore}"; then
    next="$(jq -c --arg v "${ver}" 'del(.[$v])' <<<"${cur}")"
else
    reason="${2:?REASON}"
    next="$(jq -c --arg v "${ver}" --arg r "${reason}" '. + {($v): $r}' <<<"${cur}")"
fi
gh variable set UPDATE_FEED_WITHDRAWN --repo "${repo}" --body "${next}"
gh workflow run update-feed.yml --repo "${repo}" --ref main
echo "pull-release: UPDATE_FEED_WITHDRAWN is now ${next}; update-feed.yml started (Actions → Update feed)"
echo "pull-release: homes before the feed (v0.3.153 to v0.3.161) learn of a pull only from GitHub: mark it a pre-release there too while any remain"

#!/usr/bin/env bash
# The tag-order guard: the first job of every tag workflow (release.yml, desktop-release.yml,
# mobile-release.yml, tv-release.yml, through .github/workflows/_tag-guard.yml). A tag is
# released only when:
#   - it is above every published release (tools/release/releaseorder): v0.3.0-rc.7, tagged
#     after v0.3.0, was offered by no updater, and machines on it were offered the older
#     0.3.0. A tag already published is its own release run again (allowed);
#   - its commit is on main;
#   - CI passed on that commit (the CI workflow's run for it; a run still going is waited for).
# A deliberate maintenance release of an older line (story 22.5 EC1) is named in the
# repository variable MAKTABA_RELEASE_ALLOW_TAG: it passes the order and main checks (not
# the CI one), and never becomes the latest (publish: releaseorder.Latest).
#
# Env: TAG, SHA, GH_TOKEN, GITHUB_REPOSITORY; ALLOW_TAG (the variable); CI_WORKFLOW
# (default ci.yml); CI_WAIT_SECONDS (default 3600). Writes maintenance=true|false to
# $GITHUB_OUTPUT when set.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
: "${TAG:?}" "${SHA:?}"
ci="${CI_WORKFLOW:-ci.yml}"
wait_s="${CI_WAIT_SECONDS:-3600}"

err() { echo "::error::tag guard: $*" >&2; exit 1; }

# 1. its place among the published releases
published="$(gh release list --exclude-drafts --limit 1000 --json tagName --jq '.[].tagName')"
if ! out="$(printf '%s\n' "${published}" | (cd "${here}" && go run ./cmd/release-order guard --tag "${TAG}" \
    --allow "${ALLOW_TAG:-}") 2>&1)"; then
    err "${out#release-order: refused: }"
fi
maintenance="${out#maintenance=}"
echo "tag guard: ${TAG} may be released (maintenance: ${maintenance})"

# 2. on main (a maintenance release comes from its own branch)
if [ "${maintenance}" != true ]; then
    git fetch --quiet --no-tags origin +refs/heads/main:refs/remotes/origin/main
    git merge-base --is-ancestor "${SHA}" origin/main \
        || err "${TAG} points at ${SHA}, which is not on main: release from a commit main has (CI passed on it)"
fi

# 3. CI passed on the commit: its run for it (a push to main, or the pull request's head),
# waited for while it runs
deadline=$(( $(date +%s) + wait_s ))
while :; do
    runs="$(gh run list --workflow "${ci}" --commit "${SHA}" --limit 50 --json status,conclusion \
        --jq '[.[] | "\(.status) \(.conclusion)"] | .[]' 2>/dev/null || true)"
    if grep -q '^completed success$' <<<"${runs}"; then
        echo "tag guard: CI passed on ${SHA}"
        break
    fi
    if [ -n "${runs}" ] && ! grep -qv '^completed ' <<<"${runs}"; then
        err "CI did not pass on ${SHA} ($(tr '\n' ',' <<<"${runs}" | sed 's/,$//')): fix main, then tag the commit CI passed on"
    fi
    [ "$(date +%s)" -lt "${deadline}" ] \
        || err "no finished CI run on ${SHA} after ${wait_s}s (${runs:-none started}): tag a commit CI passed on"
    echo "tag guard: waiting for CI on ${SHA} (${runs:-not started yet})"
    sleep 30
done

if [ -n "${GITHUB_OUTPUT:-}" ]; then
    echo "maintenance=${maintenance}" >>"${GITHUB_OUTPUT}"
fi

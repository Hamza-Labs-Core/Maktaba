#!/usr/bin/env bash
# Checks, right after a release is published, that a home can read the release feed with it
# in it (the release-feed check: api/cmd/release-feed-check, the same code a home's update
# check runs). Since 2026-11-23 every home's check had failed on a list of releases longer
# than its cap, silently, for over a day: a release whose feed a home cannot read must fail
# its own workflow, loudly.
#
#   feed-check.sh TAG
#
# A release is published by whichever of release.yml and desktop-release.yml finishes last
# (release-assets.sh). Both run this after their publish step: while TAG is still a draft the
# other workflow publishes it and checks then, and this one says so and passes. Once TAG is
# published it must be selectable by a home's catalog, and every answer within 70% of its cap.
# GitHub's list can lag a publish by a moment: a release not listed yet is asked for again
# (FEED_CHECK_DELAYS, seconds, default "20 60").
#
# Env: GH_TOKEN (read; used only when GitHub rate-limits the anonymous read), GITHUB_REPOSITORY.
# FEED_CHECK_CMD overrides the check's command (the tests).
set -euo pipefail

tag="${1:?TAG}"
repo="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY}"
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "${here}/../.." && pwd)"

draft="$("${here}/gh-retry.sh" gh release view "${tag}" --repo "${repo}" --json isDraft --jq .isDraft)"
if [ "${draft}" != false ]; then
    echo "::notice::feed-check: ${tag} is still a draft; the workflow that publishes it checks the feed then"
    exit 0
fi

check() {
    if [ -n "${FEED_CHECK_CMD:-}" ]; then
        ${FEED_CHECK_CMD} -repo "${repo}" -expect "${tag}" -token-env GH_TOKEN
    else
        (cd "${root}/api" && go run ./cmd/release-feed-check -repo "${repo}" -expect "${tag}" -token-env GH_TOKEN)
    fi
}

read -r -a delays <<<"${FEED_CHECK_DELAYS:-20 60}"
attempt=0
while :; do
    out="$(mktemp)"
    if check >"${out}"; then
        cat "${out}"
        rm -f "${out}"
        exit 0
    fi
    # only a release GitHub does not list yet is asked for again; anything else fails now
    if [ "${attempt}" -lt "${#delays[@]}" ] && grep -q "is not in the" "${out}"; then
        echo "feed-check: ${tag} is not listed yet; asking again in ${delays[${attempt}]} s"
        rm -f "${out}"
        sleep "${delays[${attempt}]}"
        attempt=$((attempt + 1))
        continue
    fi
    cat "${out}"
    rm -f "${out}"
    echo "::error title=Release feed::${tag} is published, but a home cannot read the release feed with it, or soon cannot (above). Homes will not find this release: fix the feed (docs/updates.md, \"The release feed's caps\") before the next one."
    exit 1
done

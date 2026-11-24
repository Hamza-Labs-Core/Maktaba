#!/usr/bin/env bash
# Checks, right after a release is published, that a home can read GitHub's release list with it
# in it (the release-release list check: api/cmd/release-list-check, the same code a home's update
# check runs). Since 2026-11-23 every home's check had failed on a list of releases longer
# than its cap, silently, for over a day: a release a home cannot find in the list must fail
# its own workflow, loudly.
#
#   release-list-check.sh TAG
#
# A release is published by whichever of release.yml and desktop-release.yml finishes last
# (release-assets.sh). Both run this after their publish step: while TAG is still a draft the
# other workflow publishes it and checks then, and this one says so and passes. Once TAG is
# published it must be selectable by a home's catalog, and every answer within 70% of its cap.
# GitHub's list can lag a publish by a moment: a release not listed yet is asked for again
# (LIST_CHECK_DELAYS, seconds, default "20 60").
#
# Env: GH_TOKEN (read; used only when GitHub rate-limits the anonymous read), GITHUB_REPOSITORY.
# LIST_CHECK_CMD overrides the check's command (the tests).
set -euo pipefail

tag="${1:?TAG}"
repo="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY}"
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "${here}/../.." && pwd)"

draft="$("${here}/gh-retry.sh" gh release view "${tag}" --repo "${repo}" --json isDraft --jq .isDraft)"
if [ "${draft}" != false ]; then
    echo "::notice::release-list-check: ${tag} is still a draft; the workflow that publishes it checks the list then"
    exit 0
fi

check() {
    if [ -n "${LIST_CHECK_CMD:-}" ]; then
        ${LIST_CHECK_CMD} -repo "${repo}" -expect "${tag}" -token-env GH_TOKEN
    else
        (cd "${root}/api" && go run ./cmd/release-list-check -repo "${repo}" -expect "${tag}" -token-env GH_TOKEN)
    fi
}

read -r -a delays <<<"${LIST_CHECK_DELAYS:-20 60}"
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
        echo "release-list-check: ${tag} is not listed yet; asking again in ${delays[${attempt}]} s"
        rm -f "${out}"
        sleep "${delays[${attempt}]}"
        attempt=$((attempt + 1))
        continue
    fi
    cat "${out}"
    rm -f "${out}"
    echo "::error title=GitHub release list::${tag} is published, but a home cannot read GitHub's release list with it, or soon cannot (above). Homes will not find this release: fix the list (docs/updates.md, \"The caps on GitHub's release list\") before the next one."
    exit 1
done

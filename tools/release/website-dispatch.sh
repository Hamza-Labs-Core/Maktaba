#!/usr/bin/env bash
# Rebuilds maktaba.tv's download page for a release (Epic 60 story 60.11, "When it rebuilds").
#
#   website-dispatch.sh TAG
#
# A release is published by whichever of release.yml and desktop-release.yml finishes last
# (release-assets.sh), and becomes GitHub's latest only as the highest stable version with every
# desktop and player platform in its feeds. Both workflows run this after their publish step:
# when TAG is now the latest release, it starts website-deploy.yml on main (workflow_dispatch,
# release=TAG), whose build reads the release from the GitHub Releases API, so the download page
# names the new version within one deploy. The deploy goes live only with vars.WEBSITE_LIVE, as
# every deploy does. Anything else (a draft, a pre-release, a release that is not the latest, a
# re-run whose tag a newer one has passed) dispatches nothing and says so. The job that runs it
# blocks no other release job (nothing needs it, and it continues on error).
#
# Env: GH_TOKEN (actions: write, to dispatch), GITHUB_REPOSITORY.
set -euo pipefail

tag="${1:?TAG}"
repo="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY}"
case "${tag}" in
    v[0-9]*.[0-9]*.[0-9]*) ;;
    *)
        echo "::error::website-dispatch: ${tag} is not a release tag (vX.Y.Z)" >&2
        exit 1
        ;;
esac
if [[ "${tag}" == *-* ]]; then
    echo "website-dispatch: ${tag} is a pre-release; the download page shows stable releases only"
    exit 0
fi

latest="$(gh api "repos/${repo}/releases/latest" --jq .tag_name 2>/dev/null || true)"
if [ "${latest}" != "${tag}" ]; then
    echo "website-dispatch: ${tag} is not the latest release (the latest: ${latest:-none}); maktaba.tv is not rebuilt for it"
    exit 0
fi

gh workflow run website-deploy.yml --repo "${repo}" --ref main -f release="${tag}"
echo "website-dispatch: website-deploy.yml started on main for ${tag}"
{
    echo "### maktaba.tv"
    echo
    echo "The download page is being rebuilt for ${tag} (website-deploy.yml on main; it goes live only with vars.WEBSITE_LIVE)."
} >> "${GITHUB_STEP_SUMMARY:-/dev/null}"

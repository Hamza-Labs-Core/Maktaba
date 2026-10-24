#!/usr/bin/env bash
# A tag's GitHub release, as every tag workflow touches it (release.yml, desktop-release.yml,
# mobile-release.yml, tv-release.yml):
#
#   release-assets.sh ensure  TAG              the release exists, as a draft until it is complete
#                                              (NOTES set: the draft's notes)
#   release-assets.sh upload  TAG [--replace NAME]... FILE...
#   release-assets.sh publish TAG              publish the draft once complete; mark the latest
#
# A release is public only when it is complete (H2/B2: a home that looked at a release
# before its release.json existed never verified it again):
#   - release.yml's signed set: release.json, checksums.txt and its Sigstore bundle;
#   - the apps that carry the server (darwin-aarch64, windows-x86_64, linux-x86_64,
#     linux-aarch64) in its latest.json, when the repository has the updater key
#     (vars.TAURI_UPDATER_PUBKEY: REQUIRE_FEED=true).
# Every workflow runs publish after its upload: the last one to finish publishes. It becomes
# GitHub's latest only as a stable version above every other published stable one
# (releaseorder.Latest: never a pre-release or a maintenance release), with every desktop
# platform in its feed.
#
# A published release is never rewritten (B5: a re-run of a tag replaced signed files homes
# had verified, and they withdrew it): upload adds only the files it lacks. release.yml's
# run refuses outright when its checksums.txt differs from the published one; --replace
# names the files that may change on a published release (desktop's latest.json, as late
# platforms land).
#
# Env: GH_TOKEN, GITHUB_REPOSITORY; NOTES (ensure); REQUIRE_FEED (publish).
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cmd="${1:?ensure|upload|publish}"
tag="${2:?TAG}"
shift 2

prerelease() { case "${tag}" in *-*) return 0 ;; esac; return 1; }

# The release's id, drafts included ("" when there is none): gh's own lookup by tag misses a
# draft whose tag the API has not resolved, so the list is read.
release_id() {
    gh api "repos/${GITHUB_REPOSITORY}/releases?per_page=100" --paginate \
        --jq ".[] | select(.tag_name == \"${tag}\") | .id" | head -1
}

is_draft() {
    [ "$(gh api "repos/${GITHUB_REPOSITORY}/releases/$1" --jq .draft)" = true ]
}

asset_names() {
    gh api "repos/${GITHUB_REPOSITORY}/releases/$1/assets?per_page=100" --paginate --jq '.[].name'
}

ensure() {
    local id
    id="$(release_id)"
    if [ -n "${id}" ]; then
        if is_draft "${id}"; then
            # the notes and the pre-release flag on the draft (a published release keeps its own)
            if [ -n "${NOTES:-}" ]; then
                if prerelease; then
                    gh release edit "${tag}" --title "${tag}" --notes "${NOTES}" --prerelease --latest=false
                else
                    gh release edit "${tag}" --title "${tag}" --notes "${NOTES}" --prerelease=false --latest=false
                fi
            fi
            echo "release ${tag}: there already (draft)"
        else
            echo "release ${tag}: there already (published)"
        fi
        return 0
    fi
    local kind=(--draft --latest=false)
    prerelease && kind+=(--prerelease)
    gh release create "${tag}" --verify-tag --title "${tag}" --notes "${NOTES:-Release ${tag}: in the making.}" "${kind[@]}" \
        || [ -n "$(release_id)" ] # made meanwhile by another workflow
    echo "release ${tag}: created as a draft"
}

upload() {
    local replace=() files=() id names f base r keep
    while [ $# -gt 0 ]; do
        case "$1" in
            --replace) replace+=("$2"); shift 2 ;;
            *) files+=("$1"); shift ;;
        esac
    done
    [ "${#files[@]}" -gt 0 ] || return 0
    ensure
    id="$(release_id)"
    if is_draft "${id}"; then
        gh release upload "${tag}" "${files[@]}" --clobber
        return 0
    fi
    names="$(asset_names "${id}")"
    # a published release: its signed set must be this run's, else nothing is changed
    for f in "${files[@]}"; do
        if [ "$(basename "${f}")" = checksums.txt ] && grep -qx checksums.txt <<<"${names}"; then
            local tmp
            tmp="$(mktemp -d)"
            gh release download "${tag}" --pattern checksums.txt --dir "${tmp}"
            if ! cmp -s "${tmp}/checksums.txt" "${f}"; then
                echo "::error::${tag} is published and this run's files differ from it (checksums.txt):" \
                    "a published release is never rewritten. Tag a new version." >&2
                diff "${tmp}/checksums.txt" "${f}" >&2 || true
                exit 1
            fi
        fi
    done
    for f in "${files[@]}"; do
        base="$(basename "${f}")"
        keep=1
        for r in "${replace[@]+"${replace[@]}"}"; do [ "${r}" = "${base}" ] && keep=0; done
        if grep -qx "${base}" <<<"${names}" && [ "${keep}" = 1 ]; then
            echo "release ${tag} is published: ${base} kept as it is"
            continue
        fi
        gh release upload "${tag}" "${f}" --clobber
    done
}

publish() {
    local id names missing=() tmp all=false
    id="$(release_id)"
    [ -n "${id}" ] || { echo "release ${tag}: none yet"; return 0; }
    names="$(asset_names "${id}")"
    for a in release.json checksums.txt checksums.txt.sigstore.json; do
        grep -qx "${a}" <<<"${names}" || missing+=("${a} (release.yml)")
    done
    tmp="$(mktemp -d)"
    if grep -qx latest.json <<<"${names}"; then
        gh release download "${tag}" --pattern latest.json --dir "${tmp}"
        if [ "${REQUIRE_FEED:-false}" = true ] &&
            ! (cd "${here}" && go run ./cmd/updater-feed --check "${tmp}/latest.json" --require server-apps); then
            missing+=("the server apps in latest.json (desktop-release.yml)")
        fi
        if (cd "${here}" && go run ./cmd/updater-feed --check "${tmp}/latest.json" --require desktop) >/dev/null 2>&1; then
            all=true
        fi
    elif [ "${REQUIRE_FEED:-false}" = true ]; then
        missing+=("latest.json (desktop-release.yml)")
    fi
    if is_draft "${id}"; then
        if [ "${#missing[@]}" -gt 0 ]; then
            local list
            list="$(printf '%s, ' "${missing[@]}")"
            echo "::notice::${tag} stays a draft until it has ${list%, }; the workflow that adds the last of them publishes it"
            return 0
        fi
        if prerelease; then
            gh release edit "${tag}" --draft=false --prerelease --latest=false
        else
            gh release edit "${tag}" --draft=false --prerelease=false --latest=false
        fi
        echo "release ${tag}: published"
    fi
    if prerelease; then
        return 0
    fi
    local published latest
    published="$(gh release list --exclude-drafts --limit 1000 --json tagName --jq '.[].tagName')"
    latest="$(printf '%s\n' "${published}" | (cd "${here}" && go run ./cmd/release-order latest --tag "${tag}"))"
    if [ "${latest}" = true ] && { [ "${all}" = true ] || [ "${REQUIRE_FEED:-false}" != true ]; }; then
        gh release edit "${tag}" --latest
        echo "release ${tag}: the latest release"
    else
        echo "release ${tag}: not the latest (highest stable: ${latest}; every desktop platform in its feed: ${all})"
    fi
}

case "${cmd}" in
    ensure) ensure ;;
    upload) upload "$@" ;;
    publish) publish ;;
    *) echo "release-assets.sh: unknown command ${cmd}" >&2; exit 2 ;;
esac

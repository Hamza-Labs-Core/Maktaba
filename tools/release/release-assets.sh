#!/usr/bin/env bash
# A tag's GitHub release, as every tag workflow touches it (release.yml, desktop-release.yml,
# mobile-release.yml, tv-release.yml):
#
#   release-assets.sh ensure  TAG              the release exists, as a draft until it is complete
#                                              (NOTES set: the draft's notes)
#   release-assets.sh upload  TAG [--replace NAME]... FILE...
#   release-assets.sh publish TAG              publish the draft once complete; mark the latest
#   release-assets.sh adopt   TAG DIR          a published release: each file of DIR it has is
#                                              replaced by the published one, a bundle and its
#                                              .sig only as a pair (desktop's feed is then written
#                                              from the bundles and signatures apps download,
#                                              never from a re-run's own build of them)
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
# platform in its feed and, with the updater key, every player platform in latest-player.json
# (Epic 42.4: the players read the latest release's feed too).
#
# A published release is never rewritten (B5: a re-run of a tag replaced signed files homes
# had verified, and they withdrew it): upload adds only the files it lacks. release.yml's
# run refuses outright when its checksums.txt differs from the published one; --replace
# names the files that may change on a published release (desktop's latest.json and
# latest-player.json, as late platforms land).
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
    # the players' feed: not needed to publish (no home updates from it), but a release is the
    # latest only with every player platform in it, or the players would be stranded
    if [ "${all}" = true ] && [ "${REQUIRE_FEED:-false}" = true ]; then
        all=false
        if grep -qx latest-player.json <<<"${names}"; then
            gh release download "${tag}" --pattern latest-player.json --dir "${tmp}"
            if (cd "${here}" && go run ./cmd/updater-feed --check "${tmp}/latest-player.json" --require player) >/dev/null 2>&1; then
                all=true
            fi
        fi
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
        echo "release ${tag}: not the latest (highest stable: ${latest}; every desktop and player platform in its feeds: ${all})"
    fi
}

# adopt DIR: a published release's own copy of each file of DIR it has (a re-run of a
# published tag rebuilds the bundles, whose signatures differ: a feed written from them would
# name signatures the published bundles do not have, and every app's update would fail its
# check). A bundle and its signature (NAME and NAME.sig) go together: both published, both are
# taken; neither, this run's stay (a leg the release never had); one without the other (a leg
# cut between its two uploads) is refused, since a feed must never pair a published bundle with
# another build's signature. A draft's files are this run's (nothing public yet).
adopt() {
    local dir="${1:?DIR}" id names f base sig tmp
    id="$(release_id)"
    [ -n "${id}" ] || return 0
    if is_draft "${id}"; then
        return 0
    fi
    names="$(asset_names "${id}")"
    tmp="$(mktemp -d)"
    published() { grep -qxF "$1" <<<"${names}"; }
    # fetch NAME: the published copy, into tmp
    fetch() {
        gh release download "${tag}" --pattern "$1" --dir "${tmp}" --clobber
        [ -f "${tmp}/$1" ] || { echo "::error::${tag}: $1 is listed but could not be downloaded" >&2; exit 1; }
    }
    # take NAME: the fetched copy replaces this run's
    take() {
        if cmp -s "${tmp}/$1" "${dir}/$1"; then
            rm -f "${tmp}/$1"
        else
            echo "release ${tag} is published: its $1 is kept (this run built another)"
            mv -f "${tmp}/$1" "${dir}/$1"
        fi
    }
    for f in "${dir}"/*; do
        [ -f "${f}" ] || continue
        base="$(basename "${f}")"
        case "${base}" in latest.json | latest-player.json) continue ;; esac
        case "${base}" in
            *.sig)
                # a signature whose bundle is here goes with it (below); one alone, as before
                [ -f "${dir}/${base%.sig}" ] && continue
                published "${base}" || continue
                fetch "${base}"
                take "${base}"
                continue
                ;;
        esac
        sig="${base}.sig"
        if [ -f "${dir}/${sig}" ] || published "${sig}"; then
            if published "${base}" && published "${sig}"; then
                # both downloaded before either replaces this run's: never half a pair
                fetch "${base}"
                fetch "${sig}"
                take "${base}"
                take "${sig}"
            elif published "${base}" || published "${sig}"; then
                local has="${base}" lacks="${sig}"
                published "${base}" || { has="${sig}"; lacks="${base}"; }
                echo "::error::release ${tag} is published with ${has} but not ${lacks}: a bundle and its" \
                    "signature are adopted together or not at all (upload the missing one from the same build," \
                    "or delete the other, then run again)" >&2
                rm -rf "${tmp}"
                exit 1
            fi
            continue
        fi
        published "${base}" || continue
        fetch "${base}"
        take "${base}"
    done
    rm -rf "${tmp}"
}

case "${cmd}" in
    ensure) ensure ;;
    upload) upload "$@" ;;
    publish) publish ;;
    adopt) adopt "$@" ;;
    *) echo "release-assets.sh: unknown command ${cmd} (ensure|upload|publish|adopt)" >&2; exit 2 ;;
esac

#!/usr/bin/env bash
# The Homebrew tap's formula (the server) and cask (the Mac app), from their templates in
# deploy/packaging/homebrew/:
#
#   homebrew-tap.sh formula VERSION SHA_DARWIN_ARM64 SHA_DARWIN_AMD64   the formula, filled, on stdout
#   homebrew-tap.sh cask    VERSION SHA_APP_AARCH64                     the cask, filled, on stdout
#   homebrew-tap.sh push    TAP_DIR TAG FORMULA [CASK]                  into a clone of the tap:
#                                                                       Formula/maktaba.rb and
#                                                                       Casks/maktaba-app.rb, one
#                                                                       commit, pushed
#
# release.yml attaches the formula to every release (signed with the rest); packages.yml
# pushes it, with the cask, once the release is published as the newest stable one. Only the
# templates' own lines are filled (their header comments name the placeholders), and a file
# that still holds a placeholder is refused. push never takes the tap back: a tap whose
# formula is a newer release keeps it.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
templates="${here}/../../deploy/packaging/homebrew"

die() { echo "homebrew-tap.sh: $*" >&2; exit 1; }

version_ok() { [[ "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]]; }
sha_ok() { [[ "$1" =~ ^[0-9a-f]{64}$ ]]; }

# fill TEMPLATE PLACEHOLDER=VALUE...: the template with each placeholder replaced on every
# line but a comment
fill() {
    local template="$1" kv args=() out
    shift
    for kv in "$@"; do
        args+=(-e "/^[[:space:]]*#/!s/${kv%%=*}/${kv#*=}/g")
    done
    out="$(sed "${args[@]}" "${template}")"
    if grep -v '^[[:space:]]*#' <<<"${out}" | grep -q '__[A-Z0-9_]*__'; then
        die "${template##*/}: a placeholder is left: $(grep -v '^[[:space:]]*#' <<<"${out}" | grep -o '__[A-Z0-9_]*__' | sort -u | tr '\n' ' ')"
    fi
    printf '%s\n' "${out}"
}

formula() {
    [ "$#" = 3 ] || die "usage: formula VERSION SHA_DARWIN_ARM64 SHA_DARWIN_AMD64"
    version_ok "$1" || die "formula: not a version: $1"
    if ! sha_ok "$2" || ! sha_ok "$3"; then die "formula: not SHA-256 digests: $2 $3"; fi
    fill "${templates}/maktaba.rb" "__VERSION__=$1" "__SHA256_DARWIN_ARM64__=$2" "__SHA256_DARWIN_AMD64__=$3"
}

cask() {
    [ "$#" = 2 ] || die "usage: cask VERSION SHA_APP_AARCH64"
    version_ok "$1" || die "cask: not a version: $1"
    sha_ok "$2" || die "cask: not a SHA-256 digest: $2"
    fill "${templates}/maktaba-app.rb" "__VERSION__=$1" "__SHA256_APP_AARCH64__=$2"
}

# version_in FILE: the version a filled formula or cask names (nothing when none)
version_in() {
    [ -f "$1" ] || return 0
    sed -n 's/^[[:space:]]*version "\([^"]*\)".*/\1/p' "$1" | head -1
}

push() {
    [ "$#" -ge 3 ] || die "usage: push TAP_DIR TAG FORMULA [CASK]"
    local dir="$1" tag="$2" formula="$3" cask="${4:-}" ver have files=(Formula/maktaba.rb)
    ver="${tag#v}"
    version_ok "${ver}" || die "push: not a release tag: ${tag}"
    [ "$(version_in "${formula}")" = "${ver}" ] || die "push: ${formula} is not ${tag}'s formula"
    if [ -n "${cask}" ]; then
        [ "$(version_in "${cask}")" = "${ver}" ] || die "push: ${cask} is not ${tag}'s cask"
    fi
    have="$(version_in "${dir}/Formula/maktaba.rb")"
    if [ -n "${have}" ] && [ "${have}" != "${ver}" ] &&
        [ "$(printf '%s\n%s\n' "${have}" "${ver}" | sort -V | tail -1)" = "${have}" ]; then
        echo "the tap has a newer release (${have}): kept"
        return 0
    fi
    mkdir -p "${dir}/Formula"
    cp "${formula}" "${dir}/Formula/maktaba.rb"
    if [ -n "${cask}" ]; then
        mkdir -p "${dir}/Casks"
        cp "${cask}" "${dir}/Casks/maktaba-app.rb"
        files+=(Casks/maktaba-app.rb)
    fi
    git -C "${dir}" add "${files[@]}"
    if git -C "${dir}" diff --cached --quiet; then
        echo "the tap has ${tag} already"
        return 0
    fi
    git -C "${dir}" -c user.name="maktaba-release-bot" -c user.email="maktaba@hamzalabs.dev" \
        commit -q -m "maktaba ${tag}"
    git -C "${dir}" push -q origin HEAD
    echo "the tap now has ${tag}"
}

cmd="${1:-}"
[ -n "${cmd}" ] || die "usage: formula|cask|push ..."
shift
case "${cmd}" in
    formula) formula "$@" ;;
    cask) cask "$@" ;;
    push) push "$@" ;;
    *) die "unknown command ${cmd} (formula, cask, push)" ;;
esac

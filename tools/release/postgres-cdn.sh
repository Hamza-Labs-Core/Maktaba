#!/usr/bin/env bash
# Publishes the pinned Postgres archives to Maktaba's CDN (.github/workflows/postgres-cdn.yml):
# each line of LIST (tools/release/postgres-cdn.txt: "<path> <sha256> <size>", the pins of
# cmd/maktaba-server/internal/pgbin) goes to <bucket>/<path>, served at <CDN_URL>/<path>,
# where a machine downloading Postgres looks first.
#
#   postgres-cdn.sh LIST WORKDIR
#
# Idempotent: an archive the CDN already serves with the pinned SHA-256 and size is
# skipped. Every archive is checked against its pin after it is downloaded from GitHub and
# before it is uploaded: one that does not match is never uploaded.
#
# Environment:
#   CDN_URL     the CDN's public base (https://plugins-cdn.maktaba.tv)
#   BUCKET      the R2 bucket behind it (maktaba-plugins)
#   SOURCE_URL  where the archives are downloaded from: <SOURCE_URL>/<version>/<name>
#               (https://github.com/theseus-rs/postgresql-binaries/releases/download); when
#               it fails, `gh release download` (GitHub's API) is tried
#   GH_CLI      the gh command (gh)
#   UPLOAD      the command that uploads: called as `$UPLOAD <bucket>/<path> <file>`
#               (default: wrangler r2 object put, with CLOUDFLARE_API_TOKEN and
#               CLOUDFLARE_ACCOUNT_ID from the environment)
#
# Exit: 0 every archive is there; 2 an archive did not match its pin or could not be
# downloaded (nothing of it uploaded); 3 an upload failed.
set -euo pipefail

list="${1:?usage: postgres-cdn.sh LIST WORKDIR}"
work="${2:?usage: postgres-cdn.sh LIST WORKDIR}"
CDN_URL="${CDN_URL:-https://plugins-cdn.maktaba.tv}"
CDN_URL="${CDN_URL%/}"
BUCKET="${BUCKET:-maktaba-plugins}"
SOURCE_URL="${SOURCE_URL:-https://github.com/theseus-rs/postgresql-binaries/releases/download}"
SOURCE_URL="${SOURCE_URL%/}"
SOURCE_REPO="theseus-rs/postgresql-binaries"
WRANGLER_VERSION="4.124.0" # store/package.json's

mkdir -p "${work}"

sha256() {
    if command -v sha256sum >/dev/null; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi
}
size() { wc -c <"$1" | tr -d ' '; }

# matches FILE SHA SIZE: the file is the pinned archive
matches() { [ -f "$1" ] && [ "$(size "$1")" = "$3" ] && [ "$(sha256 "$1")" = "$2" ]; }

wrangler_put() {
    npx --yes "wrangler@${WRANGLER_VERSION}" r2 object put "$1" --file "$2" --remote \
        --content-type application/octet-stream --cache-control "public, max-age=31536000, immutable"
}
upload="${UPLOAD:-wrangler_put}"

fetch() { # fetch VERSION NAME DEST: the archive from GitHub, retried; GitHub's API after the door
    local url="${SOURCE_URL}/$1/$2"
    if curl -fsSL --retry 4 --retry-all-errors --retry-delay 5 --connect-timeout 20 -o "$3" "${url}"; then
        return 0
    fi
    echo "  ${url} failed: GitHub's API (gh release download)"
    rm -f "$3"
    command -v "${GH_CLI:-gh}" >/dev/null || return 1
    "${GH_CLI:-gh}" release download "$1" --repo "${SOURCE_REPO}" --pattern "$2" --output "$3" --clobber
}

uploaded=0 skipped=0
while read -r path sha want_size <&3; do
    case "${path}" in "" | \#*) continue ;; esac
    if ! [[ "${sha}" =~ ^[0-9a-f]{64}$ ]] || ! [[ "${want_size}" =~ ^[0-9]+$ ]] ||
        ! [[ "${path}" =~ ^postgres/[0-9.]+/[A-Za-z0-9._-]+$ ]]; then
        echo "::error::${list}: not a \"<path> <sha256> <size>\" line: ${path} ${sha} ${want_size}"
        exit 2
    fi
    name="${path##*/}"
    version="${path#postgres/}"
    version="${version%%/*}"
    file="${work}/${name}"

    # what the CDN serves already, when it is the pinned archive, stays
    if curl -fsS --connect-timeout 20 -o "${file}.cdn" "${CDN_URL}/${path}" 2>/dev/null &&
        matches "${file}.cdn" "${sha}" "${want_size}"; then
        echo "${path}: on the CDN already with its pinned SHA-256: skipped"
        rm -f "${file}.cdn"
        skipped=$((skipped + 1))
        continue
    fi
    if [ -s "${file}.cdn" ]; then
        echo "::warning::${CDN_URL}/${path} is not the pinned archive: replaced"
    fi
    rm -f "${file}.cdn"

    if ! matches "${file}" "${sha}" "${want_size}"; then
        rm -f "${file}"
        if ! fetch "${version}" "${name}" "${file}"; then
            echo "::error::${name}: could not be downloaded from GitHub"
            exit 2
        fi
    fi
    if ! matches "${file}" "${sha}" "${want_size}"; then
        echo "::error::${name}: SHA-256 $(sha256 "${file}") ($(size "${file}") bytes), pinned ${sha} (${want_size} bytes): not uploaded"
        rm -f "${file}"
        exit 2
    fi
    if ! "${upload}" "${BUCKET}/${path}" "${file}"; then
        echo "::error::${path}: the upload to ${BUCKET} failed"
        exit 3
    fi
    echo "${path}: uploaded (${want_size} bytes, SHA-256 ${sha})"
    uploaded=$((uploaded + 1))
done 3<"${list}"

echo "Postgres archives on ${CDN_URL}: ${uploaded} uploaded, ${skipped} there already"

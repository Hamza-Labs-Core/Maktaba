#!/usr/bin/env bash
# Maktaba home-server installer. Take it from a release (its install.sh asset,
# listed in the release's signed checksums.txt), not from a branch:
#
#   curl -fsSLO https://github.com/Hamza-Labs-Core/Maktaba/releases/latest/download/install.sh
#   bash install.sh          # the server binaries
#   bash install.sh --app    # Linux: the whole server, for a machine without a desktop
#
# Detects your OS + CPU, downloads the matching signed release archive
# from GitHub, verifies the release's Sigstore signature (when cosign is
# installed) and its SHA-256 against the release checksums, and
# installs the maktaba-server binary (plus the api/streaming siblings it
# supervises) into a bin directory on your PATH. On Linux it can also
# drop in and enable the systemd unit.
#
# With --app (Linux, Epic 39): the whole server instead, for a machine without a
# desktop: the release's maktaba-linux-payload-<ver>-<arch>.tar.gz (the server,
# Postgres and Python the Linux app carries; ffmpeg is the distribution's), unpacked
# in ~/.cache/maktaba/install, then its `maktaba-server setup` asks the app's setup
# questions in this terminal (and, when the distribution's ffmpeg is missing or
# falls short, offers to run the command that installs it), copies the server into
# your data directory and runs it as your systemd user service. Arguments after --app go to setup
# (--answers FILE). --app never runs a payload whose signature it has not
# checked: without cosign on the PATH it fetches cosign's own release build,
# pinned by its SHA-256 below, and MAKTABA_NO_VERIFY does not apply. It installs
# for the user who runs it, never as root. Take this script from the release
# (its install.sh asset, listed in checksums.txt), not from a branch.
#
# Tunables (env vars):
#   MAKTABA_VERSION   release tag to install            (default: latest)
#   MAKTABA_PREFIX    install dir for binaries          (default: /usr/local/bin)
#   MAKTABA_SYSTEMD   1=install+enable unit, 0=skip     (default: prompt; 0 when non-interactive)
#   MAKTABA_NO_VERIFY 1=skip checksum + signature verification (default: 0;
#                     refused with --app)
#   MAKTABA_REQUIRE_COSIGN 1=refuse to install without cosign  (default: 0;
#                     without cosign only the SHA-256 is checked; --app always
#                     checks the signature, with a fetched cosign if need be)
#
# Verification fails closed: a release missing checksums.txt, its Sigstore
# bundle or the archive's checksum entry is refused - release assets can be
# replaced or deleted after publishing, the signature cannot be forged.
set -euo pipefail

REPO="Hamza-Labs-Core/Maktaba"
PREFIX="${MAKTABA_PREFIX:-/usr/local/bin}"
VERSION="${MAKTABA_VERSION:-latest}"

# The verifier --app fetches when the machine has none: sigstore/cosign's
# release build (the 2.6 line the release workflow signs with), checked against
# these SHA-256s (cosign_checksums.txt of that release) before it runs.
COSIGN_VERSION="v2.6.5"
COSIGN_SHA256_AMD64="c3b4f5410e608af03a5eb0aaac84a4313d8da131248e08ff1759ac70c79d1644"
COSIGN_SHA256_ARM64="426193b4c5da4d4d643e822f48fe0cc8a476ca1782a272704831f5a0cef716d7"
# The oldest cosign whose verify-blob reads the release's (new format) Sigstore
# bundle: an older one on the PATH is passed over for the pinned build.
COSIGN_MIN="2.4.3"
# The cosign verify_signature runs (ensure_cosign may point it at the fetched one).
COSIGN="cosign"
# What a refusal adds; --app has no way around the verification.
NO_VERIFY_HINT=" (MAKTABA_NO_VERIFY=1 overrides)"

# --- pretty output -----------------------------------------------------
info()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn()  { printf '\033[1;33mwarn:\033[0m %s\n' "$*" >&2; }
die()   { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "required tool '$1' not found"; }

# --- detect platform ---------------------------------------------------
detect_os() {
    local os
    os="$(uname -s)"
    case "${os}" in
        Linux)  echo "linux" ;;
        Darwin) echo "darwin" ;;
        *)      die "unsupported OS '${os}' — use Docker or build from source" ;;
    esac
}

detect_arch() {
    local arch
    arch="$(uname -m)"
    case "${arch}" in
        x86_64|amd64)   echo "amd64" ;;
        arm64|aarch64)  echo "arm64" ;;
        *)              die "unsupported CPU '${arch}' — use Docker or build from source" ;;
    esac
}

# --- download helper (curl or wget) -----------------------------------
fetch() {
    # fetch <url> <dest>
    local url="$1" dest="$2"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "${url}" -o "${dest}"
    elif command -v wget >/dev/null 2>&1; then
        wget -qO "${dest}" "${url}"
    else
        die "need curl or wget to download releases"
    fi
}

fetch_stdout() {
    local url="$1"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "${url}"
    else
        wget -qO- "${url}"
    fi
}

# Resolve "latest" to a concrete tag via the GitHub API (no jq needed).
resolve_version() {
    if [ "${VERSION}" != "latest" ]; then
        echo "${VERSION}"
        return
    fi
    local tag
    tag="$(fetch_stdout "https://api.github.com/repos/${REPO}/releases/latest" \
        | grep -m1 '"tag_name"' \
        | sed -E 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/')"
    [ -n "${tag}" ] || die "could not resolve the latest release tag"
    echo "${tag}"
}

# A release tag is vMAJOR.MINOR.PATCH[-prerelease]; anything else would end
# up in URLs and in the signing identity.
valid_tag() {
    printf '%s' "$1" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'
}

# payload_archive <version> <amd64|arm64>: the Linux payload asset's name.
payload_archive() {
    local machine
    case "$2" in
        amd64) machine=x86_64 ;;
        arm64) machine=aarch64 ;;
        *)     return 1 ;;
    esac
    printf 'maktaba-linux-payload-%s-%s.tar.gz' "$1" "${machine}"
}

# sha256_of <file>: its SHA-256 (sha256sum or shasum), or nothing.
sha256_of() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

# version_at_least <have> <need>: dotted versions (a leading v ignored), have >= need.
version_at_least() {
    local -a have need
    local i h n
    IFS=. read -r -a have <<<"${1#v}"
    IFS=. read -r -a need <<<"${2#v}"
    for i in 0 1 2; do
        h="${have[i]:-0}" n="${need[i]:-0}"
        h="${h%%[!0-9]*}" n="${n%%[!0-9]*}"
        [ "${h:-0}" -gt "${n:-0}" ] && return 0
        [ "${h:-0}" -lt "${n:-0}" ] && return 1
    done
    return 0
}

# cosign_version <cosign>: its version (cosign version's GitVersion), or nothing
# (a cosign whose `version` fails is not one this script can use, never an error).
cosign_version() {
    { "$1" version 2>/dev/null || true; } | awk '$1 == "GitVersion:" { print $2; exit }'
}

# ensure_cosign <dir> <amd64|arm64>: COSIGN is a cosign that can check the
# release here: the one on the PATH when it is COSIGN_MIN or newer, else
# cosign's pinned release build fetched into <dir> and checked against its
# SHA-256 before it is made executable.
ensure_cosign() {
    local dir="$1" arch="$2" want bin have
    if command -v cosign >/dev/null 2>&1; then
        have="$(cosign_version cosign)"
        if [ -n "${have}" ] && version_at_least "${have}" "${COSIGN_MIN}"; then
            COSIGN="cosign"
            return 0
        fi
        info "The cosign on the PATH (${have:-of unknown version}) is older than ${COSIGN_MIN}, which this release's signature needs"
    fi
    case "${arch}" in
        amd64) want="${COSIGN_SHA256_AMD64}" ;;
        arm64) want="${COSIGN_SHA256_ARM64}" ;;
        *)     die "no pinned cosign for ${arch}" ;;
    esac
    mkdir -p "${dir}/cosign"
    bin="${dir}/cosign/cosign"
    info "Fetching cosign ${COSIGN_VERSION} to check the release's signature"
    fetch "https://github.com/sigstore/cosign/releases/download/${COSIGN_VERSION}/cosign-linux-${arch}" "${bin}" \
        || die "could not download cosign ${COSIGN_VERSION} (install cosign, then run this again)"
    [ "$(sha256_of "${bin}")" = "${want}" ] \
        || { rm -f "${bin}"; die "the downloaded cosign ${COSIGN_VERSION} is not the release build (SHA-256 mismatch)"; }
    chmod 0755 "${bin}"
    COSIGN="${bin}"
}

# main_app [setup args]: the Linux app's server for a machine without a desktop.
main_app() {
    local arch tag ver archive base dir
    [ "$(detect_os)" = linux ] || die "--app is for Linux (the Mac and Windows apps carry the server)"
    [ "$(id -u)" -ne 0 ] \
        || die "--app installs Maktaba for one user, as a user service: run it as that user, without sudo (an administrator's password is asked only to start it at boot)"
    [ "${MAKTABA_NO_VERIFY:-0}" != "1" ] \
        || die "--app never runs a payload it has not verified: unset MAKTABA_NO_VERIFY"
    NO_VERIFY_HINT=""
    arch="$(detect_arch)"
    tag="$(resolve_version)"
    valid_tag "${tag}" || die "invalid release tag '${tag}' (want vX.Y.Z)"
    ver="${tag#v}"
    archive="$(payload_archive "${ver}" "${arch}")"
    base="https://github.com/${REPO}/releases/download/${tag}"
    # not /tmp: often a small tmpfs, and the payload is a few hundred MB unpacked
    dir="${XDG_CACHE_HOME:-${HOME}/.cache}/maktaba/install/${ver}"
    rm -rf "${dir}"
    mkdir -p "${dir}"
    # the download and the unpacked payload go, whatever happens (setup copied what it needs)
    # shellcheck disable=SC2064 # the path, now
    trap "rm -rf $(printf '%q' "${dir}")" EXIT
    info "Installing Maktaba ${tag} (the whole server) for linux/${arch}"
    ensure_cosign "${dir}" "${arch}"
    info "Downloading ${archive}"
    fetch "${base}/${archive}" "${dir}/${archive}" || die "download failed: ${base}/${archive}"
    MAKTABA_REQUIRE_COSIGN=1 verify_release "${dir}" "${base}" "${tag}" "${archive}"
    info "Unpacking"
    tar -C "${dir}" -xzf "${dir}/${archive}"
    rm -f "${dir}/${archive}"
    [ -x "${dir}/server/bin/maktaba-server" ] || die "${archive} has no server/bin/maktaba-server"
    # the questions need a terminal: curl | bash gives the script on stdin
    if [ -r /dev/tty ] && [ -t 1 ]; then
        "${dir}/server/bin/maktaba-server" setup "$@" </dev/tty
    else
        case " $* " in
            *" --answers"*) "${dir}/server/bin/maktaba-server" setup "$@" ;;
            *) die "setup asks questions: run this in a terminal, or pass --answers FILE" ;;
        esac
    fi
    rm -rf "${dir}"
}

main() {
    local os arch tag base archive url tmp
    if [ "${1:-}" = "--app" ]; then
        shift
        main_app "$@"
        return
    fi
    os="$(detect_os)"
    arch="$(detect_arch)"
    tag="$(resolve_version)"
    valid_tag "${tag}" || die "invalid release tag '${tag}' (want vX.Y.Z)"
    local ver="${tag#v}"

    info "Installing maktaba-server ${tag} for ${os}/${arch}"

    archive="maktaba-server-${ver}-${os}-${arch}.tar.gz"
    base="https://github.com/${REPO}/releases/download/${tag}"
    url="${base}/${archive}"

    tmp="$(mktemp -d)"
    trap 'rm -rf "${tmp}"' EXIT

    info "Downloading ${archive}"
    fetch "${url}" "${tmp}/${archive}" \
        || die "download failed: ${url}"

    # Verify against the release checksums.txt unless disabled: first the
    # Sigstore signature over checksums.txt (pinned to this repository's
    # release workflow for exactly this tag), then the archive's SHA-256.
    if [ "${MAKTABA_NO_VERIFY:-0}" != "1" ]; then
        verify_release "${tmp}" "${base}" "${tag}" "${archive}"
    else
        warn "MAKTABA_NO_VERIFY=1 - installing ${archive} without any verification"
    fi

    info "Extracting"
    tar -C "${tmp}" -xzf "${tmp}/${archive}"

    install_binaries "${tmp}" "${os}"

    if [ "${os}" = "linux" ]; then
        maybe_install_systemd "${tmp}"
    fi

    info "Done. Run 'maktaba-server setup' to configure your library."
}

# The exact signing identity of a release (Epic 35 story 35.1): this
# repository's release workflow, run for the tag being installed. A regexp
# over the repository name would also accept a fork's workflow.
identity_for() {
    printf 'https://github.com/%s/.github/workflows/release.yml@refs/tags/%s' "${REPO}" "$1"
}

# verify_release <dir> <base-url> <tag> <archive>: the Sigstore signature over
# checksums.txt, then the archive's SHA-256 against it. Every missing piece is
# fatal: whoever can replace a release's assets can also delete some, and a
# check skipped when its input is missing protects nothing.
verify_release() {
    local dir="$1" base="$2" tag="$3" archive="$4"
    fetch "${base}/checksums.txt" "${dir}/checksums.txt" 2>/dev/null \
        || die "release ${tag} has no checksums.txt - refusing to install it unverified${NO_VERIFY_HINT}"
    verify_signature "${dir}" "${base}" "${tag}"
    info "Verifying SHA-256 checksum"
    verify_checksum "${dir}" "${archive}" \
        || die "checksum verification failed for ${archive}"
}

verify_signature() {
    local dir="$1" base="$2" tag="$3" id
    id="$(identity_for "${tag}")"
    if ! fetch "${base}/checksums.txt.sigstore.json" "${dir}/checksums.txt.sigstore.json" 2>/dev/null; then
        die "release ${tag} has no Sigstore bundle (checksums.txt.sigstore.json) - refusing to install it${NO_VERIFY_HINT}"
    fi
    if command -v "${COSIGN}" >/dev/null 2>&1; then
        info "Verifying the Sigstore signature of checksums.txt"
        "${COSIGN}" verify-blob --bundle "${dir}/checksums.txt.sigstore.json" \
            --certificate-identity "${id}" \
            --certificate-oidc-issuer https://token.actions.githubusercontent.com \
            "${dir}/checksums.txt" >/dev/null 2>&1 \
            || die "signature verification failed: checksums.txt is not signed by ${id}"
    else
        [ "${MAKTABA_REQUIRE_COSIGN:-0}" != "1" ] \
            || die "cosign not found and MAKTABA_REQUIRE_COSIGN=1 - install cosign first"
        warn "cosign not found - checksums.txt signature not checked. To check it yourself:"
        warn "  cosign verify-blob --bundle checksums.txt.sigstore.json --certificate-identity '${id}' --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt"
    fi
}

verify_checksum() {
    local dir="$1" file="$2" want have
    # checksums.txt lines look like "<sha256>  ./<file>" or "<sha256>  <file>"
    # (sha256sum's binary marker "*" allowed). The name is compared exactly,
    # not as a regexp, and a name listed twice is refused (never "first wins").
    want="$(tr -d '\r' < "${dir}/checksums.txt" | awk -v f="${file}" \
        '{ n = $2; sub(/^\*/, "", n); sub(/^\.\//, "", n); if (n == f) print $1 }')"
    case "${want}" in
        "") warn "no checksum entry for ${file}"; return 1 ;;
        *[!0-9a-f]*) warn "bad or duplicate checksum entry for ${file}"; return 1 ;;
    esac
    [ "${#want}" -eq 64 ] || { warn "bad checksum entry for ${file}"; return 1; }
    if command -v sha256sum >/dev/null 2>&1; then
        have="$(sha256sum "${dir}/${file}" | awk '{print $1}')"
    elif command -v shasum >/dev/null 2>&1; then
        have="$(shasum -a 256 "${dir}/${file}" | awk '{print $1}')"
    else
        warn "no sha256 tool found (sha256sum or shasum)"
        return 1
    fi
    [ "${want}" = "${have}" ]
}

# Install every maktaba-* binary found in the extracted archive into the
# prefix, using sudo only when the prefix isn't writable.
install_binaries() {
    local dir="$1" os="$2" sudo="" b name
    if [ ! -w "${PREFIX}" ]; then
        if [ "$(id -u)" -ne 0 ]; then
            command -v sudo >/dev/null 2>&1 \
                || die "${PREFIX} is not writable and sudo is unavailable; set MAKTABA_PREFIX"
            sudo="sudo"
        fi
    fi
    ${sudo} mkdir -p "${PREFIX}"
    for b in "${dir}"/maktaba-server "${dir}"/maktaba-api "${dir}"/maktaba-streaming \
             "${dir}"/maktaba-server.exe; do
        [ -f "${b}" ] || continue
        name="$(basename "${b}")"
        info "Installing ${name} -> ${PREFIX}/${name}"
        ${sudo} install -m 0755 "${b}" "${PREFIX}/${name}"
    done
    [ -x "${PREFIX}/maktaba-server" ] || [ -x "${PREFIX}/maktaba-server.exe" ] \
        || die "maktaba-server binary not found in archive"
}

# Offer to install the systemd unit + default config on Linux.
maybe_install_systemd() {
    local dir="$1" choice="${MAKTABA_SYSTEMD:-}"
    [ -d /run/systemd/system ] || { warn "systemd not detected — skipping unit install"; return 0; }

    if [ -z "${choice}" ]; then
        if [ -t 0 ]; then
            printf 'Install and enable the systemd service now? [y/N] '
            read -r ans </dev/tty || ans=""
            case "${ans}" in y|Y|yes|YES) choice=1 ;; *) choice=0 ;; esac
        else
            choice=0  # non-interactive (curl | bash): don't touch the system silently
        fi
    fi
    [ "${choice}" = "1" ] || { info "Skipping systemd setup (run with MAKTABA_SYSTEMD=1 to enable)."; return 0; }

    need sudo
    info "Creating maktaba system user + state dirs"
    if ! getent group maktaba >/dev/null 2>&1; then sudo groupadd --system maktaba; fi
    if ! getent passwd maktaba >/dev/null 2>&1; then
        sudo useradd --system --gid maktaba --home-dir /var/lib/maktaba \
            --no-create-home --shell /usr/sbin/nologin --comment "Maktaba home server" maktaba
    fi
    sudo mkdir -p /var/lib/maktaba/media /var/log/maktaba /etc/maktaba
    sudo chown -R maktaba:maktaba /var/lib/maktaba /var/log/maktaba

    # Config + unit ship inside the archive next to the binaries.
    if [ -f "${dir}/server.toml.example" ] && [ ! -f /etc/maktaba/server.toml ]; then
        sudo install -m 0644 "${dir}/server.toml.example" /etc/maktaba/server.toml
    fi
    if [ -f "${dir}/maktaba-server.service" ]; then
        sudo install -m 0644 "${dir}/maktaba-server.service" \
            /usr/lib/systemd/system/maktaba-server.service
        sudo systemctl daemon-reload
        sudo systemctl enable maktaba-server.service
        info "Service enabled. Edit /etc/maktaba/server.toml, then: sudo systemctl start maktaba-server"
    fi
}

# MAKTABA_INSTALL_LIB=1 only defines the functions (deploy/packaging/tests).
if [ "${MAKTABA_INSTALL_LIB:-0}" != "1" ]; then
    main "$@"
fi

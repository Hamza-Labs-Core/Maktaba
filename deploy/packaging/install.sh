#!/usr/bin/env bash
# Maktaba home-server installer.
#
#   curl -fsSL https://raw.githubusercontent.com/Hamza-Labs-Core/Maktaba/main/deploy/packaging/install.sh | bash
#
# Detects your OS + CPU, downloads the matching signed release archive
# from GitHub, verifies its SHA-256 against the release checksums, and
# installs the maktaba-server binary (plus the api/streaming siblings it
# supervises) into a bin directory on your PATH. On Linux it can also
# drop in and enable the systemd unit.
#
# Tunables (env vars):
#   MAKTABA_VERSION   release tag to install            (default: latest)
#   MAKTABA_PREFIX    install dir for binaries          (default: /usr/local/bin)
#   MAKTABA_SYSTEMD   1=install+enable unit, 0=skip     (default: prompt; 0 when non-interactive)
#   MAKTABA_NO_VERIFY 1=skip checksum verification      (default: 0)
set -euo pipefail

REPO="Hamza-Labs-Core/Maktaba"
PREFIX="${MAKTABA_PREFIX:-/usr/local/bin}"
VERSION="${MAKTABA_VERSION:-latest}"

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

main() {
    local os arch tag base archive url tmp
    os="$(detect_os)"
    arch="$(detect_arch)"
    tag="$(resolve_version)"
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

    # Verify against the release checksums.txt unless disabled.
    if [ "${MAKTABA_NO_VERIFY:-0}" != "1" ]; then
        if fetch "${base}/checksums.txt" "${tmp}/checksums.txt" 2>/dev/null; then
            info "Verifying SHA-256 checksum"
            verify_checksum "${tmp}" "${archive}" \
                || die "checksum verification failed for ${archive}"
        else
            warn "checksums.txt not found in release — skipping verification"
        fi
    fi

    info "Extracting"
    tar -C "${tmp}" -xzf "${tmp}/${archive}"

    install_binaries "${tmp}" "${os}"

    if [ "${os}" = "linux" ]; then
        maybe_install_systemd "${tmp}"
    fi

    info "Done. Run 'maktaba-server setup' to configure your library."
}

verify_checksum() {
    local dir="$1" file="$2" want have
    # checksums.txt lines look like "<sha256>  ./<file>" or "<sha256>  <file>".
    want="$(grep -E "[[:space:]]\.?/?${file}\$" "${dir}/checksums.txt" \
        | head -n1 | awk '{print $1}')"
    [ -n "${want}" ] || { warn "no checksum entry for ${file}"; return 0; }
    if command -v sha256sum >/dev/null 2>&1; then
        have="$(sha256sum "${dir}/${file}" | awk '{print $1}')"
    elif command -v shasum >/dev/null 2>&1; then
        have="$(shasum -a 256 "${dir}/${file}" | awk '{print $1}')"
    else
        warn "no sha256 tool found — skipping verification"
        return 0
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

main "$@"

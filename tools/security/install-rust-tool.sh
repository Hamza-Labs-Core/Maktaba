#!/usr/bin/env bash
# Pinned Rust security tools for CI and release jobs (_cve-gate.yml, release.yml's sbom job):
# cargo-audit and cargo-cyclonedx, fetched by tools/release/fetch-pinned.sh (retries with
# backoff, GitHub API fallback, SHA-256 verified) instead of compiling from crates.io.
#
#   install-rust-tool.sh --key TOOL       the actions/cache key for TOOL's pinned archive
#   install-rust-tool.sh TOOL DIR         installs TOOL into DIR/bin and appends DIR/bin to
#                                         $GITHUB_PATH when running in GitHub Actions
#
# TOOL is `cargo-audit` or `cargo-cyclonedx`.
set -euo pipefail

CARGO_AUDIT_VERSION=0.23.0
CARGO_CYCLONEDX_VERSION=0.5.10

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fetch="$here/../release/fetch-pinned.sh"

key_only=0
if [ "${1:-}" = --key ]; then
    key_only=1
    shift
fi

tool="${1:-}"
case "$tool" in
    cargo-audit|cargo-cyclonedx) ;;
    *) echo "usage: install-rust-tool.sh [--key] <cargo-audit|cargo-cyclonedx> [DIR]" >&2; exit 2 ;;
esac

host="$(uname -s)/$(uname -m)"
url="" sha256="" archive="" ver=""
case "$tool:$host" in
    cargo-audit:Linux/x86_64)
        ver="$CARGO_AUDIT_VERSION"
        archive="cargo-audit-x86_64-unknown-linux-musl-v${ver}.tgz"
        url="https://github.com/rustsec/rustsec/releases/download/cargo-audit/v${ver}/${archive}"
        sha256="4f01c74ed84b2dffd0b1d37012a62cdf2a92c96e25bdf8eec7fcc0d4d3eb7811"
        ;;
    cargo-audit:Linux/aarch64)
        ver="$CARGO_AUDIT_VERSION"
        archive="cargo-audit-aarch64-unknown-linux-gnu-v${ver}.tgz"
        url="https://github.com/rustsec/rustsec/releases/download/cargo-audit/v${ver}/${archive}"
        sha256="00eff3158429b9629abe8748e971f29f99ee53d4cff54a25072cbb15167bdda8"
        ;;
    cargo-audit:Darwin/arm64)
        ver="$CARGO_AUDIT_VERSION"
        archive="cargo-audit-aarch64-apple-darwin-v${ver}.tgz"
        url="https://github.com/rustsec/rustsec/releases/download/cargo-audit/v${ver}/${archive}"
        sha256="84101204e5b8013c9a8d7251766aebdef3e7fa1a3ef040cde44d65b48b8c4056"
        ;;
    cargo-audit:Darwin/x86_64)
        ver="$CARGO_AUDIT_VERSION"
        archive="cargo-audit-x86_64-apple-darwin-v${ver}.tgz"
        url="https://github.com/rustsec/rustsec/releases/download/cargo-audit/v${ver}/${archive}"
        sha256="845ec02452dd233c292be887700ac99a38a697299423081ee0d8ea1a0c961464"
        ;;
    cargo-cyclonedx:Linux/x86_64)
        ver="$CARGO_CYCLONEDX_VERSION"
        archive="cargo-cyclonedx-x86_64-unknown-linux-musl.tar.xz"
        url="https://github.com/CycloneDX/cyclonedx-rust-cargo/releases/download/cargo-cyclonedx-${ver}/${archive}"
        sha256="528be9dd99ae522c3ee14fb881fd0cbf51767e8f12b74248f437e6a66b1a3c1b"
        ;;
    cargo-cyclonedx:Linux/aarch64)
        ver="$CARGO_CYCLONEDX_VERSION"
        archive="cargo-cyclonedx-aarch64-unknown-linux-gnu.tar.xz"
        url="https://github.com/CycloneDX/cyclonedx-rust-cargo/releases/download/cargo-cyclonedx-${ver}/${archive}"
        sha256="6931b234914fa9be8686f46bd1ce71358b4be6d4cd3ce6b7082bec2313e59a8e"
        ;;
    cargo-cyclonedx:Darwin/arm64)
        ver="$CARGO_CYCLONEDX_VERSION"
        archive="cargo-cyclonedx-aarch64-apple-darwin.tar.xz"
        url="https://github.com/CycloneDX/cyclonedx-rust-cargo/releases/download/cargo-cyclonedx-${ver}/${archive}"
        sha256="4c39e13494d51c9d7f63e2923e7afdb5648a7a806a9ed7684dbe3786d2e2d1da"
        ;;
    cargo-cyclonedx:Darwin/x86_64)
        ver="$CARGO_CYCLONEDX_VERSION"
        archive="cargo-cyclonedx-x86_64-apple-darwin.tar.xz"
        url="https://github.com/CycloneDX/cyclonedx-rust-cargo/releases/download/cargo-cyclonedx-${ver}/${archive}"
        sha256="5f14c5123796e278a7a655686162ef7158421836658540270bddfc481e51641c"
        ;;
    *)
        echo "::error::install-rust-tool.sh: unsupported platform ${host} for ${tool}" >&2
        exit 1
        ;;
esac

if [ "$key_only" -eq 1 ]; then
    "$fetch" --key "$url" "$sha256"
    exit 0
fi

[ $# -eq 2 ] || { echo "usage: install-rust-tool.sh <cargo-audit|cargo-cyclonedx> DIR" >&2; exit 2; }
dir="$2"
mkdir -p "$dir/bin"

downloaded="$("$fetch" "$url" "$sha256" "$dir/$archive")"
work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

case "$archive" in
    *.tgz|*.tar.gz) tar -xzf "$downloaded" -C "$work" ;;
    *.tar.xz)       tar -xJf "$downloaded" -C "$work" ;;
    *) echo "::error::install-rust-tool.sh: unsupported archive ${archive}" >&2; exit 1 ;;
esac

bin="$(find "$work" -type f -name "$tool" | awk 'NR == 1 { print }')"
[ -n "$bin" ] || { echo "::error::install-rust-tool.sh: ${tool} not found inside ${archive}" >&2; exit 1; }
install -m 0755 "$bin" "$dir/bin/$tool"

if [ "$tool" = cargo-audit ]; then
    got="$("$dir/bin/$tool" audit --version 2>&1)"
else
    got="$("$dir/bin/$tool" cyclonedx --version 2>&1)"
fi
case "$got" in
    *"$ver"*) printf '%s: %s\n' "$tool" "$got" ;;
    *) echo "::error::$dir/bin/$tool is not $ver: $got" >&2; exit 1 ;;
esac

if [ -n "${GITHUB_PATH:-}" ]; then
    (cd "$dir/bin" && pwd) >>"$GITHUB_PATH"
fi

#!/usr/bin/env bash
# Build the Maktaba desktop (Tauri 2) app.
#
# `tauri build` runs the configured beforeBuildCommand
# (pnpm --filter maktaba-web build) to produce web/dist, compiles the
# Rust shell, and emits signed installers for the HOST platform. Tauri
# does not cross-compile the native shell, so each OS is built on its own
# runner — this script just selects the right bundle targets per host.
#
# Usage:
#   ./scripts/build.sh            # build for the current host OS
#   ./scripts/build.sh macos      # .app + .dmg          (run on macOS)
#   ./scripts/build.sh windows    # .msi + .exe (NSIS)   (run on Windows)
#   ./scripts/build.sh linux      # .deb + .AppImage     (run on Linux)
#   ./scripts/build.sh debug      # fast unsigned debug build for the host
#
# Requires the Rust toolchain (https://rustup.rs) and the platform build
# deps documented in apps/desktop/README.md.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DESKTOP_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

log() { printf '\033[1;35m[desktop-build]\033[0m %s\n' "$*"; }

case "$(uname -s)" in
  Darwin) HOST=macos ;;
  Linux)  HOST=linux ;;
  MINGW*|MSYS*|CYGWIN*) HOST=windows ;;
  *)      HOST=unknown ;;
esac

TARGET="${1:-$HOST}"

cd "${DESKTOP_DIR}"

if [ ! -d node_modules ]; then
  log "Installing desktop JS deps (Tauri CLI + API bindings)"
  pnpm install
fi

require_host() {
  if [ "${HOST}" != "$1" ]; then
    echo "ERROR: '$1' bundles can only be built on a $1 host (this host is ${HOST})." >&2
    echo "       Tauri does not cross-compile the native shell." >&2
    exit 3
  fi
}

case "${TARGET}" in
  macos)
    require_host macos
    log "Building macOS .app + .dmg (universal where toolchain allows)"
    pnpm exec tauri build --bundles app,dmg
    ;;
  windows)
    require_host windows
    log "Building Windows .msi + NSIS .exe"
    pnpm exec tauri build --bundles msi,nsis
    ;;
  linux)
    require_host linux
    log "Building Linux .deb + .AppImage"
    pnpm exec tauri build --bundles deb,appimage
    ;;
  debug)
    log "Building unsigned debug bundle for ${HOST}"
    pnpm exec tauri build --debug
    ;;
  *)
    echo "Unknown target '${TARGET}' (want: macos | windows | linux | debug)" >&2
    exit 2
    ;;
esac

log "Done. Installers are under apps/desktop/src-tauri/target/release/bundle/."

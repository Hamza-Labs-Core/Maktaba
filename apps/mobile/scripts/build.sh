#!/usr/bin/env bash
# Build the Maktaba mobile (Capacitor) apps.
#
# Pipeline:
#   1. Build the shared web SPA → web/dist   (the JS bundle both shells serve)
#   2. cap sync                              → copy web/dist + plugins into
#                                              the native iOS/Android projects
#
# The native projects (ios/, android/) are generated on demand by
# `npx cap add` and are gitignored; if they do not exist yet this script
# adds them first (best-effort — requires Xcode+CocoaPods for iOS and the
# Android SDK + JDK 17 for Android).
#
# Usage:
#   ./scripts/build.sh            # build web + sync both platforms
#   ./scripts/build.sh ios        # build web + sync iOS only
#   ./scripts/build.sh android    # build web + sync Android only
#
# Env:
#   CAP_SERVER_URL   if set, capacitor.config.ts points the WebView at a
#                    live Vite dev server instead of the bundled webDir.
set -euo pipefail

# Resolve repo root from this script's location so it works from anywhere.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MOBILE_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
REPO_ROOT="$(cd "${MOBILE_DIR}/../.." && pwd)"

PLATFORM="${1:-all}"

log() { printf '\033[1;34m[mobile-build]\033[0m %s\n' "$*"; }

# 1. Build the shared web bundle.
log "Building web SPA → web/dist"
( cd "${REPO_ROOT}/web" && pnpm build )

cd "${MOBILE_DIR}"

# Ensure Capacitor deps are installed (cap CLI lives here).
if [ ! -d node_modules ]; then
  log "Installing mobile deps (pnpm install)"
  pnpm install
fi

# 2. Add platforms on first run, then sync.
add_if_missing() {
  local plat="$1"
  if [ ! -d "${plat}" ]; then
    log "Native project '${plat}/' missing — running 'npx cap add ${plat}'"
    npx cap add "${plat}"
  fi
}

case "${PLATFORM}" in
  ios)
    add_if_missing ios
    log "Syncing iOS"
    npx cap sync ios
    ;;
  android)
    add_if_missing android
    log "Syncing Android"
    npx cap sync android
    ;;
  all)
    add_if_missing ios
    add_if_missing android
    log "Syncing all platforms"
    npx cap sync
    ;;
  *)
    echo "Unknown platform '${PLATFORM}' (want: ios | android | all)" >&2
    exit 2
    ;;
esac

log "Done. Open with 'npx cap open ios' / 'npx cap open android'."

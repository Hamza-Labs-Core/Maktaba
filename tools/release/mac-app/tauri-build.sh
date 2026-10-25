#!/usr/bin/env bash
# `tauri build` for the Mac apps, with the notarization settings only when the app can be
# notarized. tauri's bundler notarizes a signed app whenever APPLE_ID, APPLE_PASSWORD and
# APPLE_TEAM_ID (or APPLE_API_KEY, APPLE_API_ISSUER and APPLE_API_KEY_PATH) are in the
# environment, set or empty. desktop-release.yml without the Apple secrets exported them
# empty, and once Maktaba.app was signed ad hoc (#273) every release's macos-arm64 leg failed:
# "failed to notarize app: Error: Team ID must be at least 3 characters" (v0.3.0-rc.7 and
# v0.3.1 shipped no darwin-aarch64 update). So an app signed ad hoc (no identity, or "-") is
# never notarized, a Developer ID app only with a complete set, and otherwise they are unset.
# build-app.sh (make mac-app, CI's mac-app gate) and desktop-release.yml's macOS legs build
# through here, so the gate builds the way the release does, in the release's environment.
#
# Usage: tauri-build.sh [--dry-run] [tauri build options...]   (run from anywhere; tauri runs
#        in apps/desktop, so relative --config paths are relative to it)
#   --dry-run  print how the app would be notarized and which settings tauri would get
#              (set or unset, never their values); builds nothing
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
NOTARIZE_VARS=(APPLE_ID APPLE_PASSWORD APPLE_TEAM_ID APPLE_API_KEY APPLE_API_ISSUER APPLE_API_KEY_PATH)

DRY=0
if [ "${1:-}" = --dry-run ]; then
    DRY=1
    shift
fi

# notarize_with: the credentials tauri notarizes with (apple-id, api-key), or none
notarize_with() {
    local id="${APPLE_SIGNING_IDENTITY:-}"
    if [ -z "$id" ] || [ "$id" = "-" ]; then
        echo none
    elif [ -n "${APPLE_ID:-}" ] && [ -n "${APPLE_PASSWORD:-}" ] && [ -n "${APPLE_TEAM_ID:-}" ]; then
        echo apple-id
    elif [ -n "${APPLE_API_KEY:-}" ] && [ -n "${APPLE_API_ISSUER:-}" ] && [ -n "${APPLE_API_KEY_PATH:-}" ]; then
        echo api-key
    else
        echo none
    fi
}

how="$(notarize_with)"
case "$how" in
    apple-id) unset APPLE_API_KEY APPLE_API_ISSUER APPLE_API_KEY_PATH ;;
    api-key) unset APPLE_ID APPLE_PASSWORD APPLE_TEAM_ID ;;
    *) unset "${NOTARIZE_VARS[@]}" ;;
esac
if [ -z "${APPLE_SIGNING_IDENTITY:-}" ] || [ "$APPLE_SIGNING_IDENTITY" = "-" ]; then
    echo "tauri-build: signed ad hoc (or not at all): not notarized"
elif [ "$how" = none ]; then
    echo "tauri-build: signed with a Developer ID, not notarized (no complete Apple ID or API key)"
else
    echo "tauri-build: signed with a Developer ID, notarized with the $how"
fi

if [ "$DRY" = 1 ]; then
    echo "notarize=$how"
    for v in "${NOTARIZE_VARS[@]}"; do
        if [ -n "${!v+x}" ]; then echo "$v=set"; else echo "$v=unset"; fi
    done
    exit 0
fi

cd "$REPO_ROOT/apps/desktop"
# CI=true: tauri's dmg script skips the Finder window layout (AppleScript), which hangs without a GUI session
CI="${CI:-true}" pnpm exec tauri build "$@"

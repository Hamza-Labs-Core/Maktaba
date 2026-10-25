#!/usr/bin/env bash
# Builds Maktaba.app for macOS arm64 with the server payload inside (Epic 36, story 36.7):
# payload.sh, then `tauri build` with the overlay it writes, then check.sh on the app.
# `make mac-app` runs it; so does CI's mac-app gate. desktop-release.yml runs the same three
# steps, then its publish job attaches what passed and writes the updater feed.
#
# Signing: with APPLE_SIGNING_IDENTITY set (a Developer ID Application identity in the
# keychain) the payload and the app are signed with the hardened runtime; without it the
# build is unsigned and runs locally (right-click > Open the first time).
#
# Usage: build-app.sh [--version VERSION]   (extra options go to payload.sh)
# Output: apps/desktop/src-tauri/target/aarch64-apple-darwin/release/bundle/{macos,dmg}/
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
BUILD="$REPO_ROOT/build/mac-app"

[ "$(uname -m)" = arm64 ] || { echo "build-app: Maktaba.app with the server is Apple Silicon only" >&2; exit 1; }

"$SCRIPT_DIR/payload.sh" --build "$BUILD" "$@"

cd "$REPO_ROOT/apps/desktop"
[ -d node_modules ] || pnpm install --frozen-lockfile
rustup target add aarch64-apple-darwin >/dev/null 2>&1 || true
# as desktop-release.yml builds it: notarized only when the app can be (tauri-build.sh)
"$SCRIPT_DIR/tauri-build.sh" --target aarch64-apple-darwin --bundles app,dmg --config "$BUILD/tauri.mac-app.conf.json"

bundle="$REPO_ROOT/apps/desktop/src-tauri/target/aarch64-apple-darwin/release/bundle"
signed=()
[ -n "${APPLE_SIGNING_IDENTITY:-}" ] && signed=(--signed)
"$SCRIPT_DIR/check.sh" --app "$bundle/macos/Maktaba.app" ${signed[@]+"${signed[@]}"}
du -sh "$bundle/macos/Maktaba.app" "$bundle"/dmg/*.dmg

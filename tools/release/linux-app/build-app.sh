#!/usr/bin/env bash
# Builds the Linux app with the server payload inside (Epic 39 story 39.10): payload.sh,
# then `tauri build` of the AppImage, the .deb and the .rpm with the overlay it writes
# (the payload in usr/share/maktaba/server), then check-app.sh on the three.
# `make linux-app` runs it; so does CI's Linux app gate. desktop-release.yml runs the same
# steps around tauri-action (which also publishes the updater feed).
#
# Usage: build-app.sh [--version VERSION]   (extra options go to payload.sh)
# Output: apps/desktop/src-tauri/target/release/bundle/{appimage,deb,rpm}/
# Needs what payload.sh needs, plus Rust and tauri's Linux libraries (libwebkit2gtk-4.1-dev,
# libgtk-3-dev, libayatana-appindicator3-dev, librsvg2-dev), dpkg-deb, rpm2cpio and cpio.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
BUILD="$REPO_ROOT/build/linux-app"

"$SCRIPT_DIR/payload.sh" --build "$BUILD" "$@"

cd "$REPO_ROOT/apps/desktop"
[ -d node_modules ] || pnpm install --frozen-lockfile
bundle="$REPO_ROOT/apps/desktop/src-tauri/target/release/bundle"
rm -rf "$bundle/appimage" "$bundle/deb" "$bundle/rpm"
pnpm exec tauri build --bundles appimage,deb,rpm --config "$BUILD/tauri.linux-app.conf.json"

"$SCRIPT_DIR/check-app.sh" "$bundle"/appimage/*.AppImage "$bundle"/deb/*.deb "$bundle"/rpm/*.rpm
du -sh "$bundle"/appimage/*.AppImage "$bundle"/deb/*.deb "$bundle"/rpm/*.rpm

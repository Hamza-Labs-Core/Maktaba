#!/usr/bin/env bash
# Builds the Linux app with the server payload inside (Epic 39 story 39.10): payload.sh,
# then bundle.sh of the AppImage, the .deb and the .rpm at the payload's version with the
# overlay it writes (the payload in usr/share/maktaba/server), then check-app.sh on the
# three.
# `make linux-app` runs it; so does CI's Linux app gate. desktop-release.yml runs the same
# steps, then its publish job attaches what passed and writes the updater feed.
#
# Usage: build-app.sh [--version VERSION]   (extra options go to payload.sh)
# Output: apps/desktop/src-tauri/target/release/bundle/{appimage,deb,rpm}/
# Needs what payload.sh needs, plus Rust and tauri's Linux libraries (libwebkit2gtk-4.1-dev,
# libgtk-3-dev, libayatana-appindicator3-dev, librsvg2-dev), dpkg-deb, rpm and bsdtar.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
BUILD="$REPO_ROOT/build/linux-app"

"$SCRIPT_DIR/payload.sh" --build "$BUILD" "$@"

cd "$REPO_ROOT/apps/desktop"
[ -d node_modules ] || pnpm install --frozen-lockfile
bundle="$REPO_ROOT/apps/desktop/src-tauri/target/release/bundle"
# the app at the payload's version when that is semver (a tag; CI's v0.0.0-ci.N, a
# pre-release like a release candidate's), else at its own (a commit with no tag)
version="$(python3 -c 'import json, sys; print(json.load(open(sys.argv[1]))["version"])' "$BUILD/server/payload.json")"
if ! "$SCRIPT_DIR/bundle.sh" --versions "$version" >/dev/null 2>&1; then
    version="$(node -p 'require("./src-tauri/tauri.conf.json").version')"
fi
"$SCRIPT_DIR/bundle.sh" "$version" --config "$BUILD/tauri.linux-app.conf.json"

"$SCRIPT_DIR/check-app.sh" "$bundle"/appimage/*.AppImage "$bundle"/deb/*.deb "$bundle"/rpm/*.rpm
du -sh "$bundle"/appimage/*.AppImage "$bundle"/deb/*.deb "$bundle"/rpm/*.rpm

#!/usr/bin/env bash
# Build the macOS .dmg containing the maktaba-server binary plus the
# first-run wizard launcher. Run from CI with the Apple Developer ID
# credentials exposed via env:
#
#   MAKTABA_APPLE_DEV_ID         Developer ID Application identity
#   MAKTABA_APPLE_TEAM_ID        Notarization team id
#   MAKTABA_APPLE_APP_PASSWORD   App-specific password for notarytool
#
# Outputs: dist/maktaba-server-<ver>-macos-{arm64,amd64}.dmg
set -euo pipefail
ver="${1:-dev}"
out="dist"
mkdir -p "$out"

for arch in arm64 amd64; do
  echo "==> Building macOS $arch ($ver)"
  GOOS=darwin GOARCH="$arch" go build \
    -o "$out/maktaba-server-$arch" \
    -ldflags "-X main.Version=$ver -X main.Commit=$(git rev-parse --short HEAD)" \
    ./api/

  if [[ -n "${MAKTABA_APPLE_DEV_ID:-}" ]]; then
    codesign --force --options runtime \
      --sign "$MAKTABA_APPLE_DEV_ID" \
      "$out/maktaba-server-$arch"
  else
    echo "WARN: MAKTABA_APPLE_DEV_ID unset; skipping codesign (unsigned binary)"
  fi

  pkg="$out/maktaba-server-$ver-macos-$arch.dmg"
  hdiutil create -volname "Maktaba Server" -srcfolder "$out/maktaba-server-$arch" -ov -format UDZO "$pkg"

  if [[ -n "${MAKTABA_APPLE_TEAM_ID:-}" && -n "${MAKTABA_APPLE_APP_PASSWORD:-}" ]]; then
    xcrun notarytool submit "$pkg" \
      --team-id "$MAKTABA_APPLE_TEAM_ID" \
      --apple-id releases@hamzalabs.com \
      --password "$MAKTABA_APPLE_APP_PASSWORD" --wait
    xcrun stapler staple "$pkg"
  fi

  echo "==> Built $pkg"
done

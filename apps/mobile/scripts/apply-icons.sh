#!/usr/bin/env bash
# Put the Maktaba app icon into the generated native projects.
#
# `npx cap add` scaffolds ios/ and android/ (gitignored) with Capacitor's
# default icon. This copies the prebuilt set from icons/ (cut from the master
# by tools/icons/make-icons.py) over it, for each platform that exists.
# Safe to run again after every `cap add` / `cap sync`.
#
# Usage: ./scripts/apply-icons.sh [ios | android | all]
set -euo pipefail

MOBILE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PLATFORM="${1:-all}"

if [[ "${PLATFORM}" == android || "${PLATFORM}" == all ]] && [ -d "${MOBILE_DIR}/android" ]; then
  # mipmap-*/ic_launcher{,_round,_foreground}.png and the adaptive
  # background colour; the template's mipmap-anydpi-v26 XML already names them.
  cp -R "${MOBILE_DIR}/icons/android-res/." "${MOBILE_DIR}/android/app/src/main/res/"
  echo "[apply-icons] android icons applied"
fi

if [[ "${PLATFORM}" == ios || "${PLATFORM}" == all ]] && [ -d "${MOBILE_DIR}/ios" ]; then
  # Capacitor's template ships one universal 1024 px AppIcon.
  cp "${MOBILE_DIR}/icons/ios/AppIcon-512@2x.png" \
    "${MOBILE_DIR}/ios/App/App/Assets.xcassets/AppIcon.appiconset/AppIcon-512@2x.png"
  echo "[apply-icons] ios icon applied"
fi

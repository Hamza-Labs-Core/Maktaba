#!/usr/bin/env bash
# The .dmg's Finder window (Epic 13 story 13.14): its designed background, size and icon
# places (tauri.conf.json bundle.macOS.dmg), written into the image without Finder.
#
# tauri's dmg script copies the background into .background/ but lays the window out over
# AppleScript, which needs a GUI session and hangs without one, so tauri-build.sh skips it
# (CI=true). This opens the built image read-write, writes its .DS_Store (dmg_layout.py, with
# the ds_store and mac_alias wheels pinned below, fetched by fetch-pinned.sh) and compresses
# it again, then signs it again when tauri had signed it (a Developer ID; ad hoc: not).
# tauri-build.sh runs it on every .dmg it built; check-dmg.sh checks it with --check.
#
# Usage: dmg-layout.sh DMG            lay DMG's window out, in place
#        dmg-layout.sh --check DMG    fail unless DMG's window is laid out
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
CONFIG="$REPO_ROOT/apps/desktop/src-tauri/tauri.conf.json"
CACHE="${DMG_LAYOUT_CACHE:-$REPO_ROOT/build/ci-pinned/dmg-layout}"

# the libraries dmgbuild writes a .DS_Store with (MIT): pure Python, for macOS's python3 (3.9)
DS_STORE_URL=https://files.pythonhosted.org/packages/47/bf/b1c10362a0d670ee8ae086d92c3ab795fca2a927e4ff25e7cd15224d3863/ds_store-1.3.1-py3-none-any.whl
DS_STORE_SHA256=fbacbb0bd5193ab3e66e5a47fff63619f15e374ffbec8ae29744251a6c8f05b5
MAC_ALIAS_URL=https://files.pythonhosted.org/packages/39/a1/4136777ed6a56df83e7c748ad28892f0672cbbcdc3b3d15a57df6ba72443/mac_alias-2.2.2-py3-none-any.whl
MAC_ALIAS_SHA256=504ab8ac546f35bbd75ad014d6ad977c426660aa721f2cd3acf3dc2f664141bd

die() { printf 'dmg-layout: error: %s\n' "$*" >&2; exit 1; }

MODE="write"
if [ "${1:-}" = --check ]; then MODE=check; shift; fi
[ $# -eq 1 ] || die "usage: dmg-layout.sh [--check] DMG"
DMG="$1"
[ -f "$DMG" ] || die "no $DMG"

fetch() { "$REPO_ROOT/tools/release/fetch-pinned.sh" "$1" "$2" "$CACHE/${1##*/}"; }
wheels=("$(fetch "$DS_STORE_URL" "$DS_STORE_SHA256")" "$(fetch "$MAC_ALIAS_URL" "$MAC_ALIAS_SHA256")")

SCRATCH="$(mktemp -d "${TMPDIR:-/tmp}/dmg-layout.XXXXXX")"
MNT="$SCRATCH/mnt"
mkdir "$MNT"
cleanup() {
    hdiutil detach -quiet "$MNT" 2>/dev/null || hdiutil detach -quiet -force "$MNT" 2>/dev/null || true
    rm -rf "$SCRATCH"
}
trap cleanup EXIT

if [ "$MODE" = check ]; then
    hdiutil attach -quiet -readonly -nobrowse -noautoopen -mountpoint "$MNT" "$DMG" || die "cannot mount $DMG"
    /usr/bin/python3 -I "$SCRIPT_DIR/dmg_layout.py" check "$MNT" "$CONFIG" "${wheels[@]}" || die "${DMG##*/}: its Finder window is not laid out (above)"
    echo "dmg-layout: ${DMG##*/}: the background, the window and the icons' places"
    exit 0
fi

rw="$SCRATCH/rw.dmg"
hdiutil convert -quiet "$DMG" -format UDRW -o "$rw" || die "cannot open $DMG for writing"
hdiutil attach -quiet -readwrite -nobrowse -noautoopen -mountpoint "$MNT" "$rw" || die "cannot mount ${rw##*/}"
/usr/bin/python3 -I "$SCRIPT_DIR/dmg_layout.py" write "$MNT" "$CONFIG" "${wheels[@]}"
rm -rf "$MNT/.fseventsd"
sync
hdiutil detach -quiet "$MNT" || hdiutil detach -quiet -force "$MNT"
out="$SCRATCH/out.dmg"
hdiutil convert -quiet "$rw" -format UDZO -imagekey zlib-level=9 -o "$out" || die "cannot compress ${rw##*/}"
mv -f "$out" "$DMG"
id="${APPLE_SIGNING_IDENTITY:-}"
if [ -n "$id" ] && [ "$id" != "-" ]; then
    codesign --force --timestamp --sign "$id" "$DMG"
    echo "dmg-layout: ${DMG##*/} signed again with the Developer ID"
fi
echo "dmg-layout: ${DMG##*/}: laid out"

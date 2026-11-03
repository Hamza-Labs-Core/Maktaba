#!/usr/bin/env bash
# Checks what a Maktaba .dmg holds, mounted read-only: one Maktaba.app whose payload is the
# flavour asked for (payload.json), with ollama in the full one and none in the core one
# (Epic 51 story 51.17). desktop-release.yml checks each Mac leg's .dmg with it before it is
# staged; CI's bundle-size job checks the Core leg's dry-run .dmg.
#
# Usage: check-dmg.sh DMG full|core
set -euo pipefail

die() { printf 'check-dmg: error: %s\n' "$*" >&2; exit 1; }

[ $# -eq 2 ] || die "usage: check-dmg.sh DMG full|core"
DMG="$1" FLAVOUR="$2"
case "$FLAVOUR" in full|core) ;; *) die "the flavour is full or core, not '$FLAVOUR'" ;; esac
[ -f "$DMG" ] || die "no $DMG"

MNT="$(mktemp -d "${TMPDIR:-/tmp}/check-dmg.XXXXXX")"
cleanup() {
    hdiutil detach -quiet "$MNT" 2>/dev/null || hdiutil detach -quiet -force "$MNT" 2>/dev/null || true
    rmdir "$MNT" 2>/dev/null || true
}
trap cleanup EXIT
hdiutil attach -quiet -readonly -nobrowse -noautoopen -mountpoint "$MNT" "$DMG" || die "cannot mount $DMG"

apps=("$MNT"/*.app)
[ "${#apps[@]}" -eq 1 ] && [ -d "${apps[0]}" ] || die "expected one .app in $DMG, found: ${apps[*]}"
[ "${apps[0]##*/}" = Maktaba.app ] || die "the .app is ${apps[0]##*/}, not Maktaba.app"
server="${apps[0]}/Contents/Resources/server"
[ -f "$server/payload.json" ] || die "no payload.json in the .dmg's app"
got="$(/usr/bin/python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1])).get("flavour","full"))' "$server/payload.json")"
[ "$got" = "$FLAVOUR" ] || die "the .dmg's payload is the $got flavour, not $FLAVOUR"
if [ "$FLAVOUR" = core ]; then
    [ ! -e "$server/ollama" ] || die "the core .dmg holds ollama/"
else
    [ -x "$server/ollama/ollama" ] || die "the full .dmg has no ollama"
fi
echo "check-dmg: ${DMG##*/}: Maktaba.app, $FLAVOUR payload$([ "$FLAVOUR" = core ] && echo ', no ollama/' || echo ', with ollama')"

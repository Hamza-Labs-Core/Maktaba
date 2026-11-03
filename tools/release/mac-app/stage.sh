#!/usr/bin/env bash
# Stages a built Maktaba.app's release files under the names the release and the updater
# feed know them by. desktop-release.yml's macOS legs and CI's bundle-size job (the Core
# leg's dry run) both stage through here.
#
#   full:  Maktaba_<v>_aarch64.dmg       Maktaba_<v>_aarch64.app.tar.gz(.sig)       darwin-aarch64
#   core:  Maktaba-Core_<v>_aarch64.dmg  Maktaba-Core_<v>_aarch64.app.tar.gz(.sig)  darwin-aarch64-core
#
# tauri names both flavours' files the same (Maktaba_<v>_<arch>.dmg, Maktaba.app.tar.gz), so
# the name is decided here, from the flavour asked for, after the app's own payload.json is
# checked to say that flavour (a core app staged under the Full names would update every
# Full Mac to an app with no ollama). Epic 51 story 51.17.
#
# Usage: stage.sh --bundle DIR --version V --out DIR [--flavour full|core] [--arch aarch64|x64]
#   --bundle  tauri's bundle folder (target/<target>/release/bundle)
#   --version the release's version, with or without its leading v
#   --out     where the files go (created)
set -euo pipefail

die() { printf 'stage: error: %s\n' "$*" >&2; exit 1; }

BUNDLE="" VERSION="" OUT="" FLAVOUR=full ARCH=aarch64
while [ $# -gt 0 ]; do
    case "$1" in
        --bundle)  [ $# -ge 2 ] || die "--bundle needs a value"; BUNDLE="$2"; shift 2 ;;
        --version) [ $# -ge 2 ] || die "--version needs a value"; VERSION="$2"; shift 2 ;;
        --out)     [ $# -ge 2 ] || die "--out needs a value"; OUT="$2"; shift 2 ;;
        --flavour) [ $# -ge 2 ] || die "--flavour needs a value"; FLAVOUR="$2"; shift 2 ;;
        --arch)    [ $# -ge 2 ] || die "--arch needs a value"; ARCH="$2"; shift 2 ;;
        -h|--help) sed -n '2,/^set -e/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//'; exit 0 ;;
        *)         die "unknown option '$1'" ;;
    esac
done
[ -n "$BUNDLE" ] && [ -n "$VERSION" ] && [ -n "$OUT" ] || die "--bundle, --version and --out are required"
case "$FLAVOUR" in full) PREFIX=Maktaba ;; core) PREFIX=Maktaba-Core ;; *) die "--flavour is full or core, not '$FLAVOUR'" ;; esac
case "$ARCH" in aarch64|x64) ;; *) die "--arch is aarch64 or x64, not '$ARCH'" ;; esac
v="${VERSION#v}"

# the app's payload must be the flavour it is staged as
payload="$BUNDLE/macos/Maktaba.app/Contents/Resources/server"
[ -f "$payload/payload.json" ] || die "no payload.json in $payload"
got="$(/usr/bin/python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1])).get("flavour","full"))' "$payload/payload.json")"
[ "$got" = "$FLAVOUR" ] || die "the app's payload is the $got flavour, staged as $FLAVOUR"
if [ "$FLAVOUR" = core ] && [ -e "$payload/ollama" ]; then
    die "the core app holds ollama/"
fi
if [ "$FLAVOUR" = full ] && [ ! -x "$payload/ollama/ollama" ]; then
    die "the full app has no ollama"
fi

shopt -s nullglob
mkdir -p "$OUT"
dmgs=("$BUNDLE"/dmg/*.dmg)
[ "${#dmgs[@]}" -eq 1 ] || die "expected one .dmg in $BUNDLE/dmg, got ${#dmgs[@]}"
cp "${dmgs[0]}" "$OUT/${PREFIX}_${v}_${ARCH}.dmg"
# the updater archive (with updater artifacts only: a build without the key has none)
for f in "$BUNDLE"/macos/*.app.tar.gz; do
    n="${PREFIX}_${v}_${ARCH}.app.tar.gz"
    cp "$f" "$OUT/$n"
    [ ! -f "$f.sig" ] || cp "$f.sig" "$OUT/$n.sig"
done
ls -l "$OUT"

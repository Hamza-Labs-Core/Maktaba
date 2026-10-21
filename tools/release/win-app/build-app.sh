#!/usr/bin/env bash
# Builds the Windows app with the server payload inside (Epic 38, story 38.11): Maktaba.exe
# and <install>\server as one NSIS installer (current user, %LOCALAPPDATA%\Maktaba, no
# administrator): payload.sh, then `tauri build` with the overlay it writes, then the hooks'
# test (test-hooks.sh) and check.sh on the installer (a silent install over a client-only
# app, a reinstall and an uninstall, in a temporary folder). `make win-app` runs
# it; so does CI's win-app gate. desktop-release.yml runs the same three steps around
# tauri-action (which also publishes the updater feed).
#
# Unsigned: SmartScreen warns on the first run of a downloaded installer. Authenticode
# signing is the owner's decision (README.md, Signing).
#
# Usage: build-app.sh [--version VERSION]   (extra options go to payload.sh)
# Output: <cargo target dir>/release/bundle/nsis/Maktaba_<version>_x64-setup.exe
#         (apps/desktop/src-tauri/target unless CARGO_TARGET_DIR is set)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
BUILD="$REPO_ROOT/build/win-app"

case "$(uname -s)" in
    MINGW*|MSYS*) ;;
    *) echo "build-app: the Windows app is built on Windows, in Git Bash" >&2; exit 1 ;;
esac

"$SCRIPT_DIR/payload.sh" --build "$BUILD" "$@"

cd "$REPO_ROOT/apps/desktop"
[ -d node_modules ] || pnpm install --frozen-lockfile
pnpm exec tauri build --bundles nsis --config "$(cygpath -m "$BUILD/tauri.win-app.conf.json")"

target="$(cygpath -u "${CARGO_TARGET_DIR:-$REPO_ROOT/apps/desktop/src-tauri/target}")"
# the newest installer: an earlier build of another version may still be there
setup=""
for f in "$target"/release/bundle/nsis/*-setup.exe; do
    if [ -f "$f" ] && { [ -z "$setup" ] || [ "$f" -nt "$setup" ]; }; then setup="$f"; fi
done
[ -n "$setup" ] || { echo "build-app: no installer in $target/release/bundle/nsis" >&2; exit 1; }
# the hooks on both kinds of Maktaba.exe (this build has one of them), with tauri's makensis
"$SCRIPT_DIR/test-hooks.sh"
"$SCRIPT_DIR/check.sh" --installer "$setup"
du -sh "$BUILD/server" "$setup"

#!/usr/bin/env bash
# Checks stage.sh on fake tauri bundles: each flavour's names, and the refusals (a core app
# staged as full, a full app staged as core, a core app with ollama, no payload.json).
# Runs anywhere (no build); CI's mac-app gate runs it.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STAGE="$SCRIPT_DIR/stage.sh"
T="$(mktemp -d)"
trap 'rm -rf "$T"' EXIT
fails=0
ok() { printf '  ok    %s\n' "$*"; }
bad() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }

# fake <dir> <flavour> [ollama]: a bundle folder as tauri leaves it
fake() {
    local b="$1" server="$1/macos/Maktaba.app/Contents/Resources/server"
    mkdir -p "$b/dmg" "$server"
    printf '{"version":"v1.2.3","flavour":"%s"}\n' "$2" >"$server/payload.json"
    if [ "${3:-}" = ollama ]; then
        mkdir -p "$server/ollama"
        printf '#!/bin/sh\n' >"$server/ollama/ollama"
        chmod +x "$server/ollama/ollama"
    fi
    echo dmg >"$b/dmg/Maktaba_1.2.3_aarch64.dmg"
    echo app >"$b/macos/Maktaba.app.tar.gz"
    echo sig >"$b/macos/Maktaba.app.tar.gz.sig"
}

names() { (cd "$1" && find . -type f | sed 's#^\./##' | sort | tr '\n' ' '); }

echo "stage.sh"
fake "$T/full" full ollama
"$STAGE" --bundle "$T/full" --version v1.2.3 --out "$T/out-full" >/dev/null
want="Maktaba_1.2.3_aarch64.app.tar.gz Maktaba_1.2.3_aarch64.app.tar.gz.sig Maktaba_1.2.3_aarch64.dmg "
if [ "$(names "$T/out-full")" = "$want" ]; then ok "full names"; else bad "full names: $(names "$T/out-full")"; fi

fake "$T/core" core
"$STAGE" --bundle "$T/core" --version 1.2.3 --flavour core --out "$T/out-core" >/dev/null
want="Maktaba-Core_1.2.3_aarch64.app.tar.gz Maktaba-Core_1.2.3_aarch64.app.tar.gz.sig Maktaba-Core_1.2.3_aarch64.dmg "
if [ "$(names "$T/out-core")" = "$want" ]; then ok "core names"; else bad "core names: $(names "$T/out-core")"; fi

refuses() {
    local why="$1"
    shift
    if "$STAGE" "$@" --version 1.2.3 --out "$T/out-refused" >/dev/null 2>&1; then
        bad "staged: $why"
    else
        ok "refused: $why"
    fi
}
refuses "a core app as full" --bundle "$T/core"
refuses "a full app as core" --bundle "$T/full" --flavour core
fake "$T/core-ollama" core ollama
refuses "a core app with ollama" --bundle "$T/core-ollama" --flavour core
fake "$T/full-no-ollama" full
refuses "a full app with no ollama" --bundle "$T/full-no-ollama"
mkdir -p "$T/empty/dmg"
refuses "no payload.json" --bundle "$T/empty"
refuses "an unknown flavour" --bundle "$T/full" --flavour lite
if [ -e "$T/out-refused" ] && [ -n "$(ls -A "$T/out-refused")" ]; then
    bad "a refused stage left files"
fi

[ "$fails" -eq 0 ] || { echo "stage-check: $fails failed"; exit 1; }
echo "stage-check: all passed"

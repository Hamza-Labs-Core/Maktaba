#!/usr/bin/env bash
# Checks tauri-build.sh's notarization settings (see its header) without building: the
# release's environment without the Apple secrets (desktop-release.yml exports them empty)
# and an ad-hoc app never reach tauri, a Developer ID app gets only a complete set, and the
# release's macOS legs build through tauri-build.sh. Runs anywhere bash does; CI's mac-app
# gate runs it before it builds the app in that same environment.
#
# Usage: tauri-build-check.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
fails=0
ok()   { printf '  ok    %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }

# expect NAME WANT ENV...: tauri-build.sh --dry-run in exactly ENV prints every line of WANT
expect() {
    local name="$1" want="$2" got line
    shift 2
    got="$(env -i PATH="$PATH" "$@" bash "$SCRIPT_DIR/tauri-build.sh" --dry-run)"
    for line in $want; do
        if ! grep -qx -- "$line" <<<"$got"; then
            fail "$name: no '$line' in:"$'\n'"$got"
            return
        fi
    done
    ok "$name"
}

NONE="notarize=none APPLE_ID=unset APPLE_PASSWORD=unset APPLE_TEAM_ID=unset APPLE_API_KEY=unset APPLE_API_ISSUER=unset APPLE_API_KEY_PATH=unset"

expect "the release without the Apple secrets (set, empty)" "$NONE" \
    APPLE_ID= APPLE_PASSWORD= APPLE_TEAM_ID=
expect "no Apple settings at all" "$NONE"
expect "signed ad hoc, with a complete Apple ID" "$NONE" \
    APPLE_SIGNING_IDENTITY=- APPLE_ID=a@b.c APPLE_PASSWORD=p APPLE_TEAM_ID=TEAM123456
expect "no identity, with a complete API key" "$NONE" \
    APPLE_API_KEY=k APPLE_API_ISSUER=i APPLE_API_KEY_PATH=/k.p8
expect "a Developer ID with an empty team" "$NONE" \
    "APPLE_SIGNING_IDENTITY=Developer ID Application: X (TEAM123456)" APPLE_ID=a@b.c APPLE_PASSWORD=p APPLE_TEAM_ID=
expect "a Developer ID with the Apple ID trio" \
    "notarize=apple-id APPLE_ID=set APPLE_PASSWORD=set APPLE_TEAM_ID=set APPLE_API_KEY=unset APPLE_API_ISSUER=unset APPLE_API_KEY_PATH=unset" \
    "APPLE_SIGNING_IDENTITY=Developer ID Application: X (TEAM123456)" APPLE_ID=a@b.c APPLE_PASSWORD=p APPLE_TEAM_ID=TEAM123456 \
    APPLE_API_KEY= APPLE_API_ISSUER= APPLE_API_KEY_PATH=
expect "a Developer ID with an API key" \
    "notarize=api-key APPLE_ID=unset APPLE_PASSWORD=unset APPLE_TEAM_ID=unset APPLE_API_KEY=set APPLE_API_ISSUER=set APPLE_API_KEY_PATH=set" \
    "APPLE_SIGNING_IDENTITY=Developer ID Application: X (TEAM123456)" APPLE_ID= APPLE_PASSWORD= APPLE_TEAM_ID= \
    APPLE_API_KEY=k APPLE_API_ISSUER=i APPLE_API_KEY_PATH=/k.p8

# the release's macOS legs build through tauri-build.sh, not tauri directly
wf="$REPO_ROOT/.github/workflows/desktop-release.yml"
if grep -q 'tools/release/mac-app/tauri-build.sh' "$wf"; then
    ok "desktop-release.yml builds the Mac apps through tauri-build.sh"
else
    fail "desktop-release.yml does not build the Mac apps through tools/release/mac-app/tauri-build.sh"
fi

if [ "$fails" -gt 0 ]; then
    echo "tauri-build-check: $fails failed"
    exit 1
fi
echo "tauri-build-check: all passed"

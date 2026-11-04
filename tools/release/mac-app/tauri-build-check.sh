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

# builds BUSY FAIL WANT_RC WANT_RUNS NAME: tauri-build.sh with a stand-in pnpm whose build
# fails BUSY times on hdiutil's "Resource busy", then FAIL times on another error, then passes
builds() {
    local busy="$1" other="$2" want_rc="$3" want_runs="$4" name="$5" dir rc runs
    dir="$(mktemp -d)"
    cat >"$dir/pnpm" <<'FAKE'
#!/usr/bin/env bash
n=$(( $(cat "$FAKE_DIR/runs" 2>/dev/null || echo 0) + 1 ))
echo "$n" >"$FAKE_DIR/runs"
if [ "$n" -le "$FAKE_BUSY" ]; then
    echo "failed to bundle project: hdiutil: create failed - Resource busy" >&2; exit 1
fi
if [ "$n" -le $((FAKE_BUSY + FAKE_OTHER)) ]; then
    echo "error: linking failed" >&2; exit 1
fi
echo "built"
FAKE
    chmod +x "$dir/pnpm"
    rc=0
    env -i PATH="$dir:$PATH" FAKE_DIR="$dir" FAKE_BUSY="$busy" FAKE_OTHER="$other" HDIUTIL_PAUSE=0 \
        bash "$SCRIPT_DIR/tauri-build.sh" build >/dev/null 2>&1 || rc=$?
    runs="$(cat "$dir/runs" 2>/dev/null || echo 0)"
    rm -rf "$dir"
    if [ "$rc" -eq "$want_rc" ] && [ "$runs" -eq "$want_runs" ]; then
        ok "$name"
    else
        fail "$name: exit $rc after $runs builds (want exit $want_rc after $want_runs)"
    fi
}

builds 0 0 0 1 "a build that passes is built once"
builds 2 0 0 3 "hdiutil busy twice, then the build passes"
builds 0 1 1 1 "another failure fails at once"
builds 5 0 1 3 "hdiutil busy every time fails after three builds"

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

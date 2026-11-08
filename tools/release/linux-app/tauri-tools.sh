#!/usr/bin/env bash
# Puts what tauri-cli's AppImage bundler would fetch from GitHub releases in place, pinned
# by SHA-256 (tauri-tools.pins), so the bundle downloads nothing: CI's Linux app job and
# desktop-release.yml's Linux legs run it before bundle.sh.
#
#   tauri-tools.sh ARCH          fetch and install the tools for ARCH (x86_64 or aarch64)
#   tauri-tools.sh --key ARCH    the actions/cache key for ARCH's downloads
#
# Each file goes through tools/release/fetch-pinned.sh (the GitHub API's route first,
# retried, checked; a file already in DOWNLOADS with the pin is kept) into DOWNLOADS
# ($TAURI_TOOLS_DOWNLOADS, default build/ci-pinned/tauri-tools), then:
#   apprun, linuxdeploy  into tauri's tools folder ($TAURI_TOOLS_DIR, default
#                        ${XDG_CACHE_HOME:-~/.cache}/tauri) under the names tauri looks for,
#                        mode 755: tauri downloads neither when they are there.
#   plugin               there too as linuxdeploy-plugin-appimage.AppImage; when its pin no
#                        longer matches (its `continuous` release moved) a warning, and
#                        tauri downloads it as before.
#   runtime              the AppImage type2 runtime: its path is written as LDAI_RUNTIME_FILE
#                        to $GITHUB_ENV (or printed, outside Actions). The appimage plugin
#                        passes it to appimagetool as --runtime-file, which then downloads
#                        no runtime (it fetched type2-runtime's `continuous` build while
#                        bundling, unchecked: the suspect when an aarch64 bundle failed in
#                        linuxdeploy, run 42480410454).
# A file whose SHA-256 is not its pin fails the step, the plugin's excepted.
# TAURI_TOOLS_PINS points at another pins file (the tests).
# tools/ci/tests/test_tauri_tools.py runs it against a local server.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
FETCH="$REPO_ROOT/tools/release/fetch-pinned.sh"
PINS="${TAURI_TOOLS_PINS:-$SCRIPT_DIR/tauri-tools.pins}"

die() { echo "::error::tauri-tools.sh: $*" >&2; exit 1; }

# pin KIND ARCH: prints "SHA256 URL" from the pins file, or fails
pin() {
    local line
    line="$(awk -v k="$1" -v a="$2" '$1 == k && $2 == a { print $3, $4; exit }' "$PINS")"
    [ -n "$line" ] || die "no $1 pin for $2 in $PINS"
    printf '%s\n' "$line"
}

key=false
if [ "${1:-}" = --key ]; then
    key=true
    shift
fi
[ $# -eq 1 ] || die "usage: tauri-tools.sh [--key] ARCH"
arch="$1"
case "$arch" in
    x86_64 | aarch64) ;;
    *) die "no tauri tool pins for $arch" ;;
esac

if $key; then
    # every pin of ARCH, in order: any changed pin misses
    pins="$(awk -v a="$arch" '!/^#/ && $2 == a { print $1, $3 }' "$PINS")"
    if command -v sha256sum >/dev/null 2>&1; then
        sum="$(printf '%s\n' "$pins" | sha256sum)"
    else
        sum="$(printf '%s\n' "$pins" | shasum -a 256)"
    fi
    printf 'pinned-linux-tauri-tools-%s-%s\n' "$arch" "${sum:0:16}"
    exit 0
fi

downloads="${TAURI_TOOLS_DOWNLOADS:-$REPO_ROOT/build/ci-pinned/tauri-tools}"
tools="${TAURI_TOOLS_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/tauri}"
mkdir -p "$downloads" "$tools"
downloads="$(cd "$downloads" && pwd)"

# fetch KIND: fetches KIND's pinned file for arch into downloads, prints its path
fetch() {
    local sha url
    read -r sha url <<<"$(pin "$1" "$arch")"
    "$FETCH" "$url" "$sha" "$downloads/${url##*/}"
}

a="$(fetch apprun)"
l="$(fetch linuxdeploy)"
install -m 755 "$a" "$tools/AppRun-$arch"
install -m 755 "$l" "$tools/linuxdeploy-07333c6-$arch.AppImage"
if p="$(fetch plugin)"; then
    install -m 755 "$p" "$tools/linuxdeploy-plugin-appimage.AppImage"
else
    rm -f "$tools/linuxdeploy-plugin-appimage.AppImage"
    read -r _ url <<<"$(pin plugin "$arch")"
    echo "::warning::linuxdeploy-plugin-appimage ($url) is not the pinned build: tauri downloads it"
fi
runtime="$(fetch runtime)"
chmod 644 "$runtime"
if [ -n "${GITHUB_ENV:-}" ]; then
    echo "LDAI_RUNTIME_FILE=$runtime" >>"$GITHUB_ENV"
fi
echo "LDAI_RUNTIME_FILE=$runtime"
ls -l "$tools"

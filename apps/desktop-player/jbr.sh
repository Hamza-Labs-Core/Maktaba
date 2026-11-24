#!/usr/bin/env bash
# The JetBrains Runtime the Player is packaged with (story 13.15): its own title bar
# (shell/PlayerFrame.kt, Jewel's DecoratedWindow) needs JBR's custom title bar, which no other
# JDK has. The build itself still runs on Temurin; the installers' runtime is jlinked from
# this JBR through `-Pmaktaba.jbr=<its home>` (build.gradle.kts, compose's javaHome).
#
#   jbr.sh --key PLATFORM    the actions/cache key for that platform's archive
#   jbr.sh PLATFORM          fetches it (checked against its pin), unpacks it under
#                            build/jbr/PLATFORM, prints its home
#
# PLATFORM is windows-x64, linux-x64 or linux-aarch64. A new JBR changes VERSION and the
# three pins together (the SHA-256s JetBrains publishes beside each archive).
set -euo pipefail

VERSION=21.0.11
BUILD=b1163.125
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"

key=0
if [ "${1:-}" = --key ]; then key=1; shift; fi
platform="${1:?usage: jbr.sh [--key] windows-x64|linux-x64|linux-aarch64}"
case "$platform" in
    windows-x64) sha=70cba75c4675881437670f639d49d8a63f363970b9c3eef45f3f4fad7726a290 ;;
    linux-x64) sha=255e47175863ed7be108da3eae3133e8f1a78eff47f938d447acec11a5556b14 ;;
    linux-aarch64) sha=6c2d1691b1de485adce5a48ace53cc6d3638ea036a7e4f004b376792c3f368c2 ;;
    *) printf '::error::jbr.sh: no JBR pinned for %s\n' "$platform" >&2; exit 1 ;;
esac
name="jbrsdk-${VERSION}-${platform}-${BUILD}"
url="https://cache-redirector.jetbrains.com/intellij-jbr/${name}.tar.gz"

if [ "$key" = 1 ]; then
    exec "$root/tools/release/fetch-pinned.sh" --key "$url" "$sha"
fi

archive="$("$root/tools/release/fetch-pinned.sh" "$url" "$sha" "$root/build/ci-pinned/jbr/${name}.tar.gz")"
home="$here/build/jbr/$platform"
rm -rf "$home"
mkdir -p "$home"
tar -xzf "$archive" -C "$home" --strip-components=1
[ -x "$home/bin/jlink" ] || [ -x "$home/bin/jlink.exe" ] || { printf '::error::%s has no bin/jlink\n' "$name" >&2; exit 1; }
grep -q 'JetBrains' "$home/release" || { printf '::error::%s is not a JetBrains runtime\n' "$name" >&2; exit 1; }
printf '%s\n' "$home"

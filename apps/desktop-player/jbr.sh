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
# three pins together. JetBrains publishes a SHA-512 beside each archive (<archive>.checksum):
# check the download against it, then pin its SHA-256 here.
#
# Its major follows Jewel's: jewel-int-ui-decorated-window 0.42 (IntelliJ 2026.3) is compiled
# for Java 25 (class file 69), so a JBR 21 cannot load it.
set -euo pipefail

VERSION=25.0.4.1
BUILD=b635.79
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"

key=0
if [ "${1:-}" = --key ]; then key=1; shift; fi
platform="${1:?usage: jbr.sh [--key] windows-x64|linux-x64|linux-aarch64}"
case "$platform" in
    windows-x64) sha=49176eb6b06ebc5b51bf444060918901ca17eb4688aab3e233439fe382a05d20 ;;
    linux-x64) sha=37d4a7c0438c635de7d501af9360488bdf1bc595db373522d1ba237aa1e3bc6b ;;
    linux-aarch64) sha=5a4df4f511217fa2cc30d7586d3e820400192fc98b688c68a37ac4d86b7cacb8 ;;
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

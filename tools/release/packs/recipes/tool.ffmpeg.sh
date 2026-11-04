#!/usr/bin/env bash
# tool.ffmpeg: static ffmpeg and ffprobe for a Linux machine (ffmpeg/, as the Mac app's
# payload lays them out), with their licence. Only the Linux platforms have this pack.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
stage="$1" platform="$2"
# shellcheck source-path=SCRIPTDIR source=../pins.env
. "$here/../pins.env"
# shellcheck source-path=SCRIPTDIR source=../fetch.sh
. "$here/../fetch.sh"

case "$platform" in
    linux-amd64) ff_url="$FFMPEG_LINUX_AMD64_URL" ff_sum="$FFMPEG_LINUX_AMD64_SHA256"
        fp_url="$FFPROBE_LINUX_AMD64_URL" fp_sum="$FFPROBE_LINUX_AMD64_SHA256" ;;
    linux-arm64) ff_url="$FFMPEG_LINUX_ARM64_URL" ff_sum="$FFMPEG_LINUX_ARM64_SHA256"
        fp_url="$FFPROBE_LINUX_ARM64_URL" fp_sum="$FFPROBE_LINUX_ARM64_SHA256" ;;
    *) echo "tool.ffmpeg: no build for $platform" >&2; exit 1 ;;
esac

mkdir -p "$stage/ffmpeg" "$stage/licenses"
# each fetched first: a failed download or digest fails the recipe here
ff="$(fetch "$ff_url" "$ff_sum" "ffmpeg-$FFMPEG_VERSION-$platform.zip")"
fp="$(fetch "$fp_url" "$fp_sum" "ffprobe-$FFMPEG_VERSION-$platform.zip")"
unzip -q "$ff" -d "$stage/ffmpeg"
unzip -q "$fp" -d "$stage/ffmpeg"
for b in ffmpeg ffprobe; do
    [ -f "$stage/ffmpeg/$b" ] || { echo "tool.ffmpeg: the archive has no $b" >&2; exit 1; }
done
extra="$(find "$stage/ffmpeg" -mindepth 1 ! -name ffmpeg ! -name ffprobe)"
[ -z "$extra" ] || { echo "tool.ffmpeg: unexpected files in the archives: $extra" >&2; exit 1; }
chmod 0755 "$stage/ffmpeg/ffmpeg" "$stage/ffmpeg/ffprobe"
cat >"$stage/licenses/ffmpeg.txt" <<EOF
ffmpeg and ffprobe $FFMPEG_VERSION, built by Martin Riedl (https://ffmpeg.martin-riedl.de),
configured with --enable-gpl --enable-version3: licensed under the GNU General Public
License version 3 (https://www.gnu.org/licenses/gpl-3.0.html).

Binaries: $ff_url
          $fp_url
Source:   https://ffmpeg.org/releases/ffmpeg-$FFMPEG_VERSION.tar.xz
EOF

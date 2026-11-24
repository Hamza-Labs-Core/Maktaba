#!/usr/bin/env bash
# The clips the Player's decoder probe plays at start (story 13.19), made once with ffmpeg into
# DIR (src/main/resources/probe) and committed: 24 frames of 2160p each (one second at 24 fps,
# one key frame; MPEG-2's 1080p, below), each under 100 KB, in the codecs the desktop-mpv profile lists (H.264 High,
# HEVC Main and Main 10, VP9 profiles 0 and 2, AV1 Main 8-bit and 10-bit, MPEG-2), and a 0.3 s
# silent 7.1 clip the audio device's channels are measured with.
# Needs an ffmpeg with libx264, libx265, libvpx-vp9 and libsvtav1.
set -euo pipefail
dir="${1:?usage: probe-media.sh DIR}"
mkdir -p "$dir"
src=(-f lavfi -i gradients=size=3840x2160:rate=24:speed=0.02:seed=7 -frames:v 24 -an -map_metadata -1 -fflags +bitexact)
x265=(-x265-params log-level=error:keyint=24:min-keyint=24)
ffmpeg -v error -y "${src[@]}" -pix_fmt yuv420p -c:v libx264 -profile:v high -preset veryfast -crf 34 -g 24 "$dir/h264-8.mkv"
ffmpeg -v error -y "${src[@]}" -pix_fmt yuv420p -c:v libx265 -profile:v main -preset veryfast -crf 30 "${x265[@]}" "$dir/hevc-8.mkv"
ffmpeg -v error -y "${src[@]}" -pix_fmt yuv420p10le -c:v libx265 -profile:v main10 -preset veryfast -crf 30 "${x265[@]}" "$dir/hevc-10.mkv"
ffmpeg -v error -y "${src[@]}" -pix_fmt yuv420p -c:v libvpx-vp9 -profile:v 0 -b:v 0 -crf 60 -deadline realtime -cpu-used 8 -g 24 "$dir/vp9-8.mkv"
ffmpeg -v error -y "${src[@]}" -pix_fmt yuv420p10le -c:v libvpx-vp9 -profile:v 2 -b:v 0 -crf 60 -deadline realtime -cpu-used 8 -g 24 "$dir/vp9-10.mkv"
ffmpeg -v error -y "${src[@]}" -pix_fmt yuv420p -c:v libsvtav1 -preset 10 -crf 40 -g 24 "$dir/av1-8.mkv"
ffmpeg -v error -y "${src[@]}" -pix_fmt yuv420p10le -c:v libsvtav1 -preset 10 -crf 40 -g 24 "$dir/av1-10.mkv"
# MPEG-2 at 1080p: its High Level stops at 1920x1152, and a 2160p frame's macroblocks alone are
# over 100 KB; the probe scales its speed by the pixel count (Probe.CLIPS)
ffmpeg -v error -y -f lavfi -i gradients=size=1920x1080:rate=24:speed=0.02:seed=7 -frames:v 24 -an -map_metadata -1 -fflags +bitexact \
  -pix_fmt yuv420p -c:v mpeg2video -q:v 12 -g 24 "$dir/mpeg2video-8.mkv"
ffmpeg -v error -y -f lavfi -i "anullsrc=channel_layout=7.1:sample_rate=48000" -t 0.3 -map_metadata -1 -fflags +bitexact \
  -c:a flac "$dir/silence-7.1.mka"
for f in "$dir"/*.mkv; do
  size=$(wc -c <"$f")
  [ "$size" -lt 102400 ] || { echo "$f is $size bytes, over 100 KB" >&2; exit 1; }
done
ls -l "$dir"

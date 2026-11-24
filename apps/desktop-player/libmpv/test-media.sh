#!/usr/bin/env bash
# The files MpvRealTest plays (story 13.18 T01, T02), made with the runner's ffmpeg into DIR:
# a 120 s 1080p H.264 MKV with AC-3 5.1, and a 20 s 2160p HEVC 10-bit MKV.
set -euo pipefail
dir="${1:?usage: test-media.sh DIR}"
mkdir -p "$dir"
ffmpeg -v error -y -f lavfi -i testsrc2=size=1920x1080:rate=24 -f lavfi -i "sine=frequency=440:sample_rate=48000" \
  -t 120 -filter_complex "[1:a]pan=5.1|c0=c0|c1=c0|c2=c0|c3=c0|c4=c0|c5=c0[a]" -map 0:v -map "[a]" \
  -c:v libx264 -preset ultrafast -crf 32 -g 48 -c:a ac3 -b:a 384k "$dir/h264-ac3-51-1080p.mkv"
ffmpeg -v error -y -f lavfi -i testsrc2=size=3840x2160:rate=24 -t 20 -pix_fmt yuv420p10le \
  -c:v libx265 -preset ultrafast -crf 34 -x265-params log-level=error "$dir/hevc-10bit-2160p.mkv"
ls -l "$dir"

#!/usr/bin/env bash
# The files MpvRealTest and ChannelMpvRealTest play, made with the runner's ffmpeg into DIR:
# - story 13.18 T01, T02: a 120 s 1080p H.264 MKV with AC-3 5.1, and a 20 s 2160p HEVC 10-bit MKV;
# - story 13.22 T02-T04: two channels' live HLS of H.264 and AAC in 1 s MPEG-TS segments
#   (live/ red and live2/ blue: seg000.ts…, listed in full.m3u8), which the test's fake home lists in a sliding
#   window of six, as ffmpeg's hls_list_size does, one more each second while the test runs.
set -euo pipefail
dir="${1:?usage: test-media.sh DIR}"
mkdir -p "$dir"
ffmpeg -v error -y -f lavfi -i testsrc2=size=1920x1080:rate=24 -f lavfi -i "sine=frequency=440:sample_rate=48000" \
  -t 120 -filter_complex "[1:a]pan=5.1|c0=c0|c1=c0|c2=c0|c3=c0|c4=c0|c5=c0[a]" -map 0:v -map "[a]" \
  -c:v libx264 -preset ultrafast -crf 32 -g 48 -c:a ac3 -b:a 384k "$dir/h264-ac3-51-1080p.mkv"
ffmpeg -v error -y -f lavfi -i testsrc2=size=3840x2160:rate=24 -t 20 -pix_fmt yuv420p10le \
  -c:v libx265 -preset ultrafast -crf 34 -x265-params log-level=error "$dir/hevc-10bit-2160p.mkv"
# two channels told apart by their picture: live is red, live2 blue
live() {
  mkdir -p "$dir/$1"
  ffmpeg -v error -y -f lavfi -i "color=c=$2:size=1280x720:rate=25" -f lavfi -i "sine=frequency=660:sample_rate=48000" \
    -t 60 -map 0:v -map 1:a -pix_fmt yuv420p -c:v libx264 -preset ultrafast -crf 32 -g 25 -keyint_min 25 -sc_threshold 0 \
    -c:a aac -b:a 96k -ac 2 -f hls -hls_time 1 -hls_list_size 0 -hls_segment_type mpegts \
    -hls_segment_filename "$dir/$1/seg%03d.ts" "$dir/$1/full.m3u8"
}
live live 0xC02020
live live2 0x2040C0
ls -l "$dir" "$dir/live/full.m3u8" "$dir/live2/full.m3u8"

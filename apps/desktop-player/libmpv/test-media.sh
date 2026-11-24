#!/usr/bin/env bash
# The files MpvRealTest plays (story 13.18 T01, T02), made with the runner's ffmpeg into DIR:
# a 120 s 1080p H.264 MKV with AC-3 5.1, and a 20 s 2160p HEVC 10-bit MKV. Story 13.20 (T05,
# T06): a 12 s black 1280x720 MKV with three Arabic subtitle tracks, each on from 1 s to 9 s.
set -euo pipefail
dir="${1:?usage: test-media.sh DIR}"
here="$(cd "$(dirname "$0")" && pwd)"
mkdir -p "$dir"
# an SRT; an ASS whose style names a Latin-only font (Arial), in its own green; and the
# committed PGS picture (make-pgs.java: ffmpeg has no PGS encoder)
cat >"$dir/arabic.srt" <<'SRT'
1
00:00:01,000 --> 00:00:09,000
مرحبا بكم في مكتبة
SRT
cat >"$dir/arabic.ass" <<'ASS'
[Script Info]
ScriptType: v4.00+
PlayResX: 1280
PlayResY: 720

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Arial,48,&H0000FF00,&H0000FF00,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,0,2,20,20,40,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:01.00,0:00:09.00,Default,,0,0,0,,مرحبا بكم في مكتبة
ASS
ffmpeg -v error -y -f lavfi -t 12 -i color=c=black:size=1280x720:rate=24 \
  -i "$dir/arabic.srt" -i "$dir/arabic.ass" -i "$here/../src/test/resources/subs/arabic.sup" \
  -map 0:v -map 1:s -map 2:s -map 3:s -c:v libx264 -preset ultrafast -crf 30 -g 24 \
  -c:s:0 srt -c:s:1 ass -c:s:2 copy \
  -metadata:s:s:0 language=ara -metadata:s:s:0 title=SRT \
  -metadata:s:s:1 language=ara -metadata:s:s:1 title=ASS \
  -metadata:s:s:2 language=ara -metadata:s:s:2 title=PGS \
  "$dir/subs-ar.mkv"
ffmpeg -v error -y -f lavfi -i testsrc2=size=1920x1080:rate=24 -f lavfi -i "sine=frequency=440:sample_rate=48000" \
  -t 120 -filter_complex "[1:a]pan=5.1|c0=c0|c1=c0|c2=c0|c3=c0|c4=c0|c5=c0[a]" -map 0:v -map "[a]" \
  -c:v libx264 -preset ultrafast -crf 32 -g 48 -c:a ac3 -b:a 384k "$dir/h264-ac3-51-1080p.mkv"
ffmpeg -v error -y -f lavfi -i testsrc2=size=3840x2160:rate=24 -t 20 -pix_fmt yuv420p10le \
  -c:v libx265 -preset ultrafast -crf 34 -x265-params log-level=error "$dir/hevc-10bit-2160p.mkv"
ls -l "$dir"

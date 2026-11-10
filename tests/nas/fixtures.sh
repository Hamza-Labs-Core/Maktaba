#!/usr/bin/env bash
# The NAS cells' fixture library (story 59.15): 40 short clips made here, nothing downloaded
# and nothing committed. Every clip is synthetic and so free of any licence: a test pattern
# with a spoken sentence (espeak-ng) in English or Arabic.
#
#   en/Lesson 01.mp4 … Lesson 19.mp4     H.264 + AAC, audio tagged eng
#   en/Lesson 20 (HEVC).mkv               HEVC + AAC: the one a player may need transcoded
#   ar/درس 01.mp4 … درس 20.mp4           H.264 + AAC, audio tagged ara (Arabic file names)
#   ar/درس 01.ar.srt                      an Arabic side-car subtitle for درس 01
#
# Usage: fixtures.sh OUT_DIR
# Runs in a throwaway debian:12-slim container (its ffmpeg has libx264 and libx265, its
# espeak-ng speaks both languages), so the runner and a developer's machine make the same
# files. The files belong to the caller's uid. About 3 MB, a minute or two.
set -euo pipefail

OUT="${1:?usage: fixtures.sh OUT_DIR}"
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"
COUNT_EN=20
COUNT_AR=20

docker run --rm -i -v "$OUT:/out" -e COUNT_EN="$COUNT_EN" -e COUNT_AR="$COUNT_AR" \
    -e OWNER="$(id -u):$(id -g)" debian:12-slim bash -s <<'IN'
set -euo pipefail
apt-get update -qq >/dev/null
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends ffmpeg espeak-ng >/dev/null
cd /out
mkdir -p en ar
en=("The library opens at nine in the morning." "Water boils at one hundred degrees." "The train leaves from platform four." "Please close the window before the rain." "Our garden has three apple trees.")
ar=("تفتح المكتبة في التاسعة صباحا." "يغلي الماء عند مئة درجة." "يغادر القطار من الرصيف الرابع." "أغلق النافذة قبل المطر من فضلك." "في حديقتنا ثلاث أشجار تفاح.")
# clip LANG VOICE TEXT OUT [CODEC]: a 4 s test pattern with TEXT spoken
clip() {
    local lang="$1" voice="$2" text="$3" out="$4" codec="${5:-libx264}" wav
    wav="$(mktemp --suffix .wav)"
    espeak-ng -v "$voice" -w "$wav" "$text"
    ffmpeg -nostdin -loglevel error -y \
        -f lavfi -i "testsrc2=size=320x180:rate=24:duration=4" -i "$wav" \
        -map 0:v -map 1:a -c:v "$codec" -preset veryfast -x265-params log-level=error -pix_fmt yuv420p \
        -c:a aac -b:a 48k -ar 16000 -af apad -t 4 \
        -metadata:s:a:0 language="$lang" -metadata title="${out##*/}" "$out"
    rm -f "$wav"
}
for i in $(seq 1 "$COUNT_EN"); do
    n="$(printf '%02d' "$i")" t="${en[$(( (i - 1) % ${#en[@]} ))]}"
    if [ "$i" -eq "$COUNT_EN" ]; then
        clip eng en "$t" "en/Lesson $n (HEVC).mkv" libx265
    else
        clip eng en "$t" "en/Lesson $n.mp4"
    fi
done
for i in $(seq 1 "$COUNT_AR"); do
    n="$(printf '%02d' "$i")" t="${ar[$(( (i - 1) % ${#ar[@]} ))]}"
    clip ara ar "$t" "ar/درس $n.mp4"
done
cat > "ar/درس 01.ar.srt" <<'SRT'
1
00:00:00,000 --> 00:00:02,000
تفتح المكتبة

2
00:00:02,000 --> 00:00:04,000
في التاسعة صباحا
SRT
chown -R "$OWNER" /out
IN
find "$OUT" -type f | wc -l | tr -d ' ' | xargs -I{} echo "fixtures: {} files in $OUT"
du -sh "$OUT" | cut -f1 | xargs -I{} echo "fixtures: {}"

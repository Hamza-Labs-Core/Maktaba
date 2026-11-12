#!/usr/bin/env bash
# Rebuilds static/fonts/ (story 60.01): IBM Plex Sans Arabic Regular and SemiBold, from the
# copies the Apple app ships, subset to Latin and to Arabic, without hinting, as WOFF2, and
# renamed "Maktaba Text" (rename.py: the OFL reserves the name "Plex" for the original).
#   bash web/packages/website/design/fonts/subset.sh
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../../../../.." && pwd)"
src="$repo/apps/apple/MaktabaKit/Sources/MaktabaKit/Resources/Fonts"
out="$repo/web/packages/website/static/fonts"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

LATIN="U+0020-007E,U+00A0-00FF,U+0131,U+0152-0153,U+02C6,U+02DA,U+02DC,U+2000-206F,U+20AC,U+2122,U+2190-2193,U+2212,U+FEFF,U+FFFD"
ARABIC="U+0600-06FF,U+200C-200F,U+2066-2069,U+FB50-FDFF,U+FE70-FEFF"

for pair in Regular:400 SemiBold:600; do
  style="${pair%%:*}"
  weight="${pair##*:}"
  for script in latin arabic; do
    if [ "$script" = latin ]; then range="$LATIN"; else range="$ARABIC"; fi
    uvx --from 'fonttools[woff]' pyftsubset "$src/IBMPlexSansArabic-$style.ttf" \
      --unicodes="$range" --layout-features='*' --no-hinting --flavor=woff2 \
      --output-file="$tmp/$style-$script.woff2"
    uvx --from 'fonttools[woff]' python "$here/rename.py" \
      "$tmp/$style-$script.woff2" "$out/maktaba-text-$weight-$script.woff2" "$style"
  done
done
cp "$src/OFL-IBMPlexSansArabic.txt" "$out/OFL.txt"

# The display face (part B): Amiri Bold, the apps' display face, for headlines and the
# wordmark's name. Latin in the same range as the text face; Arabic in the letters,
# marks, digits and punctuation Modern Standard Arabic uses (not the Persian and Urdu
# letters, nor the presentation forms: shaping reaches those glyphs through the
# features), so an Arabic page's display file stays near 39 KB. Amiri reserves no font
# name, but the files are renamed "Maktaba Display" like the text face, so the CSS
# family never matches a copy installed on the visitor's computer.
AMIRI="$src/Amiri-Bold.ttf"
DISPLAY_ARABIC="U+060C,U+061B,U+061F,U+0621-063A,U+0640-0652,U+0660-066C,U+0670,U+200C-200F,U+2066-2069,U+00AB,U+00BB,U+0020,U+00A0"
DISPLAY_FEATURES="init,medi,fina,isol,rlig,liga,calt,ccmp,locl,mark,mkmk,curs,kern"
for script in latin arabic; do
  if [ "$script" = latin ]; then range="$LATIN"; else range="$DISPLAY_ARABIC"; fi
  uvx --from 'fonttools[woff]' pyftsubset "$AMIRI" \
    --unicodes="$range" --layout-features="$DISPLAY_FEATURES" --no-hinting --flavor=woff2 \
    --output-file="$tmp/Amiri-$script.woff2"
  uvx --from 'fonttools[woff]' python "$here/rename.py" \
    "$tmp/Amiri-$script.woff2" "$out/maktaba-display-700-$script.woff2" Bold display
done
cp "$src/OFL-Amiri.txt" "$out/OFL-Amiri.txt"

#!/usr/bin/env bash
# Builds the libmpv the desktop Player carries (Epic 13 story 13.18) into OUT, for this system:
# Linux (x86_64, aarch64) or Windows x86_64 under MSYS2's UCRT64 shell.
#
#   libmpv/build.sh OUT
#
# LGPL throughout, so the Player (which loads it dynamically) keeps its own licence:
#   - FFmpeg 8.1 without --enable-gpl and --enable-nonfree, static, inside libmpv;
#   - dav1d (BSD-2-Clause, AV1) and libplacebo (LGPL-2.1+), static, inside libmpv;
#   - mpv with -Dgpl=false (LGPL-2.1+), its library only.
# libass, FreeType, FriBidi and HarfBuzz (ISC, FTL, LGPL-2.1+, MIT) and the system's audio
# libraries are the system's on Linux (the .deb and .rpm depend on them) and MSYS2's DLLs,
# copied beside libmpv-2.dll, on Windows. The build fails when FFmpeg or mpv is configured GPL
# or non-free, and when a copied DLL's package is GPL-only.
#
# OUT gets the library (libmpv.so.2 or libmpv-2.dll and its DLLs), licences/ (each part's
# licence text) and BUILD-INFO.txt (sources, pins, configure lines), which THIRD_PARTY_NOTICES
# points to.
set -euo pipefail

out="${1:?usage: build.sh OUT}"
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../../.." && pwd)"
# shellcheck source=pins.env
. "$here/pins.env"
work="${LIBMPV_WORK:-$root/build/libmpv-work}"
prefix="$work/prefix"
jobs="$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 4)"
case "$(uname -s)" in
  MINGW*|MSYS*) os=windows ;;
  Linux) os=linux ;;
  *) echo "::error::build.sh builds on Linux or MSYS2 (UCRT64), not $(uname -s)" >&2; exit 1 ;;
esac
mkdir -p "$out" "$out/licences" "$work/dl" "$work/src" "$prefix"
export PKG_CONFIG_PATH="$prefix/lib/pkgconfig${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
info="$out/BUILD-INFO.txt"
: >"$info"
note() { printf '%s\n' "$*" >>"$info"; }

fetch() { "$root/tools/release/fetch-pinned.sh" "$1" "$2" "$work/dl/$(basename "$1")"; }
unpack() { tar -xf "$1" -C "$work/src"; }

note "libmpv for the Maktaba Player (story 13.18), $os $(uname -m)"
note ""

# ---- dav1d ----------------------------------------------------------------------------------
unpack "$(fetch "$DAV1D_URL" "$DAV1D_SHA256")"
d="$work/src/dav1d-1.5.5"
meson setup "$work/b/dav1d" "$d" --prefix="$prefix" --libdir=lib --buildtype=release \
  --default-library=static -Denable_tools=false -Denable_tests=false
ninja -C "$work/b/dav1d" install
cp "$d/COPYING" "$out/licences/dav1d.txt"
note "dav1d 1.5.5: $DAV1D_URL (sha256 $DAV1D_SHA256), BSD-2-Clause"

# ---- FFmpeg ---------------------------------------------------------------------------------
unpack "$(fetch "$FFMPEG_URL" "$FFMPEG_SHA256")"
f="$work/src/ffmpeg-8.1.3"
ffconf=(--prefix="$prefix" --enable-static --disable-shared --enable-pic --disable-programs
  --disable-doc --disable-debug --disable-autodetect --enable-libdav1d --enable-zlib)
if [ "$os" = linux ]; then
  # VA-API decoders, their frames copied back for mpv's software renderer
  ffconf+=(--enable-vaapi --enable-pthreads)
else
  ffconf+=(--enable-d3d11va --enable-dxva2 --enable-w32threads)
fi
(cd "$f" && ./configure "${ffconf[@]}" && make -j"$jobs" && make install)
grep -q '#define CONFIG_GPL 0' "$f/config.h" || { echo "::error::FFmpeg is configured GPL" >&2; exit 1; }
grep -q '#define CONFIG_NONFREE 0' "$f/config.h" || { echo "::error::FFmpeg is configured non-free" >&2; exit 1; }
cp "$f/COPYING.LGPLv2.1" "$out/licences/ffmpeg.txt"
note "FFmpeg 8.1.3: $FFMPEG_URL (sha256 $FFMPEG_SHA256), LGPL-2.1-or-later"
note "  configure ${ffconf[*]}"

# ---- libplacebo -----------------------------------------------------------------------------
p="$work/src/libplacebo"
rm -rf "$p"
git clone --quiet --depth 1 --branch "$LIBPLACEBO_TAG" --recurse-submodules --shallow-submodules "$LIBPLACEBO_GIT" "$p"
[ "$(git -C "$p" rev-parse HEAD)" = "$LIBPLACEBO_COMMIT" ] || { echo "::error::libplacebo $LIBPLACEBO_TAG is not commit $LIBPLACEBO_COMMIT" >&2; exit 1; }
meson setup "$work/b/libplacebo" "$p" --prefix="$prefix" --libdir=lib --buildtype=release \
  --default-library=static -Dvulkan=disabled -Dopengl=disabled -Dd3d11=disabled \
  -Dglslang=disabled -Dshaderc=disabled -Dlcms=disabled -Dlibdovi=disabled -Dxxhash=disabled \
  -Dunwind=disabled -Ddemos=false -Dtests=false
ninja -C "$work/b/libplacebo" install
cp "$p/LICENSE" "$out/licences/libplacebo.txt"
note "libplacebo $LIBPLACEBO_TAG: $LIBPLACEBO_GIT at $LIBPLACEBO_COMMIT, LGPL-2.1-or-later"

# ---- mpv ------------------------------------------------------------------------------------
unpack "$(fetch "$MPV_URL" "$MPV_SHA256")"
m="$work/src/mpv-0.42.0"
mpvconf=(--prefix="$prefix" --libdir=lib --buildtype=release --default-library=shared
  -Dprefer_static=true -Dlibmpv=true -Dcplayer=false -Dgpl=false -Dtests=false
  -Dlua=disabled -Djavascript=disabled -Dlibarchive=disabled -Dlibbluray=disabled
  -Ddvdnav=disabled -Dcdda=disabled -Drubberband=disabled -Duchardet=disabled -Dzimg=disabled
  -Dvapoursynth=disabled -Dlcms2=disabled -Djpeg=disabled -Dmanpage-build=disabled
  -Dhtml-build=disabled -Dvulkan=disabled -Dgl=disabled -Dx11=disabled -Dwayland=disabled
  -Ddrm=disabled -Dvaapi=disabled -Dcuda-hwaccel=disabled -Dcuda-interop=disabled
  -Dsdl2-audio=disabled -Dsdl2-video=disabled -Dopenal=disabled)
if [ "$os" = linux ]; then
  mpvconf+=(-Dalsa=enabled -Dpulse=enabled)
else
  mpvconf+=(-Dwasapi=enabled -Dd3d11=disabled -Ddirect3d=disabled)
fi
meson setup "$work/b/mpv" "$m" "${mpvconf[@]}"
grep -qE '#define HAVE_GPL 0' "$work/b/mpv/config.h" || { echo "::error::mpv is configured GPL" >&2; exit 1; }
ninja -C "$work/b/mpv" install
cp "$m/LICENSE.LGPL" "$out/licences/mpv.txt"
note "mpv 0.42.0: $MPV_URL (sha256 $MPV_SHA256), LGPL-2.1-or-later"
note "  meson ${mpvconf[*]}"

# ---- the library and what it needs ------------------------------------------------------------
if [ "$os" = linux ]; then
  lib="$(readlink -f "$prefix/lib/libmpv.so.2")"
  cp "$lib" "$out/libmpv.so.2"
  strip --strip-unneeded "$out/libmpv.so.2"
  # nothing from the build's own prefix: FFmpeg, dav1d and libplacebo are inside it
  if readelf -d "$out/libmpv.so.2" | grep -E 'NEEDED.*(libav|libsw|dav1d|placebo)'; then
    echo "::error::libmpv.so.2 needs a library that should be inside it" >&2
    exit 1
  fi
  note ""
  note "needs (the system's): $(readelf -d "$out/libmpv.so.2" | sed -n 's/.*Shared library: \[\(.*\)\]/\1/p' | tr '\n' ' ')"
  cp "$root/THIRD_PARTY_NOTICES" "$out/licences/" 2>/dev/null || true
else
  cp "$prefix/bin/libmpv-2.dll" "$out/"
  # the DLLs it loads from MSYS2, each beside it; Windows' own stay Windows'
  for dll in $(ntldd -R "$out/libmpv-2.dll" | sed -n 's|.*=> \(/ucrt64/bin/[^ ]*\|[A-Za-z]:[^ ]*ucrt64[^ ]*\) .*|\1|p' | tr '\\' '/' | sort -u); do
    path="$(cygpath -u "$dll")"
    cp "$path" "$out/"
    owner="$(pacman -Qqo "$path")"
    lic="$(pacman -Qi "$owner" | sed -n 's/^Licenses *: //p')"
    case "$lic" in
      *GPL*) case "$lic" in *LGPL*|*"GPL-2.0-or-later WITH"*|*"FTL"*) ;; *) echo "::error::$(basename "$path") ($owner) is $lic" >&2; exit 1 ;; esac ;;
    esac
    note "$(basename "$path"): MSYS2 $owner ($lic)"
    for f in /ucrt64/share/licenses/"${owner#mingw-w64-ucrt-x86_64-}"/*; do
      [ -f "$f" ] && cp "$f" "$out/licences/${owner#mingw-w64-ucrt-x86_64-}-$(basename "$f").txt"
    done
  done
fi
echo "libmpv built into $out"
cat "$info"

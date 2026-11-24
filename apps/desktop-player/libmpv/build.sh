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
# FFmpeg, dav1d and libplacebo are static only (their archives inside libmpv): each .pc's
# private libraries join its Libs, so mpv links what they need without asking meson to prefer
# static libraries everywhere (the system's own static archives are not position-independent)
# (and Requires.private joins Requires: a static archive comes before those it needs)
for pc in "$prefix"/lib/pkgconfig/*.pc; do
  priv="$(sed -n 's/^Libs.private: *//p' "$pc")"
  reqp="$(sed -n 's/^Requires.private: *//p' "$pc")"
  req="$(sed -n 's/^Requires: *//p' "$pc")"
  sed -i.bak -e '/^Libs.private:/d' -e '/^Requires.private:/d' -e '/^Requires:/d' "$pc"
  all="${req}${req:+${reqp:+, }}${reqp}"
  [ -n "$all" ] && printf 'Requires: %s\n' "$all" >>"$pc"
  [ -n "$priv" ] && sed -i.bak -e "s|^Libs: \(.*\)|Libs: \1 $priv|" "$pc"
  rm -f "$pc.bak"
done
unpack "$(fetch "$MPV_URL" "$MPV_SHA256")"
m="$work/src/mpv-0.42.0"
mpvconf=(--prefix="$prefix" --libdir=lib --buildtype=release --default-library=shared
  -Dlibmpv=true -Dcplayer=false -Dgpl=false -Dtests=false
  -Dlua=disabled -Djavascript=disabled -Dlibarchive=disabled -Dlibbluray=disabled
  -Ddvdnav=disabled -Dcdda=disabled -Drubberband=disabled -Duchardet=disabled -Dzimg=disabled
  -Dvapoursynth=disabled -Dlcms2=disabled -Djpeg=disabled -Dmanpage-build=disabled
  -Dhtml-build=disabled -Dvulkan=disabled -Dgl=disabled -Dx11=disabled -Dwayland=disabled
  -Ddrm=disabled -Dvaapi=disabled -Dcuda-hwaccel=disabled -Dcuda-interop=disabled
  -Dsdl2-audio=disabled -Dsdl2-video=disabled -Dopenal=disabled
  # mpv 0.42's curl stream backend (on whenever curl is found): the Player streams through
  # FFmpeg's HTTP from its own local proxy, and curl would bring TLS, SSH and IDN with it
  -Dlibcurl=disabled)
if [ "$os" = linux ]; then
  # FFmpeg's, dav1d's and libplacebo's symbols stay inside libmpv: exported, they would meet
  # another FFmpeg in the same process (JavaFX's media plugin links the system's)
  mpvconf+=(-Dalsa=enabled -Dpulse=enabled "-Dc_link_args=-Wl,--exclude-libs,ALL")
else
  # D3D11VA decoding (copied back): mpv 0.42 builds it only beside its D3D11 output, which
  # needs shaderc and SPIRV-Cross (Apache-2.0, MSYS2's DLLs beside libmpv-2.dll)
  mpvconf+=(-Dwasapi=enabled -Dd3d11=enabled -Ddirect3d=disabled -Dd3d-hwaccel=enabled)
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
  # and exports only mpv's API, none of what is inside it
  leaked="$(nm -D --defined-only "$out/libmpv.so.2" | awk '{print $3}' | grep -E '^(av|avcodec|avformat|swr|sws|dav1d|pl)_' || true)"
  if [ -n "$leaked" ]; then
    echo "::error::libmpv.so.2 exports what is inside it: $(head -c 400 <<<"$leaked")" >&2
    exit 1
  fi
  note ""
  note "needs (the system's): $(readelf -d "$out/libmpv.so.2" | sed -n 's/.*Shared library: \[\(.*\)\]/\1/p' | tr '\n' ' ')"
  cp "$root/THIRD_PARTY_NOTICES" "$out/licences/" 2>/dev/null || true
else
  cp "$prefix/bin/libmpv-2.dll" "$out/"
  # the DLLs it loads from MSYS2 (ldd lists them all, with their own), each beside it;
  # Windows' own stay Windows'
  ldd "$out/libmpv-2.dll" | tee "$work/ldd.txt"
  dlls="$(awk '$3 ~ /^\/ucrt64\/bin\// {print $3}' "$work/ldd.txt" | sort -u)"
  [ -n "$dlls" ] || { echo "::error::ldd found none of MSYS2's DLLs libmpv-2.dll needs (libass at least)" >&2; exit 1; }
  refused=0
  for path in $dlls; do
    cp "$path" "$out/"
    owner="$(pacman -Qqo "$path")"
    lic="$(pacman -Qi "$owner" | sed -n 's/^Licenses *: //p')"
    # the library's own licence: MSYS2 labels the other parts' ("documentation:spdx:GPL-3.0-or-
    # later" for libiconv's manual), which the Player does not carry
    libl="$(sed -E 's/[a-z]+:spdx:[^ ]+//g' <<<"$lic")"
    # a GPL licence (not LGPL) is refused unless the package offers another (" OR "), carries a
    # linking exception (" WITH "), or the DLL is one whose own licence is known below (its
    # package's GPL covers programs the Player does not carry)
    why=""
    case "$(basename "$path")" in
      libintl-8.dll) why="gettext's libintl is LGPL-2.1-or-later; the package's GPL-3.0 is gettext's programs'" ;;
    esac
    if [ -z "$why" ] && grep -qE '(^|[^L])GPL' <<<"$libl" && ! grep -qE ' OR | WITH ' <<<"$libl"; then
      echo "::error::$(basename "$path") ($owner) is $lic" >&2
      refused=1
    fi
    note "$(basename "$path"): MSYS2 $owner ($lic)${why:+; $why}"
    for f in /ucrt64/share/licenses/"${owner#mingw-w64-ucrt-x86_64-}"/*; do
      if [ -f "$f" ]; then cp "$f" "$out/licences/${owner#mingw-w64-ucrt-x86_64-}-$(basename "$f").txt"; fi
    done
  done
  # every DLL looked at first, so one run names them all
  [ "$refused" = 0 ] || exit 1
fi
echo "libmpv built into $out"
cat "$info"

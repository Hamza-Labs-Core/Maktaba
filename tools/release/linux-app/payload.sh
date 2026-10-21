#!/usr/bin/env bash
# Builds the Linux server payload (Epic 39, story 39.2) for this machine's architecture
# (x86_64 or aarch64): the same tree Maktaba.app carries in Contents/Resources/server.
#
#   bin/       maktaba-server (web UI embedded) maktaba-api maktaba-streaming maktaba-launcher
#   postgres/  bin/ lib/ share/          PostgreSQL 16, with the libraries it links that a
#                                        minimal system can lack, all on an $ORIGIN rpath
#   ffmpeg/    ffmpeg ffprobe
#   python/    CPython 3.12              python-build-standalone, stdlib precompiled
#   pipeline/  maktaba_pipeline-<v>-py3-none-any.whl constraints.txt
#   licenses/  the third-party licences and where their sources are
#   files.sha256                         every file and symlink, with mode, size, SHA-256
#   payload.json                         what is inside: versions, checksums, min glibc
#
# Third-party pieces come from pins.env and are checked against its SHA-256s. The wheels
# the venv needs (torch, …) and the models are not here: the first run downloads them.
#
# It must run on the architecture it builds for, on a glibc system no newer than the
# payload's baseline (CI: ubuntu-22.04 and ubuntu-22.04-arm): the libraries copied in
# beside Postgres come from this machine.
#
# It also writes <build>/tauri.linux-app.conf.json, the overlay `tauri build --config`
# takes to bundle the payload into the AppImage / .deb / .rpm (story 39.10).
#
# Usage: payload.sh [--version VERSION] [--build DIR] [--cache DIR]
#   --version  release tag (default: git describe)
#   --build    work directory (default: build/linux-app); the payload is <build>/server
#   --cache    downloads (default: <build>/downloads, or $LINUX_APP_CACHE)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"

die() { printf 'payload: error: %s\n' "$*" >&2; exit 1; }
log() { printf '\033[1;36m[payload]\033[0m %s\n' "$*" >&2; }

VERSION="${VERSION:-}"
BUILD="$REPO_ROOT/build/linux-app"
CACHE="${LINUX_APP_CACHE:-}"
while [ $# -gt 0 ]; do
    case "$1" in
        --version) [ $# -ge 2 ] || die "--version needs a value"; VERSION="$2"; shift 2 ;;
        --build)   [ $# -ge 2 ] || die "--build needs a value"; BUILD="$2"; shift 2 ;;
        --cache)   [ $# -ge 2 ] || die "--cache needs a value"; CACHE="$2"; shift 2 ;;
        -h|--help) sed -n '2,/^set -e/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//'; exit 0 ;;
        *)         die "unknown option '$1'" ;;
    esac
done
[ "$(uname -s)" = Linux ] || die "the Linux payload is built on Linux (its libraries come from this machine)"
case "$(uname -m)" in
    x86_64)        ARCH=x86_64;  GOARCH=amd64 ;;
    aarch64|arm64) ARCH=aarch64; GOARCH=arm64 ;;
    *)             die "unsupported architecture $(uname -m) (x86_64 or aarch64)" ;;
esac
for t in go uv pnpm curl tar sha256sum patchelf; do
    command -v "$t" >/dev/null 2>&1 || die "$t is required"
done
[ -n "$VERSION" ] || VERSION="$(git -C "$REPO_ROOT" describe --tags --dirty --always 2>/dev/null || echo dev)"

# shellcheck source-path=SCRIPTDIR source=pins.env
. "$SCRIPT_DIR/pins.env"
pin() { local v="${1}_${ARCH^^}"; printf '%s' "${!v:?pins.env has no $v}"; }

mkdir -p "$BUILD"
BUILD="$(cd "$BUILD" && pwd)"
CACHE="${CACHE:-$BUILD/downloads}"
mkdir -p "$CACHE"
OUT="$BUILD/server"
WORK="$BUILD/work"
rm -rf "$OUT" "$WORK"
mkdir -p "$OUT"/{bin,postgres,ffmpeg,pipeline,licenses} "$WORK"

sha256() { sha256sum "$1" | awk '{print $1}'; }

# fetch URL SHA256 NAME: the file in the cache, downloaded once, always checked.
fetch() {
    local url="$1" want="$2" dst="$CACHE/$3" got
    if [ ! -f "$dst" ] || [ "$(sha256 "$dst")" != "$want" ]; then
        log "downloading $3"
        curl -fsSL --retry 3 -o "$dst.part" "$url"
        mv "$dst.part" "$dst"
    fi
    got="$(sha256 "$dst")"
    [ "$got" = "$want" ] || die "$3: sha256 $got, pins.env says $want"
    printf '%s\n' "$dst"
}


# --- Go binaries (the release envelope: -trimpath, stamped, web UI embedded) -----------------
log "building the Go binaries for linux/$GOARCH ($VERSION)"
make -C "$REPO_ROOT" --no-print-directory server build-go \
    GOOS=linux GOARCH="$GOARCH" VERSION="$VERSION" BIN_SUBDIR="linux-$GOARCH/" >/dev/null
cp "$REPO_ROOT/cmd/maktaba-server/bin/linux-$GOARCH/maktaba-server" \
   "$REPO_ROOT/api/bin/linux-$GOARCH/maktaba-api" \
   "$REPO_ROOT/streaming/bin/linux-$GOARCH/maktaba-streaming" "$OUT/bin/"
(cd "$REPO_ROOT/cmd/maktaba-launcher" && \
    GOOS=linux GOARCH="$GOARCH" CGO_ENABLED=0 \
    go build -trimpath -ldflags "-buildid= -s -w -X main.Version=$VERSION" -o "$OUT/bin/maktaba-launcher" .)

# --- PostgreSQL ----------------------------------------------------------------------------------
log "PostgreSQL $POSTGRES_VERSION"
pg_tgz="$(fetch "$(pin POSTGRES_URL)" "$(pin POSTGRES_SHA256)" "postgresql-$POSTGRES_VERSION-$ARCH.tar.gz")"
tar -xzf "$pg_tgz" -C "$WORK"
pg="$WORK/postgresql-$POSTGRES_VERSION-$ARCH-unknown-linux-gnu"
[ -x "$pg/bin/postgres" ] || die "unexpected PostgreSQL archive layout"
mkdir -p "$OUT/postgres/bin"
# what the supervisor runs (36.1) and what a backup, a restore, an upgrade, a node's copy
# (pg_basebackup, Epic 37) or a person needs
for b in postgres initdb pg_ctl pg_dump pg_dumpall pg_restore psql pg_isready pg_upgrade pg_controldata pg_resetwal pg_basebackup; do
    cp "$pg/bin/$b" "$OUT/postgres/bin/"
done
# libraries (libpq, the extensions) and share/ (catalogs, extensions, time zones), without
# static libraries and build files, and without the modules that link what no supported
# system is sure to have and Maktaba never loads: PL/Python (the build machine's
# libpython3.11), xml2 (libxslt) and uuid-ossp (libossp-uuid). Their extension files go too,
# so they are not offered. Maktaba creates pg_trgm, pgcrypto and unaccent.
(cd "$pg" && tar -cf - --exclude='*.a' --exclude=pgxs --exclude=pkgconfig \
    --exclude='*plpython3*' --exclude='pgxml.so' --exclude='xml2--*' --exclude='xml2.control' \
    --exclude='uuid-ossp*' lib share) | (cd "$OUT/postgres" && tar -xf -)
cp "$pg/COPYRIGHT" "$OUT/licenses/postgresql.txt"
cp "$pg/LICENSE" "$OUT/licenses/postgresql-binaries.txt"

# --- Python (first: the payload's own tools run with it) ------------------------------------
log "Python $PYTHON_VERSION"
tar -xzf "$(fetch "$(pin PYTHON_URL)" "$(pin PYTHON_SHA256)" "cpython-$PYTHON_VERSION-$ARCH.tar.gz")" -C "$WORK"
py="$WORK/python"
[ -x "$py/bin/python3" ] || die "unexpected python-build-standalone layout"
# Tk, IDLE and the Tcl runtime are never used by the pipeline (~10 MB).
rm -rf "$py"/lib/tcl* "$py"/lib/tk* "$py"/lib/itcl* "$py"/lib/thread* "$py"/lib/libtcl* "$py"/lib/libtk* \
       "$py"/lib/python3.12/idlelib "$py"/lib/python3.12/tkinter "$py"/lib/python3.12/turtledemo \
       "$py"/lib/python3.12/lib-dynload/_tkinter.* "$py"/bin/idle3*
# Precompile the standard library with hashes that are never re-checked: the payload is
# read-only inside the AppImage and the package, and its runtime copy (39.3) is checked
# against files.sha256, so nothing writes __pycache__ into it later. -f: the archive's own
# .pyc files are timestamp-based, and a copy's new mtimes would make Python rewrite them.
"$py/bin/python3" -I -m compileall -q -f -j 0 --invalidation-mode unchecked-hash "$py/lib/python3.12" >/dev/null || true
cp "$py/lib/python3.12/LICENSE.txt" "$OUT/licenses/python.txt"
mv "$py" "$OUT/python"
PY="$OUT/python/bin/python3"
tool() { "$PY" -I "$SCRIPT_DIR/payload_tool.py" "$@"; }
# unzip_to ZIP DIR: Python's zipfile (a build machine may have no unzip); the executables'
# modes are set by the caller.
unzip_to() { "$PY" -I -c 'import sys, zipfile; zipfile.ZipFile(sys.argv[1]).extractall(sys.argv[2])' "$1" "$2"; }

# --- Postgres's libraries ----------------------------------------------------------------------
# The theseus-rs build links the system's OpenSSL 3, libxml2, lz4, zstd and krb5 (and what
# those link: ICU, ...). A minimal Debian has not all of them, so they are copied in from
# this machine. Every library in postgres/lib loads its neighbours through $ORIGIN (the
# archive's own modules carry the build machine's /opt/postgresql/lib).
log "Postgres's libraries from this machine"
mapfile -t elves < <(find "$OUT/postgres/bin" "$OUT/postgres/lib" -type f -exec sh -c \
    'for f; do head -c 4 "$f" | grep -q ELF && printf "%s\n" "$f"; done' sh {} +)
needs="$(tool closure "$OUT/postgres" "${elves[@]}")" || die "Postgres needs a library this machine does not have"
closure=()
while read -r soname path; do
    [ -n "$soname" ] || continue
    cp -L "$path" "$OUT/postgres/lib/$soname"
    chmod 0644 "$OUT/postgres/lib/$soname"
    closure+=("$soname")
done <<<"$needs"
for f in "$OUT"/postgres/lib/*.so*; do
    [ -L "$f" ] && continue
    head -c 4 "$f" | grep -q ELF || continue
    # shellcheck disable=SC2016 # $ORIGIN is the dynamic loader's, not the shell's
    patchelf --set-rpath '$ORIGIN' "$f"
done
log "copied ${#closure[@]} libraries: ${closure[*]:-none}"
cat >"$OUT/licenses/postgresql-libraries.txt" <<EOF
Libraries copied in beside PostgreSQL from the build machine ($(sed -n 's/^PRETTY_NAME=//p' /etc/os-release | tr -d '"'), $ARCH),
each under its own licence (OpenSSL: Apache-2.0; libxml2, lz4, zstd, zlib: MIT/BSD-style;
MIT Kerberos: MIT; ICU: Unicode). Their sources are the distribution's source packages of the
same versions.

$(for s in "${closure[@]}"; do printf '%s\n' "$s"; done)
EOF

# --- ffmpeg --------------------------------------------------------------------------------------
log "ffmpeg $FFMPEG_VERSION"
unzip_to "$(fetch "$(pin FFMPEG_URL)" "$(pin FFMPEG_SHA256)" "ffmpeg-$FFMPEG_VERSION-$ARCH.zip")" "$OUT/ffmpeg"
unzip_to "$(fetch "$(pin FFPROBE_URL)" "$(pin FFPROBE_SHA256)" "ffprobe-$FFMPEG_VERSION-$ARCH.zip")" "$OUT/ffmpeg"
chmod 0755 "$OUT/ffmpeg/ffmpeg" "$OUT/ffmpeg/ffprobe"
cat >"$OUT/licenses/ffmpeg.txt" <<EOF
ffmpeg and ffprobe $FFMPEG_VERSION, built by Martin Riedl (https://ffmpeg.martin-riedl.de),
configured with --enable-gpl --enable-version3: licensed under the GNU General Public
License version 3 (https://www.gnu.org/licenses/gpl-3.0.html).

Binaries: $(pin FFMPEG_URL)
          $(pin FFPROBE_URL)
Source:   https://ffmpeg.org/releases/ffmpeg-$FFMPEG_VERSION.tar.xz
Build scripts and the versions of every library linked in:
          https://git.martin-riedl.de/ffmpeg/build-script
          $(dirname "$(pin FFMPEG_URL)")/versions.txt
EOF

# --- the pipeline: the same wheel and constraints a node release ships ---------------------------
log "pipeline wheel"
# a release tag (v1.2.3, v1.2.3-rc.1) is PEP 440 after the "v"; anything else (a CI run, a
# local `git describe`) is a dev version
if [[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-(rc|alpha|beta|a|b)\.?[0-9]+)?$ ]]; then
    pyver="$VERSION"
else
    pyver="0.0.0.dev0"
fi
"$REPO_ROOT/deploy/packaging/node/build-release.sh" --version "$pyver" --out "$WORK/node" >/dev/null
whl=("$WORK"/node/maktaba_pipeline-*-py3-none-any.whl)
cons=("$WORK"/node/maktaba-pipeline-*-constraints.txt)
if [ "${#whl[@]}" -ne 1 ] || [ ! -f "${whl[0]}" ]; then die "expected one pipeline wheel"; fi
cp "${whl[0]}" "$OUT/pipeline/"
cp "${cons[0]}" "$OUT/pipeline/constraints.txt"

# --- the files and payload.json ------------------------------------------------------------------
log "checking every ELF file against the glibc $MAX_GLIBC baseline"
elf="$(tool elf "$OUT" --max-glibc "$MAX_GLIBC")" || { printf '%s\n' "$elf" >&2; die "the payload needs more than its baseline"; }
min_glibc="$("$PY" -I -c 'import json,sys; print(json.loads(sys.argv[1].splitlines()[-1])["min_glibc"])' "$elf")"
rm -rf "$WORK"
manifest="$(tool manifest "$OUT")"
WHEEL_NAME="$(basename "${whl[0]}")"
# every value goes in through the environment, never into the program's text
P_VERSION="$VERSION" P_PLATFORM="linux-$ARCH" P_MIN_GLIBC="$min_glibc" P_MANIFEST="$manifest" \
P_PG_VERSION="$POSTGRES_VERSION" P_PG_URL="$(pin POSTGRES_URL)" P_PG_SHA="$(pin POSTGRES_SHA256)" \
P_FF_VERSION="$FFMPEG_VERSION" P_FF_URL="$(pin FFMPEG_URL)" P_FF_SHA="$(pin FFMPEG_SHA256)" \
P_FP_URL="$(pin FFPROBE_URL)" P_FP_SHA="$(pin FFPROBE_SHA256)" \
P_PY_VERSION="$PYTHON_VERSION" P_PY_URL="$(pin PYTHON_URL)" P_PY_SHA="$(pin PYTHON_SHA256)" \
P_WHEEL="$WHEEL_NAME" P_WHEEL_SHA="$(sha256 "$OUT/pipeline/$WHEEL_NAME")" \
P_CONS_SHA="$(sha256 "$OUT/pipeline/constraints.txt")" \
"$PY" -I - "$OUT/payload.json" "${closure[@]}" <<'EOF'
import json, os, sys
e = os.environ
doc = {
    "schema": 1,
    "version": e["P_VERSION"],
    "platform": e["P_PLATFORM"],
    "min_glibc": e["P_MIN_GLIBC"],
    "postgres": {"version": e["P_PG_VERSION"], "source": e["P_PG_URL"], "sha256": e["P_PG_SHA"],
                 "libraries": sys.argv[2:]},
    "ffmpeg": {"version": e["P_FF_VERSION"], "source": e["P_FF_URL"], "sha256": e["P_FF_SHA"],
               "ffprobe_source": e["P_FP_URL"], "ffprobe_sha256": e["P_FP_SHA"]},
    "python": {"version": e["P_PY_VERSION"], "source": e["P_PY_URL"], "sha256": e["P_PY_SHA"]},
    "pipeline": {"wheel": e["P_WHEEL"], "sha256": e["P_WHEEL_SHA"], "constraints_sha256": e["P_CONS_SHA"]},
    "files": {"manifest": "files.sha256", **json.loads(e["P_MANIFEST"])},
}
with open(sys.argv[1], "w", encoding="utf-8") as f:
    json.dump(doc, f, indent=2)
    f.write("\n")
EOF

# --- the tauri config overlay (39.10 bundles it) -------------------------------------------------
# The payload goes to <prefix>/share/maktaba/server in every Linux bundle, not among the app's
# resources in <prefix>/lib/Maktaba: linuxdeploy (the AppImage) rewrites the rpath of, and
# strips, the ELF files it finds under usr/lib, which would no longer match files.sha256. The
# runtime copy (39.3) checks every file, and makes again the links the .rpm copies as files.
# web/dist is built above (make server), so tauri does not rebuild it.
cat >"$BUILD/tauri.linux-app.conf.json" <<EOF
{
  "build": {"beforeBuildCommand": ""},
  "bundle": {
    "linux": {
      "appimage": {"files": {"/usr/share/maktaba/server": "$OUT"}},
      "deb": {"files": {"/usr/share/maktaba/server": "$OUT"}},
      "rpm": {"files": {"/usr/share/maktaba/server": "$OUT"}}
    }
  }
}
EOF
log "payload: $OUT ($(du -sh "$OUT" | awk '{print $1}'), glibc $min_glibc, $(printf '%s' "$manifest" | "$PY" -I -c 'import json,sys; print(json.load(sys.stdin)["count"])') entries)"

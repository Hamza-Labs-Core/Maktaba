#!/usr/bin/env bash
# Builds the server payload the Windows app carries in <install>\server (Epic 38, story
# 38.11), for Windows x86_64:
#
#   bin\       maktaba-server.exe (web UI embedded) maktaba-api.exe maktaba-streaming.exe
#              maktaba-launcher.exe
#   postgres\  bin\ lib\ share\           PostgreSQL 16: the programs, the DLLs they load and
#                                         the Visual C++ runtime beside postgres.exe
#   ffmpeg\    ffmpeg.exe ffprobe.exe     static
#   python\    CPython 3.12               python-build-standalone, stdlib precompiled
#   pipeline\  maktaba_pipeline-<v>-py3-none-any.whl constraints.txt
#   migrations\                          the database's schema (shared/db/migrations):
#                                         maktaba-api finds them beside its bin\
#   licenses\  the third-party licences and where their sources are
#   payload.json                          what is inside, with versions and checksums
#
# Third-party pieces come from pins.env and are checked against its SHA-256s. The Visual
# C++ runtime comes from the build machine's Visual Studio (its redistributable folder,
# Microsoft's app-local deployment) and is checked to be signed by Microsoft. The CUDA /
# torch wheels and the models are not here: the app downloads them on first run (38.7).
#
# Nothing is signed: Authenticode signing is the owner's decision (README.md, Signing).
#
# It also writes <build>/tauri.win-app.conf.json, the config overlay `tauri build --config`
# takes to bundle the payload as the NSIS installer's server\, with the installer hooks.
#
# Runs under Git Bash (Git for Windows), on a PC and on windows-latest.
#
# Usage: payload.sh [--version VERSION] [--build DIR] [--cache DIR] [--msvc-runtime DIR]
#   --version       release tag (default: git describe)
#   --build         work directory (default: build/win-app); the payload is <build>/server
#   --cache         downloads (default: <build>/downloads, or $WIN_APP_CACHE)
#   --msvc-runtime  the Visual C++ runtime folder (…\VC\Redist\MSVC\<v>\x64\Microsoft.VC14x.CRT;
#                   default: $WIN_APP_MSVC_RUNTIME, else the newest Visual Studio's, by vswhere)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"

die() { printf 'payload: error: %s\n' "$*" >&2; exit 1; }
log() { printf '\033[1;36m[payload]\033[0m %s\n' "$*" >&2; }

VERSION="${VERSION:-}"
BUILD="$REPO_ROOT/build/win-app"
CACHE="${WIN_APP_CACHE:-}"
MSVC_RUNTIME="${WIN_APP_MSVC_RUNTIME:-}"
while [ $# -gt 0 ]; do
    case "$1" in
        --version)      [ $# -ge 2 ] || die "--version needs a value"; VERSION="$2"; shift 2 ;;
        --build)        [ $# -ge 2 ] || die "--build needs a value"; BUILD="$2"; shift 2 ;;
        --cache)        [ $# -ge 2 ] || die "--cache needs a value"; CACHE="$2"; shift 2 ;;
        --msvc-runtime) [ $# -ge 2 ] || die "--msvc-runtime needs a value"; MSVC_RUNTIME="$2"; shift 2 ;;
        -h|--help) sed -n '2,/^set -e/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//'; exit 0 ;;
        *)         die "unknown option '$1'" ;;
    esac
done
case "$(uname -s)" in
    MINGW*|MSYS*) ;;
    *) die "the Windows app payload is built on Windows, in Git Bash (cygpath, PowerShell, the Visual C++ runtime)" ;;
esac
[ "$(uname -m)" = x86_64 ] || die "the Windows app payload is x86_64; this is $(uname -m)"
for t in go uv pnpm curl unzip tar sha256sum cygpath powershell.exe; do
    command -v "$t" >/dev/null 2>&1 || die "$t is required"
done
# The Makefile builds the Go binaries. Git for Windows has no make: windows-latest has
# MinGW's (as make), a PC may have another GNU make (Strawberry Perl's gmake).
MAKE="${MAKE:-}"
if [ -z "$MAKE" ]; then
    for m in make gmake mingw32-make; do
        if command -v "$m" >/dev/null 2>&1; then MAKE="$m"; break; fi
    done
fi
[ -n "$MAKE" ] || die "GNU make is required (make, gmake or mingw32-make)"
[ -n "$VERSION" ] || VERSION="$(git -C "$REPO_ROOT" describe --tags --dirty --always 2>/dev/null || echo dev)"

# shellcheck source-path=SCRIPTDIR source=pins.env
. "$SCRIPT_DIR/pins.env"

mkdir -p "$BUILD"
BUILD="$(cd "$BUILD" && pwd)"
CACHE="${CACHE:-$BUILD/downloads}"
mkdir -p "$CACHE"
OUT="$BUILD/server"
WORK="$BUILD/work"
rm -rf "$OUT" "$WORK"
mkdir -p "$OUT"/{bin,postgres/bin,ffmpeg,pipeline,licenses} "$WORK"

sha256() { sha256sum "$1" | awk '{print $1}'; }
# win PATH...: the Windows form of each path, for the native programs given a path
win() { cygpath -w "$@"; }

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
log "building the Go binaries for windows/amd64 ($VERSION)"
"$MAKE" -C "$REPO_ROOT" --no-print-directory server build-go \
    GOOS=windows GOARCH=amd64 VERSION="$VERSION" BIN_SUBDIR=windows-amd64/ >/dev/null
# `go build -o` writes the name it is given, and the Makefile's have no .exe
cp "$REPO_ROOT/cmd/maktaba-server/bin/windows-amd64/maktaba-server" "$OUT/bin/maktaba-server.exe"
cp "$REPO_ROOT/api/bin/windows-amd64/maktaba-api" "$OUT/bin/maktaba-api.exe"
cp "$REPO_ROOT/streaming/bin/windows-amd64/maktaba-streaming" "$OUT/bin/maktaba-streaming.exe"
(cd "$REPO_ROOT/cmd/maktaba-launcher" && \
    GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
    go build -trimpath -ldflags "-buildid= -s -w -X main.Version=$VERSION" -o "$(win "$OUT/bin/maktaba-launcher.exe")" .)

# --- Python (first: payload_tool.py runs on it) -----------------------------------------------
log "Python $PYTHON_VERSION"
tar -xzf "$(fetch "$PYTHON_URL" "$PYTHON_SHA256" "cpython-$PYTHON_VERSION.tar.gz")" -C "$WORK"
py="$WORK/python"
[ -f "$py/python.exe" ] || die "unexpected python-build-standalone layout"
# Tk, IDLE and the Tcl runtime are never used by the pipeline (~12 MB).
rm -rf "$py/tcl" "$py/DLLs/_tkinter.pyd" "$py/DLLs/tcl86t.dll" "$py/DLLs/tk86t.dll" \
       "$py/Lib/idlelib" "$py/Lib/tkinter" "$py/Lib/turtledemo"
# Precompile the standard library with hashes that are never re-checked, replacing the
# timestamp-checked .pyc files the archive has for part of it: Python then never writes
# __pycache__ into the install, which the uninstaller (it removes the files it installed)
# would leave behind. Files that do not compile are test data nothing imports.
find "$py/Lib" -name __pycache__ -type d -prune -exec rm -rf {} +
"$py/python.exe" -I -m compileall -q -f -j 0 --invalidation-mode unchecked-hash "$(win "$py/Lib")" >/dev/null || true
cp "$py/LICENSE.txt" "$OUT/licenses/python.txt"
mv "$py" "$OUT/python"
PY="$OUT/python/python.exe"
# tool ARGS...: payload_tool.py on the payload's Python (its output has CRLF line ends)
tool() { "$PY" -I "$(win "$SCRIPT_DIR/payload_tool.py")" "$@"; }

# --- the Visual C++ runtime, for Postgres ---------------------------------------------------
# postgres.exe and its DLLs (ICU, OpenSSL, libxml2) are built with Visual C++ and load its
# runtime (vcruntime140.dll, msvcp140.dll) from beside postgres.exe, else from System32,
# which a PC without the Visual C++ Redistributable does not have. python-build-standalone
# carries vcruntime140.dll but not msvcp140.dll (ICU's), and the set must come from one
# release, so it is taken from Visual Studio's redistributable folder.
if [ -z "$MSVC_RUNTIME" ]; then
    vswhere="$(cygpath -u -F 42)/Microsoft Visual Studio/Installer/vswhere.exe"   # 42: Program Files (x86)
    [ -f "$vswhere" ] || die "no Visual Studio (vswhere.exe): install its C++ build tools, or pass --msvc-runtime"
    vs="$("$vswhere" -latest -products '*' -requires Microsoft.VisualStudio.Component.VC.Redist.14.Latest \
        -property installationPath | tr -d '\r')"
    [ -n "$vs" ] || die "no Visual Studio with the C++ redistributable (Microsoft.VisualStudio.Component.VC.Redist.14.Latest)"
    vs="$(cygpath -u "$vs")"
    redist="$(tr -d '[:space:]' <"$vs/VC/Auxiliary/Build/Microsoft.VCRedistVersion.default.txt")"
    crt=("$vs/VC/Redist/MSVC/$redist/x64"/Microsoft.VC*.CRT)
    [ -d "${crt[0]}" ] || die "no x64 Visual C++ runtime in $vs/VC/Redist/MSVC/$redist"
    MSVC_RUNTIME="${crt[0]}"
fi
MSVC_RUNTIME="$(cygpath -u "$MSVC_RUNTIME")"
[ -f "$MSVC_RUNTIME/vcruntime140.dll" ] || die "$MSVC_RUNTIME has no vcruntime140.dll"

# --- PostgreSQL ----------------------------------------------------------------------------------
log "PostgreSQL $POSTGRES_VERSION"
unzip -q "$(fetch "$POSTGRES_URL" "$POSTGRES_SHA256" "postgresql-$POSTGRES_VERSION.zip")" -d "$WORK"
pg="$WORK/postgresql-$POSTGRES_VERSION-x86_64-pc-windows-msvc"
[ -f "$pg/bin/postgres.exe" ] || die "unexpected PostgreSQL archive layout"
# what the supervisor runs (36.1, 38.10) and what a backup, a restore, an upgrade, a copy
# (Epic 37) or a person needs; not StackBuilder, the other client tools (createdb,
# vacuumdb, ...) or the test programs
for b in postgres initdb pg_ctl pg_dump pg_dumpall pg_restore psql pg_isready pg_upgrade pg_controldata pg_resetwal pg_basebackup; do
    cp "$pg/bin/$b.exe" "$OUT/postgres/bin/"
done
# the extensions and share\ (catalogs, extension scripts, time zones, messages), without the
# import libraries and build files
(cd "$pg" && tar -cf - --exclude='*.lib' --exclude=pkgconfig --exclude=pgxs lib share) | (cd "$OUT/postgres" && tar -xf -)
# the procedural languages whose runtime (Perl, a Python of its own, Tcl) is not here never load
rm -f "$OUT"/postgres/lib/plperl.dll "$OUT"/postgres/lib/plpython3.dll "$OUT"/postgres/lib/pltcl.dll \
      "$OUT"/postgres/lib/*_plperl.dll "$OUT"/postgres/lib/*_plpython3.dll
# the DLLs the programs and the extensions load, found by reading their import tables:
# OpenSSL, ICU, libxml2, zlib, ... from the archive's bin (not wxWidgets, StackBuilder's), and
# the Visual C++ runtime
mapfile -t pg_files < <(win "$OUT"/postgres/bin/*.exe "$OUT"/postgres/lib/*.dll)
tool closure --dir "$(win "$pg/bin")" --into "$(win "$OUT/postgres/bin")" --runtime "$(win "$MSVC_RUNTIME")" \
    "${pg_files[@]}" >"$WORK/postgres-dlls.txt" ||
    die "PostgreSQL needs a DLL that neither its archive nor Windows has (above)"
pg_dlls=()
while IFS=$'\r' read -r f; do
    [ -n "$f" ] || continue
    cp "$(cygpath -u "$f")" "$OUT/postgres/bin/"
    pg_dlls+=("$(basename "$(cygpath -u "$f")")")
done <"$WORK/postgres-dlls.txt"
# the runtime's DLLs are Microsoft's, signed (not a pinned download: Visual Studio's own files)
msvc_dlls=() msvc_names=()
for d in "${pg_dlls[@]}"; do
    if [ -f "$MSVC_RUNTIME/$d" ]; then msvc_dlls+=("$OUT/postgres/bin/$d"); msvc_names+=("$d"); fi
done
[ "${#msvc_dlls[@]}" -gt 0 ] || die "PostgreSQL loads no Visual C++ runtime DLL (unexpected archive)"
# (PowerShell's own $ variables, in single quotes; the files go in through the environment)
# shellcheck disable=SC2016
sigs="$(SIGN_FILES="$(win "${msvc_dlls[@]}" | tr '\n' ';')" powershell.exe -NoProfile -NonInteractive -Command \
    '$env:SIGN_FILES.TrimEnd(";") -split ";" | ForEach-Object { $s = Get-AuthenticodeSignature -LiteralPath $_; "{0}|{1}|{2}|{3}" -f $s.Status, $s.SignerCertificate.Subject, (Get-Item -LiteralPath $_).VersionInfo.FileVersion, (Split-Path -Leaf $_) }' | tr -d '\r')"
MSVC_VERSION=""
while IFS='|' read -r status subject version name; do
    if [ "$status" != Valid ] || [[ "$subject" != *"O=Microsoft Corporation"* ]]; then
        die "$name from $MSVC_RUNTIME is not signed by Microsoft ($status, $subject)"
    fi
    if [ "$name" = vcruntime140.dll ]; then MSVC_VERSION="$version"; fi
done <<<"$sigs"
[ -n "$MSVC_VERSION" ] || die "no vcruntime140.dll among the runtime's DLLs Postgres loads"
cp "$pg/server_license.txt" "$OUT/licenses/postgresql.txt"
cp "$pg/commandlinetools_3rd_party_licenses.txt" "$OUT/licenses/postgresql-third-party.txt"
cp "$pg/LICENSE" "$OUT/licenses/postgresql-binaries.txt"
cat >"$OUT/licenses/msvc-runtime.txt" <<EOF
The Microsoft Visual C++ runtime $MSVC_VERSION beside postgres.exe (${msvc_names[*]}),
from Visual Studio's redistributable folder ($(basename "$MSVC_RUNTIME")), deployed app-local as
Microsoft documents (https://learn.microsoft.com/cpp/windows/determining-which-dlls-to-redistribute):
Distributable Code under the Visual Studio licence terms
(https://visualstudio.microsoft.com/license-terms/). Signed by Microsoft Corporation.
EOF

# --- ffmpeg --------------------------------------------------------------------------------------
log "ffmpeg $FFMPEG_VERSION"
unzip -q "$(fetch "$FFMPEG_URL" "$FFMPEG_SHA256" "ffmpeg-$FFMPEG_VERSION.zip")" \
    '*/bin/ffmpeg.exe' '*/bin/ffprobe.exe' '*/LICENSE' '*/README.txt' -d "$WORK/ffmpeg"
ff=("$WORK"/ffmpeg/ffmpeg-"$FFMPEG_VERSION"-*)
if [ ! -f "${ff[0]}/bin/ffmpeg.exe" ] || [ ! -f "${ff[0]}/bin/ffprobe.exe" ]; then die "unexpected ffmpeg archive layout"; fi
cp "${ff[0]}/bin/ffmpeg.exe" "${ff[0]}/bin/ffprobe.exe" "$OUT/ffmpeg/"
cp "${ff[0]}/LICENSE" "$OUT/licenses/ffmpeg-gpl-3.0.txt"
# the build's configuration and the version of every library linked in
cp "${ff[0]}/README.txt" "$OUT/licenses/ffmpeg-build.txt"
source_commit="$(sed -n 's/^Source Code: *//p' "${ff[0]}/README.txt" | tr -d '\r' | head -1)"
cat >"$OUT/licenses/ffmpeg.txt" <<EOF
ffmpeg and ffprobe $FFMPEG_VERSION, built by Gyan Doshi (https://www.gyan.dev/ffmpeg/builds/),
configured with --enable-gpl --enable-version3: licensed under the GNU General Public
License version 3 (ffmpeg-gpl-3.0.txt, https://www.gnu.org/licenses/gpl-3.0.html).

Binaries: $FFMPEG_URL
Source:   https://ffmpeg.org/releases/ffmpeg-$FFMPEG_VERSION.tar.xz
          ${source_commit:-https://github.com/FFmpeg/FFmpeg/tree/n$FFMPEG_VERSION} (what this build was made from)
The build's configuration and the versions of every library linked in: ffmpeg-build.txt
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

# --- the database's migrations: setup's finish and the api's auto-migrate run in the user's
# data (%LOCALAPPDATA%\Maktaba), never a checkout, so the payload carries them (maktaba-api:
# beside its bin\) -------------------------------------------------------------------------------
log "database migrations"
cp -R "$REPO_ROOT/shared/db/migrations" "$OUT/migrations"

# --- payload.json --------------------------------------------------------------------------------
rm -rf "$WORK"
WHEEL_NAME="$(basename "${whl[0]}")"
# every value goes in through the environment, never into the program's text
P_VERSION="$VERSION" \
P_PG_VERSION="$POSTGRES_VERSION" P_PG_URL="$POSTGRES_URL" P_PG_SHA="$POSTGRES_SHA256" \
P_PG_DLLS="$(printf '%s\n' "${pg_dlls[@]}")" P_MSVC_VERSION="$MSVC_VERSION" \
P_FF_VERSION="$FFMPEG_VERSION" P_FF_URL="$FFMPEG_URL" P_FF_SHA="$FFMPEG_SHA256" \
P_PY_VERSION="$PYTHON_VERSION" P_PY_URL="$PYTHON_URL" P_PY_SHA="$PYTHON_SHA256" \
P_WHEEL="$WHEEL_NAME" P_WHEEL_SHA="$(sha256 "$OUT/pipeline/$WHEEL_NAME")" \
P_CONS_SHA="$(sha256 "$OUT/pipeline/constraints.txt")" \
"$PY" -I - "$(win "$OUT/payload.json")" <<'EOF'
import json, os, sys
e = os.environ
doc = {
    "schema": 1,
    "version": e["P_VERSION"],
    "platform": "windows-x86_64",
    "postgres": {"version": e["P_PG_VERSION"], "source": e["P_PG_URL"], "sha256": e["P_PG_SHA"],
                 "libraries": sorted(e["P_PG_DLLS"].split())},
    "msvc_runtime": {"version": e["P_MSVC_VERSION"], "source": "Visual Studio redistributable"},
    # one archive holds both programs (the Mac's has two: the same fields, for the supervisor)
    "ffmpeg": {"version": e["P_FF_VERSION"], "source": e["P_FF_URL"], "sha256": e["P_FF_SHA"],
               "ffprobe_source": e["P_FF_URL"], "ffprobe_sha256": e["P_FF_SHA"]},
    "python": {"version": e["P_PY_VERSION"], "source": e["P_PY_URL"], "sha256": e["P_PY_SHA"]},
    "pipeline": {"wheel": e["P_WHEEL"], "sha256": e["P_WHEEL_SHA"], "constraints_sha256": e["P_CONS_SHA"]},
}
with open(sys.argv[1], "w", encoding="utf-8", newline="\n") as f:
    json.dump(doc, f, indent=2)
    f.write("\n")
EOF

# --- the tauri config overlay --------------------------------------------------------------------
# The NSIS installer (current user: %LOCALAPPDATA%\Maktaba, no administrator) puts the payload
# in <install>\server and runs the hooks around it. Paths in Windows form with "/", which
# tauri and NSIS take. web/dist is built above (make server), so tauri does not rebuild it.
HOOKS="$REPO_ROOT/apps/desktop/src-tauri/windows/installer-hooks.nsh"
[ -f "$HOOKS" ] || die "no installer hooks at ${HOOKS#"$REPO_ROOT"/}"
cat >"$BUILD/tauri.win-app.conf.json" <<EOF
{
  "build": {"beforeBuildCommand": ""},
  "bundle": {
    "targets": ["nsis"],
    "resources": {"$(cygpath -m "$OUT")/": "server/"},
    "windows": {
      "nsis": {
        "installMode": "currentUser",
        "installerHooks": "$(cygpath -m "$HOOKS")"
      }
    }
  }
}
EOF
log "payload: $OUT ($(du -sh "$OUT" | awk '{print $1}'), $(find "$OUT" -type f | wc -l | tr -d ' ') files)"

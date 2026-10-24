#!/usr/bin/env bash
# Builds the server payload Maktaba.app carries in Contents/Resources/server (Epic 36,
# story 36.7), for macOS arm64:
#
#   bin/       maktaba-server (web UI embedded) maktaba-api maktaba-streaming maktaba-launcher
#   postgres/  bin/ lib/ share/          PostgreSQL 16, relocatable
#   ffmpeg/    ffmpeg ffprobe            static
#   ollama/    ollama llama-server       the language model server (arm64)
#   python/    CPython 3.12              python-build-standalone, stdlib precompiled
#   pipeline/  maktaba_pipeline-<v>-py3-none-any.whl constraints.txt
#   migrations/                          the database's schema (shared/db/migrations): maktaba-api finds them beside bin/
#   licenses/  the third-party licences and where their sources are
#   payload.json                         what is inside, with versions and checksums
#
# Third-party pieces come from pins.env and are checked against its SHA-256s. The MLX /
# torch wheels and the models (the language model too) are not here: the app downloads
# them on first run (36.3, setup step llm).
#
# With a signing identity every Mach-O file is signed (hardened runtime, secure timestamp;
# python3 with entitlements/python.plist). Without one nothing is signed and the payload
# still runs (Go and the downloaded binaries carry their own ad-hoc signatures).
#
# It also writes <build>/tauri.mac-app.conf.json, the config overlay `tauri build --config`
# takes to bundle the payload and the LaunchAgent.
#
# Usage: payload.sh [--version VERSION] [--build DIR] [--cache DIR] [--sign IDENTITY]
#   --version  release tag (default: git describe)
#   --build    work directory (default: build/mac-app); the payload is <build>/server
#   --cache    downloads (default: <build>/downloads, or $MAC_APP_CACHE)
#   --sign     codesign identity (default: $APPLE_SIGNING_IDENTITY; empty = unsigned,
#              "-" = ad-hoc without the hardened runtime, for local tests)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"

die() { printf 'payload: error: %s\n' "$*" >&2; exit 1; }
log() { printf '\033[1;36m[payload]\033[0m %s\n' "$*" >&2; }

VERSION="${VERSION:-}"
BUILD="$REPO_ROOT/build/mac-app"
CACHE="${MAC_APP_CACHE:-}"
IDENTITY="${APPLE_SIGNING_IDENTITY:-}"
while [ $# -gt 0 ]; do
    case "$1" in
        --version) [ $# -ge 2 ] || die "--version needs a value"; VERSION="$2"; shift 2 ;;
        --build)   [ $# -ge 2 ] || die "--build needs a value"; BUILD="$2"; shift 2 ;;
        --cache)   [ $# -ge 2 ] || die "--cache needs a value"; CACHE="$2"; shift 2 ;;
        --sign)    [ $# -ge 2 ] || die "--sign needs a value"; IDENTITY="$2"; shift 2 ;;
        -h|--help) sed -n '2,/^set -e/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//'; exit 0 ;;
        *)         die "unknown option '$1'" ;;
    esac
done
[ "$(uname -s)" = Darwin ] || die "the Mac app payload is built on macOS (codesign, ditto)"
for t in go uv pnpm curl unzip shasum codesign file lipo; do
    command -v "$t" >/dev/null 2>&1 || die "$t is required"
done
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
mkdir -p "$OUT"/{bin,postgres,ffmpeg,ollama,pipeline,licenses} "$WORK"

# sha256, fetch (download once, always checked against pins.env), check_pins
# shellcheck source-path=SCRIPTDIR source=lib.sh
. "$SCRIPT_DIR/lib.sh"
check_pins >&2 || die "pins.env is incomplete (above)"

# --- Go binaries (the release envelope: -trimpath, stamped, web UI embedded) -----------------
log "building the Go binaries for darwin/arm64 ($VERSION)"
make -C "$REPO_ROOT" --no-print-directory server build-go \
    GOOS=darwin GOARCH=arm64 VERSION="$VERSION" BIN_SUBDIR=darwin-arm64/ >/dev/null
cp "$REPO_ROOT/cmd/maktaba-server/bin/darwin-arm64/maktaba-server" \
   "$REPO_ROOT/api/bin/darwin-arm64/maktaba-api" \
   "$REPO_ROOT/streaming/bin/darwin-arm64/maktaba-streaming" \
   "$REPO_ROOT/cloud/bin/darwin-arm64/maktaba-cloudlink" "$OUT/bin/"
(cd "$REPO_ROOT/cmd/maktaba-launcher" && \
    GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 \
    go build -trimpath -ldflags "-buildid= -s -w -X main.Version=$VERSION" -o "$OUT/bin/maktaba-launcher" .)

# --- PostgreSQL ----------------------------------------------------------------------------------
log "PostgreSQL $POSTGRES_VERSION"
pg_tgz="$(fetch "$POSTGRES_URL" "$POSTGRES_SHA256" "postgresql-$POSTGRES_VERSION.tar.gz")"
tar -xzf "$pg_tgz" -C "$WORK"
pg="$WORK/postgresql-$POSTGRES_VERSION-aarch64-apple-darwin"
[ -x "$pg/bin/postgres" ] || die "unexpected PostgreSQL archive layout"
mkdir -p "$OUT/postgres/bin"
# what the supervisor runs (36.1) and what a backup, a restore, an upgrade or a person needs
# (a full node's copy of its home's database is a logical subscription: these, and pgoutput
# and libpqwalreceiver from lib/)
for b in postgres initdb pg_ctl pg_dump pg_dumpall pg_restore psql pg_isready pg_upgrade pg_controldata pg_resetwal; do
    cp "$pg/bin/$b" "$OUT/postgres/bin/"
done
# libraries (libpq, OpenSSL, the extensions) and share/ (catalogs, extensions, time zones),
# without static libraries and build files
(cd "$pg" && tar -cf - --exclude='*.a' --exclude=pgxs --exclude=pkgconfig lib share) | (cd "$OUT/postgres" && tar -xf -)
# no logical decoding output plugin but pgoutput (a full node's copy): a machine's replication
# role may make a slot with any plugin the home's server has, and another one (test_decoding)
# decodes every table, past what is published to it (specs/epics/37-node-replica)
find "$OUT/postgres/lib" -type f ! -name 'pgoutput.*' -exec grep -l -a -F _PG_output_plugin_init {} + 2>/dev/null |
    while IFS= read -r f; do rm -f "$f"; done || true
cp "$pg/COPYRIGHT" "$OUT/licenses/postgresql.txt"
cp "$pg/LICENSE" "$OUT/licenses/postgresql-binaries.txt"

# --- ffmpeg --------------------------------------------------------------------------------------
log "ffmpeg $FFMPEG_VERSION"
unzip -oq "$(fetch "$FFMPEG_URL" "$FFMPEG_SHA256" "ffmpeg-$FFMPEG_VERSION.zip")" -d "$OUT/ffmpeg"
unzip -oq "$(fetch "$FFPROBE_URL" "$FFPROBE_SHA256" "ffprobe-$FFMPEG_VERSION.zip")" -d "$OUT/ffmpeg"
chmod 0755 "$OUT/ffmpeg/ffmpeg" "$OUT/ffmpeg/ffprobe"
cat >"$OUT/licenses/ffmpeg.txt" <<EOF
ffmpeg and ffprobe $FFMPEG_VERSION, built by Martin Riedl (https://ffmpeg.martin-riedl.de),
configured with --enable-gpl --enable-version3: licensed under the GNU General Public
License version 3 (https://www.gnu.org/licenses/gpl-3.0.html).

Binaries: $FFMPEG_URL
          $FFPROBE_URL
Source:   https://ffmpeg.org/releases/ffmpeg-$FFMPEG_VERSION.tar.xz
Build scripts and the versions of every library linked in:
          https://git.martin-riedl.de/ffmpeg/build-script
          https://ffmpeg.martin-riedl.de/download/macos/arm64/1783011502_8.1.2/versions.txt
EOF

# --- Ollama (the language model server of the LLM stages) ----------------------------------------
log "Ollama $OLLAMA_VERSION"
mkdir -p "$WORK/ollama" "$OUT/licenses/ollama"
tar -xzf "$(fetch "$OLLAMA_URL" "$OLLAMA_SHA256" "ollama-$OLLAMA_VERSION-darwin.tgz")" -C "$WORK/ollama"
# ollama runs GGUF models (qwen2.5:7b) in llama-server, its llama.cpp runner, with the Metal
# backend built in: both self-contained on arm64. The release is universal: the x86_64
# halves and CPU backends (lib*.so, lib*.dylib) and the MLX engine (mlx_metal_v*/, only for
# MLX-format models, ~400 MB) stay out.
for b in ollama llama-server; do
    [ -f "$WORK/ollama/$b" ] || die "unexpected Ollama archive layout (no $b)"
    case " $(lipo -archs "$WORK/ollama/$b") " in
        *" arm64 "*) ;;
        *) die "Ollama's $b has no arm64 code" ;;
    esac
    if [ "$(lipo -archs "$WORK/ollama/$b")" = arm64 ]; then
        cp "$WORK/ollama/$b" "$OUT/ollama/$b"
    else
        lipo "$WORK/ollama/$b" -thin arm64 -output "$OUT/ollama/$b"
    fi
    chmod 0755 "$OUT/ollama/$b"
done
for f in "$WORK"/ollama/*LICENSE* "$WORK"/ollama/*NOTICE*; do
    case "${f##*/}" in MLX*) continue ;; esac   # the MLX engine is not shipped
    if [ -f "$f" ]; then cp "$f" "$OUT/licenses/ollama/"; fi
done
cat >"$OUT/licenses/ollama.txt" <<EOF
Ollama $OLLAMA_VERSION (ollama, llama-server), by Ollama, licensed under the MIT License
(https://github.com/ollama/ollama/blob/v$OLLAMA_VERSION/LICENSE). The licences of what it
builds in (llama.cpp, ggml, Go, cpp-httplib, fmt, …) are in licenses/ollama/.

Binaries: $OLLAMA_URL (arm64 halves)
Source:   https://github.com/ollama/ollama/tree/v$OLLAMA_VERSION
EOF

# --- Python --------------------------------------------------------------------------------------
log "Python $PYTHON_VERSION"
tar -xzf "$(fetch "$PYTHON_URL" "$PYTHON_SHA256" "cpython-$PYTHON_VERSION.tar.gz")" -C "$WORK"
py="$WORK/python"
[ -x "$py/bin/python3" ] || die "unexpected python-build-standalone layout"
# Tk, IDLE and the Tcl runtime are never used by the pipeline (~10 MB).
rm -rf "$py"/lib/tcl* "$py"/lib/tk* "$py"/lib/itcl* "$py"/lib/thread* "$py"/lib/libtcl* "$py"/lib/libtk* \
       "$py"/lib/python3.12/idlelib "$py"/lib/python3.12/tkinter "$py"/lib/python3.12/turtledemo \
       "$py"/lib/python3.12/lib-dynload/_tkinter.* "$py"/bin/idle3*
# Precompile the standard library with hashes that are never re-checked: Python then never
# writes __pycache__ into the signed app (a new file there breaks its seal).
"$py/bin/python3" -I -m compileall -q -j 0 --invalidation-mode unchecked-hash "$py/lib/python3.12" >/dev/null || true
cp "$py/lib/python3.12/LICENSE.txt" "$OUT/licenses/python.txt"
mv "$py" "$OUT/python"

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
[ "${#whl[@]}" -eq 1 ] && [ -f "${whl[0]}" ] || die "expected one pipeline wheel"
cp "${whl[0]}" "$OUT/pipeline/"
cp "${cons[0]}" "$OUT/pipeline/constraints.txt"

# --- the database's migrations: setup's finish and the api's auto-migrate run from the user's
# data directory, never a checkout, so the payload carries them (maktaba-api: beside bin/) -------
log "database migrations"
cp -R "$REPO_ROOT/shared/db/migrations" "$OUT/migrations"

# --- payload.json --------------------------------------------------------------------------------
WHEEL_NAME="$(basename "${whl[0]}")"
cat >"$OUT/payload.json" <<EOF
{
  "schema": 1,
  "version": "$VERSION",
  "platform": "darwin-arm64",
  "postgres": {"version": "$POSTGRES_VERSION", "source": "$POSTGRES_URL", "sha256": "$POSTGRES_SHA256"},
  "ffmpeg": {"version": "$FFMPEG_VERSION", "source": "$FFMPEG_URL", "sha256": "$FFMPEG_SHA256",
             "ffprobe_source": "$FFPROBE_URL", "ffprobe_sha256": "$FFPROBE_SHA256"},
  "python": {"version": "$PYTHON_VERSION", "source": "$PYTHON_URL", "sha256": "$PYTHON_SHA256"},
  "ollama": {"version": "$OLLAMA_VERSION", "source": "$OLLAMA_URL", "sha256": "$OLLAMA_SHA256"},
  "pipeline": {"wheel": "$WHEEL_NAME", "sha256": "$(sha256 "$OUT/pipeline/$WHEEL_NAME")",
               "constraints_sha256": "$(sha256 "$OUT/pipeline/constraints.txt")"}
}
EOF
rm -rf "$WORK"
xattr -cr "$OUT" 2>/dev/null || true

# --- signing -------------------------------------------------------------------------------------
# Each Mach-O file is signed on its own (no --deep, and there are no nested bundles), so
# python3's entitlements stay on python3 only.
if [ -n "$IDENTITY" ]; then
    log "signing every Mach-O file as '$IDENTITY'"
    machos="$BUILD/machos.txt"
    find "$OUT" -type f -print0 | xargs -0 file | awk -F': ' '/Mach-O/ {print $1}' >"$machos"
    # "-" (ad-hoc, local tests): no hardened runtime, whose library validation needs the
    # Team ID only a Developer ID gives (Postgres loading its own libssl would be refused).
    opts=(--force --timestamp --options runtime)
    [ "$IDENTITY" = "-" ] && opts=(--force)
    while IFS= read -r f; do
        ent=()
        case "$f" in "$OUT"/python/bin/python3*) ent=(--entitlements "$SCRIPT_DIR/entitlements/python.plist") ;; esac
        codesign "${opts[@]}" ${ent[@]+"${ent[@]}"} --sign "$IDENTITY" "$f"
    done <"$machos"
    log "signed $(wc -l <"$machos" | tr -d ' ') files"
else
    log "no signing identity: unsigned payload (local builds run it as is)"
fi

# --- the tauri config overlay --------------------------------------------------------------------
# The LaunchAgent comes from the app shell (story 36.5); bundled when it exists.
PLIST="$REPO_ROOT/apps/desktop/src-tauri/LaunchAgents/com.hamzalabs.maktaba.server.plist"
files="{}"
if [ -f "$PLIST" ]; then
    files="{\"Library/LaunchAgents/com.hamzalabs.maktaba.server.plist\": \"$PLIST\"}"
else
    log "no LaunchAgent plist yet at ${PLIST#"$REPO_ROOT"/}: bundled without it"
fi
# Signing the app: with an identity, that identity and the hardened runtime. Without one the
# app is still signed ad hoc as a bundle ("-"): macOS registers the server's login item
# (SMAppService) only for a signed bundle, and the linker's signature of the executable alone
# fails it ("Codesigning failure loading plist", -67056), leaving the app on its welcome screen.
signing='"signingIdentity": "-", "hardenedRuntime": false'
if [ -n "$IDENTITY" ] && [ "$IDENTITY" != "-" ]; then
    signing='"hardenedRuntime": true'
fi
# web/dist is built above (make server), so tauri does not rebuild it.
cat >"$BUILD/tauri.mac-app.conf.json" <<EOF
{
  "build": {"beforeBuildCommand": ""},
  "bundle": {
    "resources": {"$OUT/": "server/"},
    "macOS": {
      "minimumSystemVersion": "15.0",
      $signing,
      "files": $files
    }
  }
}
EOF
log "payload: $OUT ($(du -sh "$OUT" | awk '{print $1}'))"

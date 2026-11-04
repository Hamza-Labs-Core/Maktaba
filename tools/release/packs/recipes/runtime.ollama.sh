#!/usr/bin/env bash
# runtime.ollama: the language model server for a Mac whose app has none (the Core app,
# story 51.16): ollama and llama-server from Ollama's pinned macOS release (the one the
# full app carries, tools/release/mac-app/pins.env), checked against its sha256, with
# their licences. The binaries are kept as Ollama signed them (universal: the pack is
# built on Linux, with no lipo); the MLX engine and the CPU backends stay out, as in the
# full app.
set -euo pipefail
stage="$1" platform="$2"
die() { echo "runtime.ollama: $*" >&2; exit 1; }
[ "$platform" = darwin-arm64 ] || die "a pack of darwin-arm64 only, not $platform"
pins="${REPO_ROOT:?}/tools/release/mac-app/pins.env"
pin() { sed -n "s/^$1=//p" "$pins" | head -n 1; }
version="$(pin OLLAMA_VERSION)" url="$(pin OLLAMA_URL)" sha="$(pin OLLAMA_SHA256)"
[ -n "$version" ] && [ -n "$url" ] && [ -n "$sha" ] || die "no Ollama pin in $pins"
work="${WORK:?}"
mkdir -p "$work/x" "$stage/ollama" "$stage/licenses/ollama"
tgz="$work/ollama-$version-darwin.tgz"
curl -fsSL --retry 3 -o "$tgz" "$url"
if command -v sha256sum >/dev/null; then
    got="$(sha256sum "$tgz" | cut -d' ' -f1)"
else
    got="$(shasum -a 256 "$tgz" | cut -d' ' -f1)"
fi
[ "$got" = "$sha" ] || die "$url: sha256 $got, pinned $sha"
tar -xzf "$tgz" -C "$work/x"
for b in ollama llama-server; do
    [ -f "$work/x/$b" ] || die "unexpected Ollama archive layout (no $b)"
    cp "$work/x/$b" "$stage/ollama/$b"
    chmod 0755 "$stage/ollama/$b"
done
for f in "$work"/x/*LICENSE* "$work"/x/*NOTICE*; do
    case "${f##*/}" in MLX*) continue ;; esac
    if [ -f "$f" ]; then cp "$f" "$stage/licenses/ollama/"; fi
done
cat >"$stage/licenses/ollama.txt" <<EOF
Ollama $version (ollama, llama-server), by Ollama, licensed under the MIT License
(https://github.com/ollama/ollama/blob/v$version/LICENSE). The licences of what it builds
in (llama.cpp, ggml, Go, cpp-httplib, fmt, ...) are in licenses/ollama/.

Binaries: $url
Source:   https://github.com/ollama/ollama/tree/v$version
EOF

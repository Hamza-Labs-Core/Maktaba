#!/usr/bin/env bash
# The bundled Postgres the update rehearsal runs (docs/update-rehearsal.md): the postgres/
# folder of a published release's Linux payload (maktaba-linux-payload-<v>-<arch>.tar.gz:
# PostgreSQL 16 with pgvector, its libraries beside it), exactly what the Linux app and the
# Docker image run. Downloaded with gh (GH_TOKEN), its SHA-256 checked against the
# release's checksums.txt.
#
# usage: postgres.sh --tag vX.Y.Z --out DIR      (DIR/bin/postgres afterwards)
set -euo pipefail

tag="" out=""
while [ $# -gt 0 ]; do
    case "$1" in
        --tag) tag="$2"; shift 2 ;;
        --out) out="$2"; shift 2 ;;
        *) echo "postgres.sh: unknown option $1" >&2; exit 2 ;;
    esac
done
[ -n "$tag" ] && [ -n "$out" ] || { echo "usage: postgres.sh --tag vX.Y.Z --out DIR" >&2; exit 2; }
[ "$(uname -s)" = Linux ] || { echo "postgres.sh: the releases ship a Postgres payload for Linux only" >&2; exit 1; }
case "$(uname -m)" in
    x86_64) arch=x86_64 ;;
    aarch64|arm64) arch=aarch64 ;;
    *) echo "postgres.sh: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac
if [ -x "$out/bin/postgres" ]; then
    echo "postgres.sh: $out has a Postgres already"
    exit 0
fi
ver="${tag#v}"
file="maktaba-linux-payload-$ver-$arch.tar.gz"
dl="$(mktemp -d)"
trap 'rm -rf "$dl"' EXIT
gh release download "$tag" -p "$file" -p checksums.txt -D "$dl"
want="$(awk -v f="$file" '$2 == f || $2 == "./" f {print $1}' "$dl/checksums.txt")"
[ -n "$want" ] || { echo "postgres.sh: $file is not in $tag's checksums.txt" >&2; exit 1; }
have="$(sha256sum "$dl/$file" | awk '{print $1}')"
[ "$want" = "$have" ] || { echo "postgres.sh: $file: sha256 $have, checksums.txt says $want" >&2; exit 1; }
mkdir -p "$dl/x"
tar -xzf "$dl/$file" -C "$dl/x" --wildcards '*/postgres/*'
src="$(dirname "$(dirname "$(find "$dl/x" -path '*/postgres/bin/postgres' -type f | head -1)")")"
[ -x "$src/bin/postgres" ] || { echo "postgres.sh: no postgres/bin/postgres in $file" >&2; exit 1; }
mkdir -p "$(dirname "$out")"
rm -rf "$out"
mv "$src" "$out"
"$out/bin/postgres" --version
[ -f "$out/lib/vector.so" ] || { echo "postgres.sh: $file's Postgres has no pgvector" >&2; exit 1; }

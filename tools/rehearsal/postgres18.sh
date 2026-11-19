#!/usr/bin/env bash
# The PostgreSQL 18 the update rehearsal's scenario 17 runs (Epic 49, story 49.2): the Linux
# payload's pins (tools/release/linux-app/pins.env: theseus-rs 18, pgvector, pg_textsearch),
# checked by SHA-256, with pgvector and pg_textsearch built against it with gcc as
# payload.sh builds them. A payload of the rehearsal links it as postgres-18/.
#
# usage: postgres18.sh --out DIR      (DIR/bin/postgres afterwards)
set -euo pipefail

out=""
while [ $# -gt 0 ]; do
    case "$1" in
        --out) out="$2"; shift 2 ;;
        *) echo "postgres18.sh: unknown option $1" >&2; exit 2 ;;
    esac
done
[ -n "$out" ] || { echo "usage: postgres18.sh --out DIR" >&2; exit 2; }
[ "$(uname -s)" = Linux ] || { echo "postgres18.sh: Linux only" >&2; exit 1; }
if [ -x "$out/bin/postgres" ] && [ -f "$out/lib/pg_textsearch.so" ]; then
    echo "postgres18.sh: $out has a PostgreSQL 18 already"
    exit 0
fi
case "$(uname -m)" in
    x86_64) arch=X86_64 triple=x86_64 ;;
    aarch64|arm64) arch=AARCH64 triple=aarch64 ;;
    *) echo "postgres18.sh: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
# shellcheck source-path=SCRIPTDIR source=../release/linux-app/pins.env
. "$repo/tools/release/linux-app/pins.env"
url_var="POSTGRES18_URL_$arch" sha_var="POSTGRES18_SHA256_$arch"
dl="$(mktemp -d)"
trap 'rm -rf "$dl"' EXIT
fetch() { "$repo/tools/release/fetch-pinned.sh" "$1" "$2" "$dl/$3"; }
fetch "${!url_var}" "${!sha_var}" pg18.tar.gz
fetch "$PGVECTOR_URL" "$PGVECTOR_SHA256" pgvector.tar.gz
fetch "$PGTEXTSEARCH_URL" "$PGTEXTSEARCH_SHA256" pg_textsearch.tar.gz
tar -xzf "$dl/pg18.tar.gz" -C "$dl"
pg="$dl/postgresql-$POSTGRES18_VERSION-$triple-unknown-linux-gnu"
[ -x "$pg/bin/postgres" ] || { echo "postgres18.sh: unexpected archive layout" >&2; exit 1; }
tar -xzf "$dl/pgvector.tar.gz" -C "$dl"
tar -xzf "$dl/pg_textsearch.tar.gz" -C "$dl"
make -C "$dl/pgvector-$PGVECTOR_VERSION" --no-print-directory PG_CONFIG="$pg/bin/pg_config" OPTFLAGS="" with_llvm=no >"$dl/pgv.log" 2>&1 ||
    { tail -20 "$dl/pgv.log" >&2; exit 1; }
make -C "$dl/pg_textsearch-$PGTEXTSEARCH_VERSION" --no-print-directory -j"$(nproc)" PG_CONFIG="$pg/bin/pg_config" with_llvm=no \
    >"$dl/pgts.log" 2>&1 || { tail -20 "$dl/pgts.log" >&2; exit 1; }
cp "$dl/pgvector-$PGVECTOR_VERSION/vector.so" "$dl/pg_textsearch-$PGTEXTSEARCH_VERSION/pg_textsearch.so" "$pg/lib/"
cp "$dl/pgvector-$PGVECTOR_VERSION/vector.control" "$dl/pgvector-$PGVECTOR_VERSION"/sql/vector--*.sql \
   "$dl/pg_textsearch-$PGTEXTSEARCH_VERSION/pg_textsearch.control" "$dl/pg_textsearch-$PGTEXTSEARCH_VERSION"/sql/pg_textsearch--*.sql \
   "$pg/share/extension/"
# what no scenario loads (the kit goes to every scenario job): LLVM, PL/Python, the headers
rm -rf "$pg"/lib/libLLVM* "$pg"/lib/llvmjit* "$pg"/lib/bitcode "$pg"/lib/*plpython* "$pg"/lib/Python \
       "$pg"/lib/python3* "$pg/include"
mkdir -p "$(dirname "$out")"
rm -rf "$out"
mv "$pg" "$out"
"$out/bin/postgres" --version

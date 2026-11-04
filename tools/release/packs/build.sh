#!/usr/bin/env bash
# Build a release's component packs for one platform (Epic 51, story 51.15).
#
#   tools/release/packs/build.sh --version 0.4.2 --platform linux-amd64 --out OUT
#       [--catalog FILE] [--recipes DIR] [--part-size BYTES] [--only ID]
#
# --only builds that one of the platform's packs (the update rehearsal's tool.yt-dlp,
# story 51.18).
#
# The packs are the ones the catalog asks of the platform (gen-components-index --list):
# each one's recipe (recipes/<id>.sh STAGE PLATFORM VERSION) fills a folder, and build-pack
# makes <id>-<version>-<platform>.tar.zst of it (split in parts over 1.9 GiB), then checks
# it unpacks with the rules a machine uses. A recipe only gathers files: nothing in a pack
# is run at install time.
#
# Reproducible from the commit: SOURCE_DATE_EPOCH (default the commit's time) is every
# entry's time and the wheelhouse's built_at; entries are sorted, uid/gid 0, zstd at its
# best level on one thread. The same commit builds the same bytes.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$here/../../.." && pwd)"
VERSION="" PLATFORM="" OUT="" CATALOG="" RECIPES="$here/recipes" PART_SIZE=0 ONLY=""

die() { echo "packs/build.sh: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
    case "$1" in
        --version) VERSION="${2#v}"; shift 2 ;;
        --platform) PLATFORM="$2"; shift 2 ;;
        --out) OUT="$2"; shift 2 ;;
        --catalog) CATALOG="$2"; shift 2 ;;
        --recipes) RECIPES="$2"; shift 2 ;;
        --part-size) PART_SIZE="$2"; shift 2 ;;
        --only) ONLY="$2"; shift 2 ;;
        *) die "unknown option $1" ;;
    esac
done
if [ -z "$VERSION" ] || [ -z "$PLATFORM" ] || [ -z "$OUT" ]; then
  die "--version, --platform and --out are needed"
fi
case "$PLATFORM" in darwin-arm64 | windows-amd64 | linux-amd64 | linux-arm64) ;; *) die "bad platform $PLATFORM" ;; esac

export SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git -C "$REPO_ROOT" log -1 --pretty=%ct)}"
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"
WORK="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/packs.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT

# the two tools, built once
(cd "$REPO_ROOT/tools/release" && go build -o "$WORK/bin/" ./cmd/gen-components-index ./cmd/build-pack)

list=(--list --platform "$PLATFORM")
[ -z "$CATALOG" ] || list+=(--catalog "$CATALOG")
ids=()
while IFS= read -r id; do [ -z "$id" ] || ids+=("$id"); done < <("$WORK/bin/gen-components-index" "${list[@]}")
if [ -n "$ONLY" ]; then
    found=""
    for id in ${ids[@]+"${ids[@]}"}; do [ "$id" != "$ONLY" ] || found="$id"; done
    [ -n "$found" ] || die "$ONLY is not one of $PLATFORM's packs (${ids[*]:-none})"
    ids=("$found")
fi
echo "packs for $PLATFORM: ${ids[*]:-none}"

for id in ${ids[@]+"${ids[@]}"}; do
    recipe="$RECIPES/$id.sh"
    [ -f "$recipe" ] || die "no recipe for $id ($recipe)"
    stage="$WORK/stage/$id"
    mkdir -p "$stage"
    echo "--- $id ($PLATFORM)"
    WORK="$WORK/recipe-$id" REPO_ROOT="$REPO_ROOT" bash "$recipe" "$stage" "$PLATFORM" "$VERSION"
    "$WORK/bin/build-pack" --dir "$stage" --id "$id" --version "$VERSION" --platform "$PLATFORM" \
        --out "$OUT" --part-size "$PART_SIZE"
    "$WORK/bin/build-pack" --verify "$id-$VERSION-$PLATFORM.tar.zst" --out "$OUT"
    rm -rf "$stage"
done
ls -l "$OUT"

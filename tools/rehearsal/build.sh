#!/usr/bin/env bash
# The update rehearsal's kit (docs/update-rehearsal.md): the real binaries of two
# releases, built the way the end-to-end update test builds them (-tags e2etrust: the
# trust root and the GitHub API come from the test's fake Sigstore and fake GitHub; every
# other line of code is the release's own), plus each tree's migrations.
#
#   prev/   the previous release, built from its tag (version PREV, the tag's own schema)
#   cur/    this commit as the next release (version PREV+2; its API takes the rehearsal's
#           test-only migration, slot REV+1, for its own)
#   next/   this commit as the release after it (version PREV+3; the same API: a payload
#           that ships one more migration, REV+2, is a release whose API refuses the
#           database it migrated, the rehearsal's injected health failure)
#   versions.env   PREV, FAILED (PREV+1: the newer release the scenario of the live Mac's
#           state has rolled back from), CUR, NEXT, REV (this commit's newest migration)
#
# usage: build.sh --prev-src DIR --prev-version X.Y.Z --cur-src DIR --out DIR
#   --prev-src   a checkout of the previous release's tag (git worktree add DIR vX.Y.Z)
#   --cur-src    the tree to rehearse (default: this repository; the proof runs point it at
#                a tree from before a fix)
# Builds with CGO_ENABLED=0 for this machine's OS and architecture.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
prev_src="" prev_version="" cur_src="$repo" out=""
while [ $# -gt 0 ]; do
    case "$1" in
        --prev-src) prev_src="$2"; shift 2 ;;
        --prev-version) prev_version="${2#v}"; shift 2 ;;
        --cur-src) cur_src="$2"; shift 2 ;;
        --out) out="$2"; shift 2 ;;
        *) echo "build.sh: unknown option $1" >&2; exit 2 ;;
    esac
done
[ -n "$prev_src" ] && [ -n "$prev_version" ] && [ -n "$out" ] || {
    echo "usage: build.sh --prev-src DIR --prev-version X.Y.Z [--cur-src DIR] --out DIR" >&2
    exit 2
}
[[ "$prev_version" =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)$ ]] || {
    echo "build.sh: --prev-version must be X.Y.Z (a stable release), not $prev_version" >&2
    exit 2
}
major="${BASH_REMATCH[1]}" minor="${BASH_REMATCH[2]}" patch="${BASH_REMATCH[3]}"
failed="$major.$minor.$((patch + 1))"
cur="$major.$minor.$((patch + 2))"
next="$major.$minor.$((patch + 3))"

# the newest migration slot of a tree (the four digits of its newest NNNN_*.sql)
newest_slot() {
    find "$1/shared/db/migrations" -maxdepth 1 -name '[0-9][0-9][0-9][0-9]_*.sql' -exec basename {} \; |
        cut -c1-4 | sort -n | tail -1 | sed 's/^0*//'
}
rev="$(newest_slot "$cur_src")"
[ -n "$rev" ] || { echo "build.sh: no migrations in $cur_src" >&2; exit 1; }

mkdir -p "$out"
out="$(cd "$out" && pwd)"

# build SRC DST VERSION SCHEMA_REV(0: the tree's own)
build() {
    local src="$1" dst="$2" version="$3" schema="$4" name dir ldflag ld
    rm -rf "$dst"
    mkdir -p "$dst"
    for spec in \
        "maktaba-api:api:github.com/Hamza-Labs-Core/Maktaba/api/internal/version.Version" \
        "maktaba-streaming:streaming:github.com/Hamza-Labs-Core/Maktaba/streaming/internal/version.Version" \
        "maktaba-server:cmd/maktaba-server:github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/version.Version" \
        "maktaba-launcher:cmd/maktaba-launcher:main.Version"; do
        IFS=: read -r name dir ldflag <<<"$spec"
        ld="-X $ldflag=$version"
        if [ "$name" = maktaba-api ] && [ "$schema" -gt 0 ]; then
            ld="$ld -X main.e2eSchemaRev=$schema"
        fi
        echo "build.sh: $name $version (from $src)"
        (cd "$src/$dir" && CGO_ENABLED=0 GOTOOLCHAIN=local go build -trimpath -tags e2etrust -ldflags "$ld" \
            -o "$dst/$name" .)
    done
    cp -R "$src/shared/db/migrations" "$dst/migrations"
}

build "$prev_src" "$out/prev" "$prev_version" 0
build "$cur_src" "$out/cur" "$cur" $((rev + 1))
build "$cur_src" "$out/next" "$next" $((rev + 1))

cat >"$out/versions.env" <<EOF
PREV=$prev_version
FAILED=$failed
CUR=$cur
NEXT=$next
REV=$rev
PREV_COMMIT=$(git -C "$prev_src" rev-parse HEAD 2>/dev/null || echo unknown)
CUR_COMMIT=$(git -C "$cur_src" rev-parse HEAD 2>/dev/null || echo unknown)
EOF
echo "build.sh: the kit is in $out"
cat "$out/versions.env"

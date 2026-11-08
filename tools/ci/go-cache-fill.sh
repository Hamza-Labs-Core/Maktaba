#!/usr/bin/env bash
# Puts every Go module of the checkout in the module cache, before the one job that writes
# its runner's Go cache entry saves it (.github/actions/go-cache): the other jobs on that
# runner restore the same entry and should find their modules in it, not only the writer's.
#
#   go-cache-fill.sh [ROOT]    ROOT: the checkout (default: the current directory)
#
# A module whose go directive asks for a newer Go than the one running is skipped (with
# GOTOOLCHAIN=local, so no toolchain lands in the cache): a job with that Go version writes
# its own entry. Each module's `go mod download` gets three tries, 15 s then 45 s apart
# (GO_CACHE_FILL_DELAYS overrides the waits, for the tests). A module that still fails is a
# warning, not an error: the entry is saved without it and the jobs that need it download it
# (go-mod-download), as they did before. GOPROXY=off, which go-mod-download leaves for the
# job, is lifted here, for these downloads only.
# tools/ci/tests/test_go_cache.py runs it against a fake go.
set -euo pipefail

root="${1:-.}"
cd "$root"
read -r -a delays <<<"${GO_CACHE_FILL_DELAYS:-15 45}"
if [ "${GOPROXY:-}" = off ]; then
    export GOPROXY=https://proxy.golang.org,direct
fi
export GOTOOLCHAIN=local

running="$(go env GOVERSION)"
running="${running#go}"

# newer A B: true when Go version A is later than B (1.26.0 > 1.25.8; 1.26 is 1.26.0)
newer() {
    local a b
    IFS=. read -r -a a <<<"$1"
    IFS=. read -r -a b <<<"$2"
    for i in 0 1 2; do
        local x="${a[$i]:-0}" y="${b[$i]:-0}"
        x="${x%%[!0-9]*}" y="${y%%[!0-9]*}"
        if [ "${x:-0}" -gt "${y:-0}" ]; then return 0; fi
        if [ "${x:-0}" -lt "${y:-0}" ]; then return 1; fi
    done
    return 1
}

filled=0 skipped=0 failed=0
while IFS= read -r mod; do
    dir="$(dirname "$mod")"
    want="$(awk '$1 == "go" { print $2; exit }' "$mod")"
    if [ -n "$want" ] && newer "$want" "$running"; then
        echo "go-cache-fill: $dir wants Go $want (running $running), skipped"
        skipped=$((skipped + 1))
        continue
    fi
    n=0
    until (cd "$dir" && go mod download); do
        if [ "$n" -ge "${#delays[@]}" ]; then
            echo "::warning::go-cache-fill: the modules of $dir did not download; the entry is saved without them"
            failed=$((failed + 1))
            continue 2
        fi
        echo "go-cache-fill: $dir: try $((n + 1)) failed, again in ${delays[$n]}s"
        sleep "${delays[$n]}"
        n=$((n + 1))
    done
    filled=$((filled + 1))
done < <(git ls-files '*go.mod' | grep -v '/testdata/' | sort)

echo "go-cache-fill: $filled modules in the cache, $skipped for a newer Go, $failed failed (Go $running)"

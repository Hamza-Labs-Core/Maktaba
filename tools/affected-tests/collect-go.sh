#!/usr/bin/env bash
# tools/affected-tests/collect-go.sh: the Go half of the coverage map.
#
# For each Go module given, runs each package's unit tests (-short) on its own with
# -coverpkg=<module>/..., so its profile shows every file of the module those tests
# execute, not only the package's own. Writes OUT/<n>.cover per package and OUT/index.tsv
# ("<package dir>\t<profile>") for `affected-tests map --go OUT`.
#
# A package whose tests fail still writes its profile (its tests ran); one that does not
# build writes none and stays unmapped. Either way this script goes on: the unit gate is
# what fails a red test, not the map.
#
# usage: collect-go.sh OUT MODULE...   (JOBS=n parallel packages, default 4)
set -euo pipefail

if [[ "${1:-}" == "--one" ]]; then
  # one package: --one ROOT OUT MODULE MODPATH IMPORTPATH DIR N
  root=$2 out=$3 mod=$4 modpath=$5 imp=$6 dir=$7 n=$8
  rel=${dir#"$root"/}
  if (cd "$root/$mod" && go test -short -count=1 -coverpkg="$modpath/..." \
      -coverprofile="$out/$n.cover" "$imp" >"$out/$n.log" 2>&1); then
    rm -f "$out/$n.log"
  else
    echo "affected-tests: $rel: tests failed or did not build (see $n.log)" >&2
  fi
  if [[ -s "$out/$n.cover" ]]; then
    printf '%s\t%s\n' "$rel" "$n.cover" >"$out/$n.idx"
  fi
  exit 0
fi

out=${1:?usage: collect-go.sh OUT MODULE...}
shift
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
mkdir -p "$out"
out="$(cd "$out" && pwd)"
list="$out/packages.txt"
: >"$list"
n=0
for mod in "$@"; do
  [[ -f "$root/$mod/go.mod" ]] || { echo "affected-tests: $mod has no go.mod" >&2; continue; }
  modpath=$(cd "$root/$mod" && go list -m)
  while IFS=' ' read -r imp dir; do
    [[ -n "$imp" ]] || continue
    n=$((n + 1))
    printf '%s %s %s %s %s %s %s\n' "$root" "$out" "$mod" "$modpath" "$imp" "$dir" "$n" >>"$list"
  done < <(cd "$root/$mod" && go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}} {{.Dir}}{{end}}' ./...)
done
echo "affected-tests: $n test packages in $# modules"
# shellcheck disable=SC2016
xargs -P "${JOBS:-4}" -L 1 bash -c 'exec "$0" --one "$@"' "${BASH_SOURCE[0]}" <"$list"
cat "$out"/*.idx >"$out/index.tsv" 2>/dev/null || : >"$out/index.tsv"
rm -f "$out"/*.idx
echo "affected-tests: $(wc -l <"$out/index.tsv" | tr -d ' ') package profiles in $out/index.tsv"

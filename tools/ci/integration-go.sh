#!/usr/bin/env bash
# tools/ci/integration-go.sh — the integration tier's Go modules (Story 20.1), run
# MAKTABA_INTEGRATION_JOBS at a time (default 4) rather than one after another: each
# module's `go test` runs in its own process, its output is printed whole once every
# module is done (in the order given, so the log reads as before), and the tier fails
# when any module failed. The modules are given biggest first (the Makefile's order), so
# the longest start first.
#
#   tools/ci/integration-go.sh <module dir>...
#
# -vet=off: the unit tier's go test and the lint job vet every package already.
set -uo pipefail

jobs="${MAKTABA_INTEGRATION_JOBS:-4}"
out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT

i=0
for mod in "$@"; do
    i=$((i + 1))
    printf '%s %s\n' "$i" "$mod"
done | OUT="$out" xargs -P "$jobs" -n 2 sh -c '
    n="$1"; mod="$2"
    start="$(date +%s)"
    (cd "$mod" && go test -tags=integration -vet=off -count=1 ./...) >"$OUT/$n.log" 2>&1
    echo "$?" >"$OUT/$n.rc"
    echo "$(( $(date +%s) - start ))" >"$OUT/$n.secs"
' _

rc=0
i=0
for mod in "$@"; do
    i=$((i + 1))
    echo "==> go test -tags=integration ./... ($mod, $(cat "$out/$i.secs" 2>/dev/null || echo ?)s)"
    cat "$out/$i.log" 2>/dev/null
    r="$(cat "$out/$i.rc" 2>/dev/null || echo 1)"
    if [ "$r" != 0 ]; then
        echo "FAIL: go test in $mod (exit $r)"
        rc=1
    fi
done
exit "$rc"

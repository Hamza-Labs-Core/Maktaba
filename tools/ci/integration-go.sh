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
#
# Each module runs with -json (issue #855): its output reads as without it (each package's
# line, and the whole output of a test that failed), then its ten slowest tests, so the
# job's log shows where the tier's time went.
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
    (cd "$mod" && go test -json -tags=integration -vet=off -count=1 ./...) >"$OUT/$n.json" 2>"$OUT/$n.err"
    echo "$?" >"$OUT/$n.rc"
    echo "$(( $(date +%s) - start ))" >"$OUT/$n.secs"
' _

# show prints a module's run as go test without -json would (each package's line, a
# failed test's output, build errors), then its ten slowest tests; a line that is not
# JSON is printed as it is
show() {
    jq -Rjn '
        [inputs | (fromjson? // {Action: "output", Output: (. + "\n")})] as $e
        | ($e | map(select(.Action == "fail" and .Test) | {key: "\(.Package) \(.Test)", value: true}) | from_entries) as $failed
        | $e[]
        | select(.Action == "build-output"
            or (.Action == "output" and .Output != "PASS\n" and (.Test == null or $failed["\(.Package) \(.Test)"])))
        | .Output' "$1.json" || cat "$1.json"
    cat "$1.err"
    echo "--> the ten slowest tests"
    jq -Rr 'fromjson? // empty
        | select((.Action == "pass" or .Action == "fail") and .Test and (.Test | contains("/") | not))
        | "\(.Elapsed)s\t\(.Package | sub("^github.com/Hamza-Labs-Core/Maktaba/"; ""))\t\(.Test)"' "$1.json" |
        sort -rn | sed -n 1,10p
}

rc=0
i=0
for mod in "$@"; do
    i=$((i + 1))
    echo "==> go test -tags=integration ./... ($mod, $(cat "$out/$i.secs" 2>/dev/null || echo ?)s)"
    show "$out/$i"
    r="$(cat "$out/$i.rc" 2>/dev/null || echo 1)"
    if [ "$r" != 0 ]; then
        echo "FAIL: go test in $mod (exit $r)"
        rc=1
    fi
done
exit "$rc"

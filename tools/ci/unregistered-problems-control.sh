#!/bin/sh
# unregistered-problems-control.sh : the registry check's positive control (story 60.06 T12). A
# fixture handler behind the recoverer sends one unregistered and one registered type; its test
# passes (it checks only the status, as many do), and unregistered-problems.sh must then fail
# naming the unregistered slug and only it.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
file=$(mktemp)
trap 'rm -f "$file"' EXIT
(cd "$here/../../api" && MAKTABA_UNREGISTERED_PROBLEMS=$file MAKTABA_PROBLEMS_POSITIVE_CONTROL=1 \
	go test -count=1 -run '^TestUnregisteredProblemPositiveControl$' ./internal/middleware/)
if out=$("$here/unregistered-problems.sh" "$file"); then
	echo "::error::the check passed on a fixture that sent an unregistered type"
	exit 1
fi
printf '%s\n' "$out"
case $out in
*positive-control-unregistered-fixture*) ;;
*) echo "::error::the check did not name the fixture's slug"; exit 1 ;;
esac
if [ "$(wc -l < "$file" | tr -d ' ')" != 1 ]; then
	echo "::error::the control recorded more than the unregistered type:"; cat "$file"; exit 1
fi
echo "positive control: the check fails on the unregistered type and names it; the registered one passes"

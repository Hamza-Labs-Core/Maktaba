#!/bin/sh
# no-pg-skips.sh OUT... : fail when `go test -v` output names a test skipped for want of a
# Postgres (its skip reason names MAKTABA_TEST_PG_URL or MAKTABA_TEST_PG_BIN). A gate that
# runs the Postgres tests must not pass on tests that never ran (Epic 37's replica gate).
set -eu
skipped=$(grep -h -B3 -- '--- SKIP' "$@" | grep 'MAKTABA_TEST_PG_' || true)
if [ -n "$skipped" ]; then
	echo "::error::Postgres tests were skipped:"
	printf '%s\n' "$skipped"
	exit 1
fi
echo "no Postgres test skipped ($(grep -h -c -- '--- PASS' "$@" | awk '{ s += $1 } END { print s + 0 }') passed)"

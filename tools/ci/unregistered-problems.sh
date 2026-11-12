#!/bin/sh
# unregistered-problems.sh FILE : fail when the API's test binaries recorded a problem type the
# registry (shared/api/problems.json) lacks. httperror.Write appends each one to the file named
# by MAKTABA_UNREGISTERED_PROBLEMS (one line: slug, status, request) instead of panicking, so a
# test that checks only the status still fails the job here (Epic 60 story 60.06).
set -eu
file=$1
if [ ! -s "$file" ]; then
	echo "every problem type the tests sent is registered"
	exit 0
fi
echo "::error::the API sent problem types that shared/api/problems.json does not list (add an entry, then run go generate ./internal/httperror in api/):"
cut -f1 "$file" | sort | uniq -c | sort -rn
echo "first requests:"
sort -u "$file" | sed -n 1,40p
exit 1

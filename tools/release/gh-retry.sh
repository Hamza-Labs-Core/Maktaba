#!/usr/bin/env bash
# Runs a release asset upload (gh release upload ... --clobber), again when it failed on
# GitHub's side for a moment: an HTTP 5xx (504 "couldn't respond to your request in time"), a
# timeout, an HTTP/2 stream cancelled or reset, a dropped connection. A manual re-run of the
# job fixed each of those (v0.3.142, v0.3.144); this is that re-run, done by the step itself.
#
#   gh-retry.sh CMD [ARG]...
#
# Anything else fails at once, with the command's own status: a 4xx (auth, a missing release,
# a rate limit), a missing file, a usage error. Only uploads go through it, always with
# --clobber, so an asset that half arrived before the failure is replaced on the next try;
# never a signing or verify step (a check that failed is never tried again until it passes).
#
# Env: GH_RETRY_DELAYS, the waits in seconds before each further try (default "1 2 4 8":
# five tries, about 15 s of waiting).
set -uo pipefail

[ $# -gt 0 ] || { echo "gh-retry.sh: no command" >&2; exit 2; }

read -ra delays <<<"${GH_RETRY_DELAYS:-1 2 4 8}"

# transient FILE: FILE (the command's stderr) names a failure worth another try
transient() {
    # a 4xx is the request's own fault: never retried, whatever else the message says
    if grep -Eq 'HTTP 4[0-9][0-9]' "$1"; then
        return 1
    fi
    grep -Eiq 'HTTP 5[0-9][0-9]|stream error|CANCEL|INTERNAL_ERROR|connection reset|connection refused|broken pipe|unexpected EOF|i/o timeout|timed out|timeout|couldn.t respond to your request in time|TLS handshake|no such host|server closed|GOAWAY' "$1"
}

err="$(mktemp)"
trap 'rm -f "${err}"' EXIT
try=1
while :; do
    rc=0
    "$@" 2>"${err}" || rc=$?
    cat "${err}" >&2
    [ "${rc}" -eq 0 ] && exit 0
    if ! transient "${err}"; then
        echo "gh-retry.sh: $1 failed (status ${rc}), not a transient error: not tried again" >&2
        exit "${rc}"
    fi
    if [ "${try}" -gt "${#delays[@]}" ]; then
        echo "::error::$1 failed ${try} times on transient errors; giving up" >&2
        exit "${rc}"
    fi
    wait_s="${delays[$((try - 1))]}"
    echo "::warning::$1 failed on a transient error (try ${try}); trying again in ${wait_s} s" >&2
    sleep "${wait_s}"
    try=$((try + 1))
done

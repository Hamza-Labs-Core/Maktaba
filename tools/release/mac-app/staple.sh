#!/usr/bin/env bash
# Staples a notarized app or disk image and validates the staple, retrying when Apple's
# ticket service drops the connection (stapler exit 68, "The network connection was
# lost"): notarization was already accepted, so a dropped download of its ticket must not
# fail the release. Runs on macOS (CI: desktop-release).
#
# Usage: staple.sh PATH...
#   STAPLE_TRIES  attempts per path (default 6); the waits between them double from
#                 STAPLE_WAIT seconds (default 15): 15, 30, 60, 120, 240
set -euo pipefail

[ $# -ge 1 ] || { echo "usage: staple.sh PATH..." >&2; exit 2; }
tries="${STAPLE_TRIES:-6}"

for path in "$@"; do
    wait="${STAPLE_WAIT:-15}"
    for ((n = 1; ; n++)); do
        if xcrun stapler staple "$path" && xcrun stapler validate "$path"; then
            echo "stapled: $path"
            break
        fi
        if [ "$n" -ge "$tries" ]; then
            echo "::error::stapling $path failed $tries times (Apple's ticket service)" >&2
            exit 1
        fi
        echo "::warning::stapling $path failed (attempt $n of $tries); again in ${wait}s"
        sleep "$wait"
        wait=$((wait * 2))
    done
done

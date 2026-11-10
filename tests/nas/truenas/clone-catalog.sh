#!/usr/bin/env bash
# truenas/apps at the commit catalog.env pins, into DIR (story 59.10): the tooling our CI runs
# on the TrueNAS app. Retried: GitHub's git service drops a fetch now and then.
#   clone-catalog.sh DIR
set -euo pipefail
dir="${1:?usage: clone-catalog.sh DIR}"
# shellcheck source=/dev/null
. "$(dirname "$0")/catalog.env"
rm -rf "$dir"
git init -q "$dir"
git -C "$dir" remote add origin "$TRUENAS_APPS_REPO"
for i in 1 2 3; do
  if git -C "$dir" fetch -q --depth 1 origin "$TRUENAS_APPS_COMMIT"; then break; fi
  [ "$i" = 3 ] && { echo "::error::truenas/apps $TRUENAS_APPS_COMMIT could not be fetched"; exit 1; }
  sleep $((i * 15))
done
git -C "$dir" checkout -q FETCH_HEAD
echo "truenas/apps at $(git -C "$dir" rev-parse HEAD)"

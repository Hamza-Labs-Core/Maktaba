#!/usr/bin/env bash
# Seed the review home, or rotate its passwords (Epic 58 story 58.04), on the review home's
# machine, by the release session or the owner:
#
#   REVIEW_HOME_VIEWER_PASSWORD=... REVIEW_HOME_CHILD_PASSWORD=... deploy/review-home/seed.sh seed
#   REVIEW_HOME_VIEWER_PASSWORD=... REVIEW_HOME_CHILD_PASSWORD=... deploy/review-home/seed.sh rotate
#
# The passwords come from the release secrets, read into the environment of this one command
# (never a file, a flag or the shell history). `seed` makes the libraries, the viewer and the
# restricted child, the live channel and each item's titles and licence note; run it again after
# the libraries' first scan to title what the scan found. `rotate` is the release checklist's
# step after each store submission round: new passwords, every device of both accounts signed
# out. Both run `maktaba-server review-home` inside the home's container (docs/stores/review-home.md).
set -euo pipefail

cmd="${1:-}"
case "${cmd}" in seed | rotate) ;; *) echo "usage: $0 seed|rotate" >&2; exit 2 ;; esac
: "${REVIEW_HOME_VIEWER_PASSWORD:?the password of the viewer (the release secret REVIEW_HOME_VIEWER_PASSWORD)}"
: "${REVIEW_HOME_CHILD_PASSWORD:?the password of the child (the release secret REVIEW_HOME_CHILD_PASSWORD)}"
container="${REVIEW_HOME_CONTAINER:-maktaba-review}"

args=(review-home "${cmd}")
[ "${cmd}" = seed ] && args+=(--manifest /srv/review-media/manifest.json)
# -e NAME (no value) passes this shell's value without writing it on the command line
exec docker exec -e REVIEW_HOME_VIEWER_PASSWORD -e REVIEW_HOME_CHILD_PASSWORD \
	-e REVIEW_HOME_VIEWER -e REVIEW_HOME_CHILD \
	"${container}" /opt/maktaba/server/bin/maktaba-server "${args[@]}"

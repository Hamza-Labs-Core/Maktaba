#!/usr/bin/env bash
# Folds probe rounds into the status data and publishes it (Epic 50 story 50.11):
#
#   status/publish-data.sh <status-data checkout> <rounds file> [--if-changed]
#
# Runs status/update.mjs, then commits checks/ to the checkout's branch (status-data) and
# pushes it, then runs PUBLISH_CMD (status-probe: start the status page's build). With
# --if-changed it does nothing unless a component's state changed, so a probe run can publish
# a change at once and everything else once at its end.
#
# The data lives on its own branch, not main: main's every green commit is tagged as a release
# (auto-tag.yml), and the probe writes every few minutes.
set -euo pipefail
data="${1:?usage: publish-data.sh <data dir> <rounds file> [--if-changed]}"
rounds="${2:?usage: publish-data.sh <data dir> <rounds file> [--if-changed]}"
here="$(cd "$(dirname "$0")" && pwd)"
states() { jq -c '.components | map_values(.state)' "${data}/checks/state.json" 2>/dev/null || echo null; }
before="$(states)"
[ -s "${rounds}" ] || { echo "status: no rounds yet"; exit 0; }
node "${here}/update.mjs" --data "${data}" --rounds "${rounds}"
after="$(states)"
if [ "${3:-}" = --if-changed ] && [ "${before}" = "${after}" ]; then exit 0; fi
git -C "${data}" add -A checks
if git -C "${data}" diff --cached --quiet; then echo "status: nothing new"; exit 0; fi
git -C "${data}" -c user.name="maktaba-status" -c user.email="status@maktaba.tv" \
    commit -qm "status: rounds to $(jq -r .updated_at "${data}/checks/state.json"): ${after}"
for attempt in 1 2 3; do
    git -C "${data}" push -q origin HEAD && break
    [ "${attempt}" = 3 ] && { echo "status: the push failed three times" >&2; exit 1; }
    sleep $((attempt * 5))
done
echo "status: published ${after}"
if [ -n "${PUBLISH_CMD:-}" ]; then
    bash -c "${PUBLISH_CMD}" || echo "status: the publish command failed; the scheduled build catches up" >&2
fi

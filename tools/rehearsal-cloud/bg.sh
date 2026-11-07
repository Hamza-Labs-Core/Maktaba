#!/usr/bin/env bash
# The rehearsal's start and stop for deploy/cloud-ha/bin/bluegreen (Epic 50 story 50.08): a
# colour is its compose service, a tag its image (maktaba-cloud:rehearsal-<tag>, or
# maktaba-cloud:rehearsal for "rehearsal").
#   bg.sh start <colour> <tag>    bg.sh stop <colour>
# Env: BG_PROJECT (the scenario's compose project).
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
dc() { docker compose -p "${BG_PROJECT:?}" -f "${here}/compose.yml" "$@"; }
case "$1" in
    start)
        img="maktaba-cloud:rehearsal"; [ "$3" = rehearsal ] || img="maktaba-cloud:rehearsal-$3"
        var="$(tr '[:lower:]' '[:upper:]' <<<"$2")_IMAGE"
        env "${var}=${img}" docker compose -p "${BG_PROJECT}" -f "${here}/compose.yml" up -d --no-deps "relay-$2" ;;
    stop)
        # SIGTERM: the relay drains its homes, up to its 150 s grace
        dc stop -t 160 "relay-$2" ;;
    *) echo "usage: bg.sh start <colour> <tag> | stop <colour>" >&2; exit 2 ;;
esac

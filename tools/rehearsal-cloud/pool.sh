#!/usr/bin/env bash
# The rehearsal's restart of one api node for deploy/cloud-ha/bin/api-rollout (Epic 50 story
# 50.17, compose-pool.yml): a node is named by its api's host port (the rollout's READY_URL
# reaches it there), a tag is an image (maktaba-cloud:rehearsal-<tag>, or maktaba-cloud:rehearsal
# for "rehearsal").
#   pool.sh <port> <tag>
# SIGTERM to the node's api and worker (the api drains for 12 s, then shuts down), then the
# node again on the new image.
# Env: POOL_PROJECT (the scenario's compose project), API1_PORT..API3_PORT.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
port="${1:?usage: pool.sh <port> <tag>}"; tag="${2:?usage: pool.sh <port> <tag>}"
n=""
for i in 1 2 3; do
    v="API${i}_PORT"
    [ "${!v:-}" != "${port}" ] || n="${i}"
done
[ -n "${n}" ] || { echo "pool.sh: no node on port ${port}" >&2; exit 2; }
img="maktaba-cloud:rehearsal"; [ "${tag}" = rehearsal ] || img="maktaba-cloud:rehearsal-${tag}"
dc() { docker compose -p "${POOL_PROJECT:?}" -f "${here}/compose-pool.yml" "$@"; }
dc stop -t 45 "worker-${n}" "api-${n}"
env "API${n}_IMAGE=${img}" docker compose -p "${POOL_PROJECT}" -f "${here}/compose-pool.yml" \
    up -d --no-deps "api-${n}" "worker-${n}"

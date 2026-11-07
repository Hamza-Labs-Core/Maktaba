#!/usr/bin/env bash
# The cloud deploy guard (Epic 50 story 50.06): relay.yml's deploy job runs it first. A cloud
# deploy goes ahead only when the cloud rehearsal (cloud-rehearsal.yml: failover, deploy,
# rollback and restore, rehearsed in Docker) passed on that exact commit. A run still going is
# waited for; a failed one refuses the deploy and names its failed scenarios; no run at all is
# waited for too (it starts on every push to main), then refused.
#
# Env: SHA, GH_TOKEN, GITHUB_REPOSITORY; CLOUD_REHEARSAL_WORKFLOW (default cloud-rehearsal.yml);
# WAIT_SECONDS (default 3600); POLL_SECONDS (default 30).
set -euo pipefail

: "${SHA:?}" "${GITHUB_REPOSITORY:?}"
wf="${CLOUD_REHEARSAL_WORKFLOW:-cloud-rehearsal.yml}"
wait_s="${WAIT_SECONDS:-3600}"
poll_s="${POLL_SECONDS:-30}"

err() { echo "::error::cloud guard: $*" >&2; exit 1; }

started="$(date +%s)"
while :; do
    # the newest run of the workflow on this commit: "status conclusion id" ("" while none)
    run="$(gh run list --repo "${GITHUB_REPOSITORY}" --workflow "${wf}" --commit "${SHA}" --limit 20 \
        --json status,conclusion,databaseId --jq '.[0] // empty | "\(.status) \(.conclusion) \(.databaseId)"' 2>/dev/null || true)"
    read -r status conclusion id <<<"${run:-none none 0}"
    if [ "${status}" = completed ]; then
        if [ "${conclusion}" = success ]; then
            echo "cloud guard: the cloud rehearsal passed on ${SHA} (run ${id})"
            exit 0
        fi
        failed="$(gh api --paginate "repos/${GITHUB_REPOSITORY}/actions/runs/${id}/jobs" \
            --jq '.jobs[] | select(.conclusion != "success" and .conclusion != "skipped") | .name' 2>/dev/null | paste -sd, - || true)"
        err "the cloud rehearsal ${conclusion} on ${SHA} (run ${id}): ${failed:-no job named}. No deploy of a commit whose failover, deploy or restore was not proven"
    fi
    if [ $(( $(date +%s) - started )) -ge "${wait_s}" ]; then
        if [ "${status}" = none ]; then
            err "no cloud rehearsal ran on ${SHA} within ${wait_s} s: run it (Actions → Cloud rehearsal) before deploying"
        fi
        err "the cloud rehearsal on ${SHA} is still ${status} after ${wait_s} s"
    fi
    echo "cloud guard: the cloud rehearsal on ${SHA} is ${status}; waiting"
    sleep "${poll_s}"
done

#!/usr/bin/env bash
# Network 2's probe (Epic 50 story 50.10): from GitHub's runners, outside Hetzner, every 30 s
# for 5 minutes (cloud-probe.yml runs it every 5 minutes): the canary home through the relay,
# the API, the status page, and once the canary's 10 MB file. Each round's results go to the
# witness's Pushgateway (probe_success{job="canary",instance="actions"} …) when PUSHGATEWAY_URL
# is set; two failed rounds in a row of the canary or the API fail the run (GitHub then mails).
#
# For the status page (story 50.11), ROUNDS_FILE gets one JSON line a round: this network's
# results beside network 1's (the witness's own probes, NETWORK1_URL, read with the same token;
# older than 2 minutes or unreachable counts as no answer), for status/update.mjs.
#
# Env: CANARY_URL, API_URL, STATUS_URL, FILE_URL, PUSHGATEWAY_URL, PUSHGATEWAY_TOKEN,
# NETWORK1_URL, ROUNDS_FILE, ROUND_CMD (run after each round: the status page publishes a
# change at once), ROUNDS (10), EVERY (30), CONNECT_TO (curl --connect-to, the rehearsal's edge).
set -uo pipefail
rounds="${ROUNDS:-10}" every="${EVERY:-30}"
connect=() # the rehearsal reaches a home's name through its local edge
[ -z "${CONNECT_TO:-}" ] || connect=(--connect-to "${CONNECT_TO}")
check() { curl -fsS ${connect[@]+"${connect[@]}"} -o /dev/null --max-time 10 "$1" >/dev/null 2>&1 && echo 1 || echo 0; }
network1() { # network 1's latest view, or nulls when it is stale or unreachable
    local n1=""
    [ -z "${NETWORK1_URL:-}" ] || n1="$(curl -fsS --max-time 5 ${PUSHGATEWAY_TOKEN:+-H "Authorization: Bearer ${PUSHGATEWAY_TOKEN}"} "${NETWORK1_URL}" 2>/dev/null)"
    [ -n "${n1}" ] || n1='{}'
    jq -c --argjson now "$(date -u +%s)" 'if ((.at // "1970-01-01T00:00:00Z") | fromdateiso8601) >= ($now - 120) then . else {} end' <<<"${n1}" 2>/dev/null || echo '{}'
}
record() { # record <canary> <api>: one round for the status page
    [ -n "${ROUNDS_FILE:-}" ] || return 0
    network1 | jq -c --arg at "$(date -u +%FT%TZ)" --argjson c "$1" --argjson a "$2" \
        '{at: $at, relay: {actions: $c, witness: .relay, held_ratio: .held_ratio}, api: {actions: $a, witness: .api}, push: {lag: .push_lag}}' \
        >>"${ROUNDS_FILE}"
}
push() {
    [ -n "${PUSHGATEWAY_URL:-}" ] || return 0
    curl -fsS --max-time 10 ${PUSHGATEWAY_TOKEN:+-H "Authorization: Bearer ${PUSHGATEWAY_TOKEN}"} \
        --data-binary @- "${PUSHGATEWAY_URL}/metrics/job/network2/instance/actions" >/dev/null || echo "probe: the Pushgateway did not take the results" >&2
}
if [ -n "${FILE_URL:-}" ]; then
    stats="$(curl -fsS ${connect[@]+"${connect[@]}"} -o /dev/null --max-time 60 -w '%{http_code} %{speed_download}' "${FILE_URL}" 2>/dev/null || echo "000 0")"
    read -r code speed <<<"${stats}"
    echo "probe: 10 MB file: HTTP ${code}, ${speed} bytes/s"
    printf 'canary_throughput_bytes_per_second %s\n' "${speed%%.*}" | push
fi
canary_fails=0 api_fails=0 worst=0
for i in $(seq 1 "${rounds}"); do
    c="$(check "${CANARY_URL:?}")" a="$(check "${API_URL:?}")" s=1
    [ -z "${STATUS_URL:-}" ] || s="$(check "${STATUS_URL}")"
    echo "probe: round ${i}: canary ${c}, api ${a}, status ${s}"
    record "${c}" "${a}"
    [ -z "${ROUND_CMD:-}" ] || bash -c "${ROUND_CMD}" || echo "probe: the round command failed" >&2
    printf 'probe_success{job="canary"} %s\nprobe_success{job="api"} %s\nprobe_success{job="status"} %s\n' "${c}" "${a}" "${s}" | push
    if [ "${c}" = 1 ]; then canary_fails=0; else canary_fails=$((canary_fails + 1)); fi
    if [ "${a}" = 1 ]; then api_fails=0; else api_fails=$((api_fails + 1)); fi
    [ "${canary_fails}" -gt "${worst}" ] && worst="${canary_fails}"
    [ "${api_fails}" -gt "${worst}" ] && worst="${api_fails}"
    [ "${i}" = "${rounds}" ] || sleep "${every}"
done
if [ "${worst}" -ge 2 ]; then
    echo "::error::the canary or the API failed ${worst} probes in a row from outside Hetzner"
    exit 1
fi
echo "probe: ok"

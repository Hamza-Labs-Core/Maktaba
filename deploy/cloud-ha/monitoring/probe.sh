#!/usr/bin/env bash
# Network 2's probe (Epic 50 story 50.10): from GitHub's runners, outside Hetzner, every 30 s
# for 5 minutes (cloud-probe.yml runs it every 5 minutes): the canary home through the relay,
# the API, the status page, and once the canary's 10 MB file. Each round's results go to the
# witness's Pushgateway (probe_success{job="canary",instance="actions"} …) when PUSHGATEWAY_URL
# is set; two failed rounds in a row of the canary or the API fail the run (GitHub then mails).
#
# Env: CANARY_URL, API_URL, STATUS_URL, FILE_URL, PUSHGATEWAY_URL, PUSHGATEWAY_TOKEN,
# ROUNDS (10), EVERY (30).
set -uo pipefail
rounds="${ROUNDS:-10}" every="${EVERY:-30}"
check() { curl -fsS -o /dev/null --max-time 10 "$1" >/dev/null 2>&1 && echo 1 || echo 0; }
push() {
    [ -n "${PUSHGATEWAY_URL:-}" ] || return 0
    curl -fsS --max-time 10 ${PUSHGATEWAY_TOKEN:+-H "Authorization: Bearer ${PUSHGATEWAY_TOKEN}"} \
        --data-binary @- "${PUSHGATEWAY_URL}/metrics/job/network2/instance/actions" >/dev/null || echo "probe: the Pushgateway did not take the results" >&2
}
if [ -n "${FILE_URL:-}" ]; then
    stats="$(curl -fsS -o /dev/null --max-time 60 -w '%{http_code} %{speed_download}' "${FILE_URL}" 2>/dev/null || echo "000 0")"
    read -r code speed <<<"${stats}"
    echo "probe: 10 MB file: HTTP ${code}, ${speed} bytes/s"
    printf 'canary_throughput_bytes_per_second %s\n' "${speed%%.*}" | push
fi
canary_fails=0 api_fails=0 worst=0
for i in $(seq 1 "${rounds}"); do
    c="$(check "${CANARY_URL:?}")" a="$(check "${API_URL:?}")" s=1
    [ -z "${STATUS_URL:-}" ] || s="$(check "${STATUS_URL}")"
    echo "probe: round ${i}: canary ${c}, api ${a}, status ${s}"
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

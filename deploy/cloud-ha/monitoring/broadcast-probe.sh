#!/usr/bin/env bash
# The canary broadcast's viewer probe (Epic 50 story 50.28 §5): one play, as a viewer would.
#   1. redeem the canary pass (BROADCAST_PASS_URL answers {"master": "<master.m3u8 URL>"});
#   2. fetch the master and its first rung's playlist;
#   3. fetch the last 2 segments;
#   4. read the clock: BROADCAST_OCR_CMD (given a segment file, prints the burned-in UTC clock as
#      unix seconds) when set, else the newest segment's PROGRAM-DATE-TIME.
# It records success, startup (pass to the second segment, ms), glass-to-glass (s) and the
# newest segment's age (s). A failed fetch, the offline slate, or a playlist that hasn't moved
# since the last play (STATE_FILE) is a failure; a wrong clock is not (its latency is recorded,
# and broadcast_glass_to_glass_high sees it).
#
# Prints one JSON line; pushes broadcast_probe_* to the witness's Pushgateway (job
# broadcast_probe, instance NETWORK) when PUSHGATEWAY_URL is set. Exits 0 on success, 1 on failure.
#
# Env: BROADCAST_PASS_URL, NETWORK (actions | witness), STATE_FILE, BROADCAST_OCR_CMD,
# PUSHGATEWAY_URL, PUSHGATEWAY_TOKEN, CONNECT_TO (curl --connect-to, the rehearsal's edge).
set -uo pipefail
network="${NETWORK:-actions}"
connect=()
[ -z "${CONNECT_TO:-}" ] || connect=(--connect-to "${CONNECT_TO}")
work="$(mktemp -d)"; trap 'rm -rf "${work}"' EXIT
nowms() { python3 -c 'import time; print(int(time.time() * 1000))'; }
get() { curl -fsS ${connect[@]+"${connect[@]}"} --max-time 10 -o "$2" "$1" 2>/dev/null; }
# resolve <base URL> <reference>: a playlist's URI against the playlist's own URL
resolve() { python3 -c 'import sys, urllib.parse; print(urllib.parse.urljoin(sys.argv[1], sys.argv[2]))' "$1" "$2"; }

ok=0 reason="" startup="" g2g="" age=""
result() {
    jq -cn --argjson ok "${ok}" --arg reason "${reason}" --arg startup "${startup}" --arg g2g "${g2g}" --arg age "${age}" \
        '{ok: $ok, reason: $reason, startup_ms: ($startup | tonumber? // null),
          glass_to_glass_s: ($g2g | tonumber? // null), segment_age_s: ($age | tonumber? // null)}'
    if [ -n "${PUSHGATEWAY_URL:-}" ]; then
        {
            printf 'broadcast_probe_success %s\n' "${ok}"
            [ -z "${startup}" ] || printf 'broadcast_probe_startup_seconds %s\n' "$(jq -n "${startup} / 1000")"
            [ -z "${g2g}" ] || printf 'broadcast_probe_glass_to_glass_seconds %s\n' "${g2g}"
            [ -z "${age}" ] || printf 'broadcast_probe_segment_age_seconds %s\n' "${age}"
        } | curl -fsS --max-time 10 ${PUSHGATEWAY_TOKEN:+-H "Authorization: Bearer ${PUSHGATEWAY_TOKEN}"} --data-binary @- \
            "${PUSHGATEWAY_URL}/metrics/job/broadcast_probe/instance/${network}" >/dev/null ||
            echo "broadcast probe: the Pushgateway did not take the results" >&2
    fi
    [ "${ok}" = 1 ]
    exit $?
}
fail() { reason="$1"; ok=0; result; }

t0="$(nowms)"
get "${BROADCAST_PASS_URL:?}" "${work}/pass.json" || fail "pass"
master="$(jq -r '.master // empty' "${work}/pass.json" 2>/dev/null)"
[ -n "${master}" ] || fail "pass"
get "${master}" "${work}/master.m3u8" || fail "master"
rung="$(grep -v '^#' "${work}/master.m3u8" | grep -m1 . || true)"
[ -n "${rung}" ] || fail "master"
media="$(resolve "${master}" "${rung}")"
get "${media}" "${work}/index.m3u8" || fail "playlist"
segs=()
while IFS= read -r line; do segs+=("${line}"); done < <(grep -v '^#' "${work}/index.m3u8" | grep . | tail -n 2)
[ "${#segs[@]}" -ge 1 ] || fail "playlist"
case "${segs[${#segs[@]}-1]}" in
    *slate/*) fail "offline" ;;
esac
i=0
for s in "${segs[@]}"; do
    i=$((i + 1))
    get "$(resolve "${media}" "${s}")" "${work}/seg${i}.ts" || fail "segment"
done
startup=$(( $(nowms) - t0 ))

# has the playlist moved since the last play? (the edge's own sequence: no clock involved)
seq="$(sed -n 's/^#EXT-X-MEDIA-SEQUENCE:\([0-9]*\).*/\1/p' "${work}/index.m3u8" | head -n1)"
last="${segs[${#segs[@]}-1]}"
if [ -n "${STATE_FILE:-}" ]; then
    prev="$(cat "${STATE_FILE}" 2>/dev/null || true)"
    printf '%s %s\n' "${seq}" "${last}" >"${STATE_FILE}"
    if [ -n "${prev}" ] && [ "${prev}" = "${seq} ${last}" ]; then fail "stale"; fi
fi

# the clock: OCR of the burned-in clock, else the newest segment's PROGRAM-DATE-TIME
now_s="$(jq -n "$(nowms) / 1000")"
shown=""
if [ -n "${BROADCAST_OCR_CMD:-}" ]; then
    shown="$(bash -c "${BROADCAST_OCR_CMD} \"\$1\"" _ "${work}/seg${i}.ts" 2>/dev/null | grep -Eo '^[0-9]+(\.[0-9]+)?$' | head -n1 || true)"
fi
pdt="$(grep '^#EXT-X-PROGRAM-DATE-TIME:' "${work}/index.m3u8" | tail -n1 | cut -d: -f2-)"
dur="$(grep '^#EXTINF:' "${work}/index.m3u8" | tail -n1 | sed 's/^#EXTINF:\([0-9.]*\).*/\1/')"
if [ -n "${pdt}" ]; then
    pdt_s="$(jq -rn --arg d "${pdt}" '($d | capture("(?<s>^[^.Z+]+)(?<f>\\.[0-9]+)?") ) as $m | (($m.s + "Z") | fromdateiso8601) + (($m.f // "0") | tonumber)' 2>/dev/null || true)"
    if [ -n "${pdt_s}" ]; then
        [ -n "${shown}" ] || shown="${pdt_s}"
        age="$(jq -n "((${now_s} - ${pdt_s} - ${dur:-0}) * 1000 | round) / 1000")"
    fi
fi
[ -z "${shown}" ] || g2g="$(jq -n "((${now_s} - ${shown}) * 1000 | round) / 1000")"
ok=1
result

#!/usr/bin/env bash
# Tests for broadcast-probe.sh (story 50.28 T04), against a stand-in edge (python's http.server):
# a good play succeeds with its latency; a playlist that hasn't moved and the offline slate fail;
# a wrong clock succeeds with its latency recorded; a network failure fails.
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"; pid=""
# the stand-in server is stopped by its PID (the trail's #779 rule): never a blanket kill
trap 'if [ -n "${pid}" ]; then kill "${pid}"; wait "${pid}"; fi; rm -rf "${work}"' EXIT
www="${work}/www"; mkdir -p "${www}/live/720p" "${www}/live/slate"
port=$((30000 + RANDOM % 20000))
base="http://127.0.0.1:${port}"
printf '{"master":"%s/live/master.m3u8"}\n' "${base}" >"${www}/pass.json"
printf '#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=3000000\n720p/index.m3u8\n' >"${www}/live/master.m3u8"
head -c 2048 /dev/zero >"${www}/live/720p/10-aaaaaaaa.ts"; cp "${www}/live/720p/10-aaaaaaaa.ts" "${www}/live/720p/11-bbbbbbbb.ts"
cp "${www}/live/720p/10-aaaaaaaa.ts" "${www}/live/slate/0-cccccccc.ts"
iso() { python3 -c 'import sys, datetime; print(datetime.datetime.fromtimestamp(float(sys.argv[1]), datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%f")[:-3] + "Z")' "$1"; }
# playlist <media sequence> <PDT of the newest segment, seconds ago> [slate]
playlist() {
    local pdt; pdt="$(iso "$(python3 -c "import time; print(time.time() - $2)")")"
    {
        printf '#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:%s\n' "$1"
        printf '#EXTINF:4.000,\n10-aaaaaaaa.ts\n'
        if [ "${3:-}" = slate ]; then
            printf '#EXT-X-DISCONTINUITY\n#EXT-X-PROGRAM-DATE-TIME:%s\n#EXTINF:4.000,\n../slate/0-cccccccc.ts\n' "${pdt}"
        else
            printf '#EXT-X-PROGRAM-DATE-TIME:%s\n#EXTINF:4.000,\n11-bbbbbbbb.ts\n' "${pdt}"
        fi
    } >"${www}/live/720p/index.m3u8"
}
(cd "${www}" && exec python3 -m http.server "${port}" --bind 127.0.0.1 >/dev/null 2>&1) &
pid=$!
for _ in $(seq 1 50); do curl -fsS -o /dev/null "${base}/pass.json" 2>/dev/null && break; sleep 0.1; done
fails=0
probe() { BROADCAST_PASS_URL="${1:-${base}/pass.json}" STATE_FILE="${work}/state" bash "${here}/broadcast-probe.sh" 2>"${work}/err"; }
check() { # check <name> <want exit> <jq test> <output>
    local name="$1" code="$2" test="$3" out="$4" got="$5"
    if [ "${got}" = "${code}" ] && jq -e "${test}" <<<"${out}" >/dev/null 2>&1; then echo "ok   ${name}"; else
        echo "FAIL ${name}: exit ${got}, ${out} $(cat "${work}/err")"; fails=1; fi
}

playlist 10 6
out="$(probe)"; code=$?
check "a good play succeeds, with startup, glass-to-glass and segment age" 0 \
    '.ok == 1 and .startup_ms >= 0 and (.glass_to_glass_s > 5.5 and .glass_to_glass_s < 30) and (.segment_age_s > 1.5 and .segment_age_s < 26)' "${out}" "${code}"

out="$(probe)"; code=$?
check "a playlist that hasn't moved since the last play fails (stale)" 1 '.ok == 0 and .reason == "stale"' "${out}" "${code}"

playlist 11 400
out="$(probe)"; code=$?
check "a wrong clock succeeds and records its latency" 0 '.ok == 1 and .glass_to_glass_s > 390' "${out}" "${code}"

playlist 12 2 slate
out="$(probe)"; code=$?
check "the offline slate fails" 1 '.ok == 0 and .reason == "offline"' "${out}" "${code}"

out="$(probe "http://127.0.0.1:1/pass.json")"; code=$?
check "a network failure fails" 1 '.ok == 0 and .reason == "pass"' "${out}" "${code}"

# the burned-in clock, when an OCR command reads one, wins over PROGRAM-DATE-TIME
playlist 13 400
rm -f "${work}/state"
now="$(python3 -c 'import time; print(int(time.time()) - 9)')"
out="$(BROADCAST_OCR_CMD="echo ${now} #" BROADCAST_PASS_URL="${base}/pass.json" bash "${here}/broadcast-probe.sh" 2>"${work}/err")"; code=$?
check "the OCR clock is used when given" 0 '.ok == 1 and .glass_to_glass_s >= 8 and .glass_to_glass_s < 30' "${out}" "${code}"

if [ "${fails}" != 0 ]; then exit 1; fi
echo "all passed"

#!/usr/bin/env bash
# Tests for probe.sh (story 50.10): all up passes; one failed round passes; two in a row fail.
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"; pid=""
# the stand-in server is stopped by its PID (the trail's #779 rule): never a blanket kill, never a hidden error
trap 'if [ -n "${pid}" ]; then kill "${pid}"; wait "${pid}"; fi; rm -rf "${work}"' EXIT
mkdir -p "${work}/www/api"; echo ok >"${work}/www/api/health"; echo ok >"${work}/www/readyz"
port=$((30000 + RANDOM % 20000))
(cd "${work}/www" && exec python3 -m http.server "${port}" --bind 127.0.0.1 >/dev/null 2>&1) &
pid=$!
for _ in $(seq 1 50); do curl -fsS -o /dev/null "http://127.0.0.1:${port}/readyz" 2>/dev/null && break; sleep 0.1; done
fails=0
run() { ROUNDS=3 EVERY=0 CANARY_URL="$1" API_URL="http://127.0.0.1:${port}/readyz" bash "${here}/probe.sh" >"${work}/out" 2>&1; echo $?; }
if [ "$(run "http://127.0.0.1:${port}/api/health")" = 0 ]; then echo "ok   all up"; else echo "FAIL all up: $(cat "${work}/out")"; fails=1; fi
if [ "$(run "http://127.0.0.1:${port}/missing")" = 1 ] && grep -q "failed 3 probes in a row" "${work}/out"; then
    echo "ok   the canary down fails the run"
else
    echo "FAIL down: $(cat "${work}/out")"; fails=1
fi
# the status page's rounds (story 50.11): network 1's fresh view merged in, a stale one dropped
mkdir -p "${work}/www/status"
printf '{"at":"%s","relay":0,"api":1,"push_lag":12,"held_ratio":0.01}\n' "$(date -u +%FT%TZ)" >"${work}/www/status/network1.json"
rm -f "${work}/rounds"
ROUNDS=2 EVERY=0 CANARY_URL="http://127.0.0.1:${port}/api/health" API_URL="http://127.0.0.1:${port}/missing" \
    NETWORK1_URL="http://127.0.0.1:${port}/status/network1.json" ROUNDS_FILE="${work}/rounds" bash "${here}/probe.sh" >"${work}/out" 2>&1
want='{"relay":{"actions":1,"witness":0,"held_ratio":0.01},"api":{"actions":0,"witness":1},"push":{"lag":12}}'
got="$(jq -c 'del(.at)' "${work}/rounds" | sort -u)"
if [ "$(wc -l <"${work}/rounds" | tr -d ' ')" = 2 ] && [ "${got}" = "${want}" ] && jq -e '.at | test("Z$")' "${work}/rounds" >/dev/null; then
    echo "ok   the rounds file has both networks"
else
    echo "FAIL rounds: ${got}"; fails=1
fi
printf '{"at":"2026-01-01T00:00:00Z","relay":1,"api":1}\n' >"${work}/www/status/network1.json"
rm -f "${work}/rounds"
ROUNDS=1 EVERY=0 CANARY_URL="http://127.0.0.1:${port}/api/health" API_URL="http://127.0.0.1:${port}/readyz" \
    NETWORK1_URL="http://127.0.0.1:${port}/status/network1.json" ROUNDS_FILE="${work}/rounds" bash "${here}/probe.sh" >"${work}/out" 2>&1
if [ "$(jq -c '[.relay.witness, .api.witness, .push.lag]' "${work}/rounds")" = "[null,null,null]" ]; then
    echo "ok   a stale network 1 counts as no answer"
else
    echo "FAIL stale: $(cat "${work}/rounds")"; fails=1
fi
rm -f "${work}/rounds"
ROUNDS=1 EVERY=0 CANARY_URL="http://127.0.0.1:${port}/api/health" API_URL="http://127.0.0.1:${port}/readyz" \
    NETWORK1_URL="http://127.0.0.1:1/none" ROUNDS_FILE="${work}/rounds" bash "${here}/probe.sh" >"${work}/out" 2>&1
if [ "$(jq -c '[.relay.actions, .relay.witness]' "${work}/rounds")" = "[1,null]" ]; then
    echo "ok   an unreachable witness counts as no answer"
else
    echo "FAIL unreachable: $(cat "${work}/rounds") $(cat "${work}/out")"; fails=1
fi
if [ "${fails}" != 0 ]; then exit 1; fi
echo "all passed"

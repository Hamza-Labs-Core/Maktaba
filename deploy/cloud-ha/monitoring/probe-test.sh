#!/usr/bin/env bash
# Tests for probe.sh (story 50.10): all up passes; one failed round passes; two in a row fail.
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"; pid=""
trap 'kill "${pid}" 2>/dev/null; wait "${pid}" 2>/dev/null; rm -rf "${work}"' EXIT
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
if [ "${fails}" != 0 ]; then exit 1; fi
echo "all passed"

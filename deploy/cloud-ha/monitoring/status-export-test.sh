#!/usr/bin/env bash
# Tests for bin/status-export (story 50.11): the witness's view from its Prometheus, with null
# for a series Prometheus doesn't have, and nulls when Prometheus is down.
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"; pid=""
# the stand-in server is stopped by its PID (the trail's #779 rule): never a blanket kill, never a hidden error
trap 'if [ -n "${pid}" ]; then kill "${pid}"; wait "${pid}"; fi; rm -rf "${work}"' EXIT
port=$((30000 + RANDOM % 20000))
cat >"${work}/prom.py" <<'PY'
import http.server, json, sys, urllib.parse
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        q = urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query).get("query", [""])[0]
        v = None
        if 'job="canary"' in q: v = "0"
        elif 'job="api"' in q: v = "1"
        elif "held_total" in q: v = "0.07"
        res = [] if v is None else [{"metric": {}, "value": [1700000000, v]}]
        b = json.dumps({"status": "success", "data": {"resultType": "vector", "result": res}}).encode()
        self.send_response(200); self.send_header("Content-Type", "application/json"); self.end_headers(); self.wfile.write(b)
    def log_message(self, *a): pass
http.server.HTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
PY
python3 "${work}/prom.py" "${port}" & pid=$!
for _ in $(seq 1 50); do curl -fsS -o /dev/null "http://127.0.0.1:${port}/" 2>/dev/null && break; sleep 0.1; done
fails=0
PROMETHEUS="http://127.0.0.1:${port}" OUT="${work}/n1.json" bash "${here}/../bin/status-export"
got="$(jq -c 'del(.at)' "${work}/n1.json")"
if [ "${got}" = '{"relay":0,"api":1,"push_lag":null,"held_ratio":0.07}' ] && jq -e '.at | fromdateiso8601' "${work}/n1.json" >/dev/null; then
    echo "ok   the witness's view, null for a missing series"
else
    echo "FAIL view: $(cat "${work}/n1.json")"; fails=1
fi
PROMETHEUS="http://127.0.0.1:1" OUT="${work}/n2.json" bash "${here}/../bin/status-export"
if [ "$(jq -c 'del(.at)' "${work}/n2.json")" = '{"relay":null,"api":null,"push_lag":null,"held_ratio":null}' ]; then
    echo "ok   Prometheus down: all null"
else
    echo "FAIL down: $(cat "${work}/n2.json")"; fails=1
fi
if [ "${fails}" != 0 ]; then exit 1; fi
echo "all passed"

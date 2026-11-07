#!/usr/bin/env bash
# Tests for the one-click runbook actions (Epic 50 story 50.12): a stand-in pair (NODE_EXEC
# answers as Patroni would) and a stand-in Cloudflare API. The real pair runs them in the
# cloud rehearsal (runbook-switchover, runbook-forced-failover, runbook-dns-swap).
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"; pid=""
trap 'kill "${pid}" 2>/dev/null; wait "${pid}" 2>/dev/null; rm -rf "${work}"' EXIT
fails=0
ok() { echo "ok   $*"; }
bad() { echo "FAIL $*"; fails=1; }

# the stand-in pair: ${work}/leader names the leading host; commands are logged
cat >"${work}/node" <<'SH'
#!/usr/bin/env bash
host="$1"; cmd="$2"; st="$(dirname "$0")"
echo "${host}: ${cmd}" >>"${st}/log"
other() { [ "$1" = node-a ] && echo node-b || echo node-a; }
case "${cmd}" in
    *"/primary"*) [ "$(cat "${st}/leader")" = "${host}" ] ;;
    *"switchover --announced"*|*"switchover --unannounced"*)
        [ -f "${st}/refuse" ] && { echo "switchover: refused: the replica lags" >&2; exit 1; }
        other "${host}" >"${st}/leader" ;;
    *"list -f json"*)
        l="$(cat "${st}/leader")"; printf '[{"Member":"%s","Role":"Leader"},{"Member":"%s","Role":"Sync Standby"}]\n' "${l}" "$(other "${l}")" ;;
    *"failover --candidate"*) echo "${cmd##*--candidate }" | cut -d' ' -f1 >"${st}/leader" ;;
    *"stop patroni"*) echo stopped >"${st}/stopped-${host}" ;;
    *) echo "unexpected: ${cmd}" >&2; exit 9 ;;
esac
SH
chmod +x "${work}/node"
export RELAY_HOST_A=node-a RELAY_HOST_B=node-b NODE_EXEC="${work}/node" SUDO="" WAIT=3

echo node-a >"${work}/leader"
if bash "${here}/switchover.sh" --announced >"${work}/out" 2>&1 && [ "$(cat "${work}/leader")" = node-b ]; then
    ok "switchover: the other node leads"
else bad "switchover: $(cat "${work}/out")"; fi
touch "${work}/refuse"
if ! bash "${here}/switchover.sh" --announced >"${work}/out" 2>&1 && grep -q "refused or failed" "${work}/out"; then
    ok "switchover: a refusal fails the action with the reason"
else bad "switchover refused: $(cat "${work}/out")"; fi
rm -f "${work}/refuse"
if ! bash "${here}/switchover.sh" >"${work}/out" 2>&1; then ok "switchover: needs --announced or --unannounced"; else bad "switchover without a flag ran"; fi
echo none >"${work}/leader"
if ! bash "${here}/switchover.sh" --announced >"${work}/out" 2>&1 && grep -q "no node leads" "${work}/out"; then
    ok "switchover: no leader, no action"
else bad "switchover with no leader: $(cat "${work}/out")"; fi

echo node-b >"${work}/leader"
if bash "${here}/failover.sh" --stop-old >"${work}/out" 2>&1 && [ "$(cat "${work}/leader")" = node-a ] && [ -f "${work}/stopped-node-b" ]; then
    ok "failover: the standby leads, the old leader's Patroni stopped"
else bad "failover: $(cat "${work}/out")"; fi
if grep -qE "^node-a: +patronictl -c /etc/patroni/patroni.yml failover --candidate node-a --force" "${work}/log"; then
    ok "failover: asked of the standby's Patroni, for itself"
else bad "failover command: $(grep failover "${work}/log")"; fi

# the stand-in Cloudflare
port=$((30000 + RANDOM % 20000))
cat >"${work}/cf.py" <<'PY'
import http.server, json, sys, urllib.parse
recs = {"r1": {"id": "r1", "name": "relay.example.tv", "type": "A", "content": "203.0.113.10", "ttl": 300},
        "r2": {"id": "r2", "name": "*.relay.example.tv", "type": "A", "content": "203.0.113.10", "ttl": 300}}
class H(http.server.BaseHTTPRequestHandler):
    def ok(self, body, code=200):
        b = json.dumps(body).encode(); self.send_response(code); self.send_header("Content-Type", "application/json"); self.end_headers(); self.wfile.write(b)
    def auth(self):
        if self.headers.get("Authorization") != "Bearer cf-token": self.ok({"success": False}, 403); return False
        return True
    def do_GET(self):
        if not self.auth(): return
        u = urllib.parse.urlparse(self.path); q = urllib.parse.parse_qs(u.query)
        if u.path == "/zones/z1/dns_records": self.ok({"success": True, "result": [r for r in recs.values() if r["name"] == q.get("name", [""])[0]]})
        elif u.path == "/state": self.ok(recs)
        else: self.ok({"success": False}, 404)
    def do_PATCH(self):
        if not self.auth(): return
        rid = self.path.rsplit("/", 1)[-1]
        if not self.path.startswith("/zones/z1/dns_records/") or rid not in recs: self.ok({"success": False}, 404); return
        recs[rid].update(json.loads(self.rfile.read(int(self.headers["Content-Length"]))))
        self.ok({"success": True, "result": recs[rid]})
    def log_message(self, *a): pass
http.server.HTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
PY
python3 "${work}/cf.py" "${port}" & pid=$!
for _ in $(seq 1 50); do curl -fsS -o /dev/null "http://127.0.0.1:${port}/state" -H "Authorization: Bearer cf-token" 2>/dev/null && break; sleep 0.1; done
export CF_API="http://127.0.0.1:${port}" CF_API_TOKEN=cf-token CF_ZONE_ID=z1 RELAY_DOMAIN=relay.example.tv \
    PUBLIC_IP_A=198.51.100.11 PUBLIC_IP_B=198.51.100.12 FLOATING_IP=203.0.113.10
state() { curl -fsS "http://127.0.0.1:${port}/state" -H "Authorization: Bearer cf-token" | jq -c '[.r1.content, .r2.content, .r1.ttl, .r2.ttl, .r1.proxied]'; }
echo node-b >"${work}/leader"
if bash "${here}/dns-swap.sh" --to leader >"${work}/out" 2>&1 && [ "$(state)" = '["198.51.100.12","198.51.100.12",60,60,false]' ]; then
    ok "dns swap: both names on the leader's own address, TTL 60, DNS only"
else bad "dns swap to the leader: $(state) $(cat "${work}/out")"; fi
if bash "${here}/dns-swap.sh" --to floating >"${work}/out" 2>&1 && [ "$(state)" = '["203.0.113.10","203.0.113.10",60,60,false]' ]; then
    ok "dns swap: back on the Floating IP"
else bad "dns swap back: $(state) $(cat "${work}/out")"; fi
if ! bash "${here}/dns-swap.sh" --to 'evil;rm' >"${work}/out" 2>&1 && grep -q "not an IPv4" "${work}/out"; then ok "dns swap: refuses what is not an address"; else bad "dns swap took a bad address"; fi
if ! RELAY_DOMAIN=other.example.tv bash "${here}/dns-swap.sh" --to 192.0.2.1 >"${work}/out" 2>&1 && grep -q "no A record" "${work}/out"; then
    ok "dns swap: a missing record fails the action"
else bad "dns swap with no record: $(cat "${work}/out")"; fi
if ! CF_API_TOKEN=wrong bash "${here}/dns-swap.sh" --to 192.0.2.1 >"${work}/out" 2>&1; then ok "dns swap: a refused token fails the action"; else bad "dns swap with a wrong token passed"; fi

if [ "${fails}" != 0 ]; then exit 1; fi
echo "all passed"

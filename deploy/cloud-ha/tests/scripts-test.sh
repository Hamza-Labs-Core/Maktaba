#!/usr/bin/env bash
# Tests for deploy/cloud-ha/bin (story 50.07 T01–T03, T08), with stubbed curl, patronictl,
# logger and sleep: on-role-change as primary moves the Floating IP once and starts the cloud
# processes, retries a 5xx, and raises fip_assign_failed after the retry window; as a replica
# it stops them and never calls the API; switchover refuses a lagging or unhealthy candidate
# and runs patronictl when every check passes. Needs bash and jq; touches only a temp dir.
set -uo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
# a background Floating IP retry a failed case left running is stopped before the stubs go:
# without them it would run on with the real curl and sleep, for good
cleanup() {
    local f
    for f in "${work}"/*/fip-retry.pid; do [ -f "${f}" ] && kill "$(cat "${f}")" 2>/dev/null; done
    rm -rf "${work}"
}
trap cleanup EXIT
fails=0
ok()   { printf '  ok    %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }

mkdir -p "${work}/bin"
# curl: the Hetzner API answers the codes in $CODES one per call (then $CODE_AFTER, "201" when
# unset), and each
# call is recorded; any other URL (a Caddy health check) answers $HEALTH (0 = up)
cat >"${work}/bin/curl" <<'STUB'
#!/usr/bin/env bash
url="${*: -1}"
if [[ "${url}" == *:8453/readyz ]]; then
    # the green colour: ready while $GREEN_READY says so (a count of good answers, or "always")
    n="$(cat "${STATE}/green_n" 2>/dev/null || echo 0)"; echo $((n + 1)) >"${STATE}/green_n"
    [ "${GREEN_READY:-always}" = always ] || [ "${n}" -lt "${GREEN_READY}" ]
    exit $?
fi
if [[ "${url}" == *:8443/readyz ]]; then exit 0; fi
if [[ "${url}" == */primary ]]; then
    # $STATE/primary_code, when a case writes it, overrides $GUARD_CODE as it runs
    printf '%s' "$(cat "${STATE}/primary_code" 2>/dev/null || echo "${GUARD_CODE:-200}")"
    exit 0
fi
if [[ "${url}" == *floating_ips* ]]; then
    echo "$*" >>"${STATE}/calls"
    n="$(wc -l <"${STATE}/calls" | tr -d ' ')"
    code="$(sed -n "${n}p" <<<"${CODES:-}" | tr -d ' ')"
    printf '%s' "${code:-${CODE_AFTER:-201}}"
    exit 0
fi
exit "${HEALTH:-0}"
STUB
printf '#!/usr/bin/env bash\nexit 0\n' >"${work}/bin/logger"
cat >"${work}/bin/sleep" <<'STUB'
#!/usr/bin/env bash
echo "$1" >>"${STATE}/slept"
STUB
cat >"${work}/bin/active" <<'STUB'
#!/usr/bin/env bash
echo "$1" >>"${STATE}/active"
STUB
cat >"${work}/bin/alert" <<'STUB'
#!/usr/bin/env bash
echo "$1 $2" >>"${STATE}/alerts"
STUB
chmod +x "${work}/bin/"*
echo token >"${work}/token"

role() { # role <case> <role> [env...]: run on-role-change in a fresh state dir
    local name=$1 r=$2; shift 2
    rm -rf "${work:?}/${name:?}"; mkdir -p "${work}/${name}"
    env PATH="${work}/bin:${PATH}" STATE="${work}/${name}" CLOUD_HA_ENV=/nonexistent \
        HCLOUD_TOKEN_FILE="${work}/token" HCLOUD_ENDPOINT=http://fake/v1 FLOATING_IP_ID=7 SERVER_ID=2 \
        MAKTABA_ACTIVE_CMD="${work}/bin/active" ALERT_CMD="${work}/bin/alert" FIP_ADD_ADDR=0 \
        "$@" bash "${here}/bin/on-role-change" on_role_change "${r}" maktaba >"${work}/${name}/out" 2>&1
}
count() { [ -f "$1" ] && wc -l <"$1" | tr -d ' ' || echo 0; }

# T01: primary: one assign call (to floating ip 7, server 2), then the processes start
role primary primary
if [ "$(count "${work}/primary/calls")" = 1 ] && grep -q 'floating_ips/7/actions/assign' "${work}/primary/calls" \
    && grep -q '"server": 2' "${work}/primary/calls" && [ "$(cat "${work}/primary/active")" = start ]; then
    ok "primary: one assign call, processes started"
else
    fail "primary: calls=$(cat "${work}/primary/calls" 2>/dev/null) active=$(cat "${work}/primary/active" 2>/dev/null)"
fi
# Patroni's older role name is the same
role master master
if [ "$(cat "${work}/master/active" 2>/dev/null)" = start ]; then ok "master: started"; else fail "master: $(cat "${work}/master/out")"; fi

# T01: a 5xx is retried with backoff
role retry primary CODES=$'503\n502\n201'
if [ "$(count "${work}/retry/calls")" = 3 ] && [ "$(tr '\n' ' ' <"${work}/retry/slept")" = "1 2 " ] \
    && [ "$(cat "${work}/retry/active")" = start ] && [ ! -f "${work}/retry/alerts" ]; then
    ok "primary: 503, 502 retried after 1 s and 2 s; started; no alert"
else
    fail "retry: calls=$(count "${work}/retry/calls") slept=$(cat "${work}/retry/slept" 2>/dev/null) alerts=$(cat "${work}/retry/alerts" 2>/dev/null)"
fi

# T08: the API refuses for the whole window: fip_assign_failed, the processes start anyway
# (every assign call answers 503)
role refused primary CODE_AFTER=503 FIP_RETRY_FOR=15 FIP_RETRY_EVERY=1 \
    FIP_RETRY_PIDFILE="${work}/refused/fip-retry.pid"
if grep -q '^fip_assign_failed .*DNS-swap runbook' "${work}/refused/alerts" 2>/dev/null && [ "$(cat "${work}/refused/active")" = start ]; then
    ok "refused: fip_assign_failed after the retry window, with the runbook; processes started"
else
    fail "refused: alerts=$(cat "${work}/refused/alerts" 2>/dev/null) out=$(cat "${work}/refused/out")"
fi
# the retry goes on in the background while this node leads, and while Patroni doesn't answer
# (restarting is not a demotion); once Patroni answers it is not the primary, the retry stops
alive() { kill -0 "$(cat "${work}/refused/fip-retry.pid" 2>/dev/null)" 2>/dev/null; }
gone_within() { local _; for _ in $(seq 1 50); do alive || return 0; command sleep 0.1; done; return 1; }
retry_ok=1
alive || { retry_ok=0; fail "refused: no background retry while this node leads"; }
echo 000 >"${work}/refused/primary_code"
command sleep 0.5
alive || { retry_ok=0; fail "refused: the retry stopped when Patroni didn't answer"; }
echo 503 >"${work}/refused/primary_code"
gone_within || { retry_ok=0; fail "refused: the retry still runs after Patroni answered 503 on /primary"; }
grep -q 'no longer leads: the Floating IP retry stops' "${work}/refused/out" \
    || { retry_ok=0; fail "refused: the retry's stop is not logged: $(tail -3 "${work}/refused/out")"; }
[ "${retry_ok}" = 1 ] && ok "refused: the background retry runs while leading or unanswered, and stops on a demotion"

# T02: replica: processes stopped, no API call
for r in replica demoted; do
    role "${r}" "${r}"
    if [ "$(count "${work}/${r}/calls")" = 0 ] && [ "$(cat "${work}/${r}/active")" = stop ]; then
        ok "${r}: stopped, no assign call"
    else
        fail "${r}: calls=$(count "${work}/${r}/calls") active=$(cat "${work}/${r}/active" 2>/dev/null)"
    fi
done

# story 50.18: the database nodes (CLOUD_PROCESSES=off) move only the database's Floating IP
# and start nothing; their replicas drop PgBouncer's clients; the pair in the move moves both
cat >"${work}/bin/psql" <<'STUB'
#!/usr/bin/env bash
echo "$*" >>"${STATE}/psql"
STUB
chmod +x "${work}/bin/psql"
role dbprimary primary CLOUD_PROCESSES=off DB_FLOATING_IP_ID=8 DB_HCLOUD_ENDPOINT=http://fakedb/v1
if [ "$(count "${work}/dbprimary/calls")" = 1 ] && grep -q 'http://fakedb/v1/floating_ips/8/actions/assign' "${work}/dbprimary/calls" \
    && [ ! -f "${work}/dbprimary/active" ]; then
    ok "db primary: the database's Floating IP only, no cloud process started"
else
    fail "db primary: calls=$(cat "${work}/dbprimary/calls" 2>/dev/null) active=$(cat "${work}/dbprimary/active" 2>/dev/null)"
fi
role both primary DB_FLOATING_IP_ID=8
if [ "$(count "${work}/both/calls")" = 2 ] && sed -n 1p "${work}/both/calls" | grep 'floating_ips/8/' >/dev/null \
    && sed -n 2p "${work}/both/calls" | grep 'floating_ips/7/' >/dev/null && [ "$(cat "${work}/both/active")" = start ]; then
    ok "pair primary with a database Floating IP: the database's first, then the relay's; processes started"
else
    fail "both: calls=$(cat "${work}/both/calls" 2>/dev/null) active=$(cat "${work}/both/active" 2>/dev/null)"
fi
role dbreplica replica CLOUD_PROCESSES=off PGBOUNCER=yes
if [ "$(count "${work}/dbreplica/calls")" = 0 ] && [ ! -f "${work}/dbreplica/active" ] \
    && grep -q -- '-c KILL maktaba -c RESUME maktaba' "${work}/dbreplica/psql" \
    && grep -q -- '-c KILL cloud_session -c RESUME cloud_session' "${work}/dbreplica/psql"; then
    ok "db replica: PgBouncer's clients dropped on both pools, nothing stopped, no assign call"
else
    fail "db replica: psql=$(cat "${work}/dbreplica/psql" 2>/dev/null) active=$(cat "${work}/dbreplica/active" 2>/dev/null)"
fi
role nopgb replica
if [ ! -f "${work}/nopgb/psql" ]; then ok "replica without PgBouncer: no admin console call"; else fail "nopgb: $(cat "${work}/nopgb/psql")"; fi

# T03: switchover
cat >"${work}/bin/patronictl" <<'STUB'
#!/usr/bin/env bash
case "$*" in
    *"list -f json"*) cat "${STATE}/members" ;;
    *switchover*) echo "$*" >>"${STATE}/switched"
        # Patroni's refusal: printed, and exit 0
        if [ -f "${STATE}/refuse412" ]; then echo "Switchover failed, details: 412, candidate name does not match with sync_standby"; exit 0; fi
        c="$*"; c="${c##*--candidate }"; c="${c%% *}"
        jq --arg c "${c}" '[.[] | if .Member == $c then .Role = "Leader" else .Role = "Replica" end]' "${STATE}/members" >"${STATE}/m2" && mv "${STATE}/m2" "${STATE}/members" ;;
    *) exit 1 ;;
esac
STUB
chmod +x "${work}/bin/patronictl"
members() { # members <state> <lag>
    printf '[{"Member":"node-a","Host":"10.50.0.11","Role":"Leader","State":"running","Lag in MB":0},{"Member":"node-b","Host":"10.50.0.12","Role":"Sync Standby","State":"%s","Receive Lag":0,"Replay Lag":%s}]' "$1" "$2"
}
sw() { # sw <case> <members json> [env...] -- args
    local name=$1 m=$2; shift 2
    rm -rf "${work:?}/${name:?}"; mkdir -p "${work}/${name}"
    printf '%s' "${m}" >"${work}/${name}/members"
    env PATH="${work}/bin:${PATH}" STATE="${work}/${name}" PATRONICTL=patronictl WAIT=3 "$@" >"${work}/${name}/out" 2>&1
}
sw lag "$(members streaming 3)" bash "${here}/bin/switchover" --announced
if grep -q "lags 3 MB" "${work}/lag/out" && [ ! -f "${work}/lag/switched" ]; then ok "switchover: refuses a lagging replica"; else fail "lag: $(cat "${work}/lag/out")"; fi
sw stopped "$(members stopped 0)" bash "${here}/bin/switchover" --announced
if grep -q "is stopped, not streaming" "${work}/stopped/out"; then ok "switchover: refuses a replica that is not streaming"; else fail "stopped: $(cat "${work}/stopped/out")"; fi
sw caddy "$(members streaming 0)" HEALTH=7 bash "${here}/bin/switchover" --announced
if grep -q "Caddy does not answer" "${work}/caddy/out" && [ ! -f "${work}/caddy/switched" ]; then ok "switchover: refuses when the candidate's Caddy is down"; else fail "caddy: $(cat "${work}/caddy/out")"; fi
sw noflag "$(members streaming 0)" bash "${here}/bin/switchover"
if grep -q "say --announced" "${work}/noflag/out"; then ok "switchover: wants --announced or --unannounced"; else fail "noflag: $(cat "${work}/noflag/out")"; fi
sw good "$(members streaming 0)" bash "${here}/bin/switchover" --announced
if grep -q -- "--leader node-a --candidate node-b --force" "${work}/good/switched" && grep -q "node-b leads" "${work}/good/out"; then
    ok "switchover: every check passes: patronictl switches node-a -> node-b"
else
    fail "good: $(cat "${work}/good/out")"
fi

# --db (story 50.18): three members, the synchronous standby is the default candidate, and
# its PgBouncer (not a Caddy) must answer
cat >"${work}/bin/pg_isready" <<'STUB'
#!/usr/bin/env bash
echo "$*" >>"${STATE}/pg_isready"
exit "${PGREADY:-0}"
STUB
chmod +x "${work}/bin/pg_isready"
members3='[{"Member":"db-1","Host":"10.60.0.31","Role":"Leader","State":"running","Lag in MB":0},{"Member":"db-2","Host":"10.60.0.32","Role":"Replica","State":"streaming","Lag in MB":0},{"Member":"db-3","Host":"10.60.0.33","Role":"Sync Standby","State":"streaming","Lag in MB":0}]'
sw dbgood "${members3}" HEALTH=7 bash "${here}/bin/switchover" --announced --db
if grep -q -- "--leader db-1 --candidate db-3 --force" "${work}/dbgood/switched" && grep -q -- "-h 10.60.0.33 -p 6432" "${work}/dbgood/pg_isready" \
    && grep -q "PgBouncer answers" "${work}/dbgood/out" && grep -q "db-3 leads" "${work}/dbgood/out"; then
    ok "switchover --db: the synchronous standby is the candidate, its PgBouncer checked, no Caddy needed"
else
    fail "dbgood: $(cat "${work}/dbgood/out")"
fi
sw dbdown "${members3}" PGREADY=2 bash "${here}/bin/switchover" --announced --db
if grep -q "db-3's PgBouncer does not answer" "${work}/dbdown/out" && [ ! -f "${work}/dbdown/switched" ]; then
    ok "switchover --db: refuses when the candidate's PgBouncer is down"
else
    fail "dbdown: $(cat "${work}/dbdown/out")"
fi

sw notsync "${members3}" bash "${here}/bin/switchover" --announced --db --candidate db-2
if grep -q "db-2 is not the synchronous standby (db-3)" "${work}/notsync/out" && [ ! -f "${work}/notsync/switched" ]; then
    ok "switchover: refuses a candidate that is not the synchronous standby, before asking Patroni"
else
    fail "notsync: $(cat "${work}/notsync/out")"
fi
rm -rf "${work:?}/refused412"; mkdir -p "${work}/refused412"; touch "${work}/refused412/refuse412"
printf '%s' "${members3}" >"${work}/refused412/members"
env PATH="${work}/bin:${PATH}" STATE="${work}/refused412" PATRONICTL=patronictl WAIT=30 \
    bash "${here}/bin/switchover" --announced --db >"${work}/refused412/out" 2>&1
if grep -q "Patroni refused: Switchover failed, details: 412" "${work}/refused412/out" && [ ! -f "${work}/refused412/slept" ]; then
    ok "switchover: Patroni's refusal (exit 0) fails at once, with its reason"
else
    fail "refused412: $(cat "${work}/refused412/out")"
fi

# the leader guard: stops a running node that Patroni no longer calls the primary, never
# one that leads, never starts anything
guard() { # guard <case> <code> <active>
    rm -rf "${work:?}/$1"; mkdir -p "${work}/$1"
    echo "$3" >"${work}/$1/state"
    env PATH="${work}/bin:${PATH}" STATE="${work}/$1" CLOUD_HA_ENV=/nonexistent GUARD_CODE="$2" ONCE=1 FAILS=1 \
        ACTIVE_FILE="${work}/$1/state" MAKTABA_ACTIVE_CMD="${work}/bin/active" bash "${here}/bin/leader-guard" >"${work}/$1/out" 2>&1
}
guard demoted 503 start
if [ "$(cat "${work}/demoted/active" 2>/dev/null)" = stop ]; then ok "leader-guard: a node that stops leading stops its processes"; else fail "guard demoted: $(cat "${work}/demoted/out")"; fi
guard leads 200 start
if [ ! -f "${work}/leads/active" ]; then ok "leader-guard: the leader keeps its processes"; else fail "guard leads: $(cat "${work}/leads/active")"; fi
guard idle 503 stop
if [ ! -f "${work}/idle/active" ]; then ok "leader-guard: a standby with nothing running is left alone"; else fail "guard idle"; fi

# CLOUD_PROCESSES=off (story 50.18), read again from cloud-ha.env each round: no stop
rm -rf "${work:?}/off"; mkdir -p "${work}/off"; echo start >"${work}/off/state"
echo CLOUD_PROCESSES=off >"${work}/off/env"
env PATH="${work}/bin:${PATH}" STATE="${work}/off" CLOUD_HA_ENV="${work}/off/env" GUARD_CODE=503 ONCE=1 FAILS=1 \
    ACTIVE_FILE="${work}/off/state" MAKTABA_ACTIVE_CMD="${work}/bin/active" bash "${here}/bin/leader-guard" >"${work}/off/out" 2>&1
if [ ! -f "${work}/off/active" ]; then ok "leader-guard: CLOUD_PROCESSES=off in cloud-ha.env: nothing stopped"; else fail "guard off: $(cat "${work}/off/active")"; fi

# migrate-db-nodes (story 50.18): its guards, against five stand-in hosts (NODE_EXEC): each
# host's cloud-ha.env is ${STATE}/<host>.env, ${STATE}/leader names the pair's leader,
# ${STATE}/pgb-down-<host> makes that PgBouncer silent. The whole move runs in the rehearsal
# (db-nodes-migrate).
cat >"${work}/bin/dbhost" <<'STUB'
#!/usr/bin/env bash
host="$1"; cmd="${2//\/etc\/maktaba\/cloud-ha.env/${STATE}/${1}.env}"; cmd="${cmd//\/etc\/maktaba\/db-ca.crt/${STATE}/${1}-db-ca.crt}"
case "${cmd}" in
    *"/primary"*) [ "$(cat "${STATE}/leader")" = "${host}" ] ;;
    *"list -f json"*) cat "${STATE}/members" ;;
    *pg_isready*) [ ! -f "${STATE}/pgb-down-${host}" ] ;;
    *) bash -c "${cmd}" ;;
esac
STUB
chmod +x "${work}/bin/dbhost"
mig() { # mig <case> [env...] -- args: a fresh set of hosts, every guard satisfied
    local name=$1 h; shift
    rm -rf "${work:?}/${name:?}"; mkdir -p "${work}/${name}"
    for h in node-a node-b db-1 db-2 db-3; do echo "NODE_NAME=${h}" >"${work}/${name}/${h}.env"; done
    for h in node-a node-b; do echo DB_FLOATING_IP_ID=8 >>"${work}/${name}/${h}.env"; done
    for h in db-1 db-2 db-3; do echo CLOUD_PROCESSES=off >>"${work}/${name}/${h}.env"; done
    for h in node-a node-b; do echo ca >"${work}/${name}/${h}-db-ca.crt"; done
    echo node-a >"${work}/${name}/leader"
    printf '[{"Member":"node-a","Role":"Leader","State":"running"},{"Member":"node-b","Role":"Sync Standby","State":"streaming","Lag in MB":0}]' >"${work}/${name}/members"
    MIG_DIR="${work}/${name}"
}
mig_run() { # mig_run [env...] -- args
    env PATH="${work}/bin:${PATH}" STATE="${MIG_DIR}" NODE_EXEC=dbhost SUDO="" PATRONI_URL=http://x:8008 \
        RELAY_HOST_A=node-a RELAY_HOST_B=node-b "$@" >"${MIG_DIR}/out" 2>&1
}
mig nohosts; mig_run bash "${here}/bin/migrate-db-nodes" check
if grep -q "DB_HOSTS names the three database nodes" "${MIG_DIR}/out"; then ok "migrate-db-nodes: refuses without DB_HOSTS"; else fail "nohosts: $(cat "${MIG_DIR}/out")"; fi
mig two; mig_run DB_HOSTS="db-1 db-2" bash "${here}/bin/migrate-db-nodes" check
if grep -q "DB_HOSTS must name three nodes, got 2" "${MIG_DIR}/out"; then ok "migrate-db-nodes: refuses two database nodes"; else fail "two: $(cat "${MIG_DIR}/out")"; fi
mig good; mig_run DB_HOSTS="db-1 db-2 db-3" bash "${here}/bin/migrate-db-nodes" check
if grep -q "check: node-a leads, node-b streams" "${MIG_DIR}/out"; then ok "migrate-db-nodes: check passes when every guard holds"; else fail "good: $(cat "${MIG_DIR}/out")"; fi
mig notdb; sed -i.bak 's/^CLOUD_PROCESSES=off/CLOUD_PROCESSES=leader/' "${MIG_DIR}/db-3.env"
mig_run DB_HOSTS="db-1 db-2 db-3" bash "${here}/bin/migrate-db-nodes" check
if grep -q "refused: db-3 is not a database node" "${MIG_DIR}/out"; then ok "migrate-db-nodes: refuses a node not set up as a database node"; else fail "notdb: $(cat "${MIG_DIR}/out")"; fi
mig pgbdown; touch "${MIG_DIR}/pgb-down-node-b"
mig_run DB_HOSTS="db-1 db-2 db-3" bash "${here}/bin/migrate-db-nodes" check
if grep -q "refused: PgBouncer does not answer on node-b" "${MIG_DIR}/out"; then ok "migrate-db-nodes: refuses while a PgBouncer is silent"; else fail "pgbdown: $(cat "${MIG_DIR}/out")"; fi
mig noip; sed -i.bak '/^DB_FLOATING_IP_ID=/d' "${MIG_DIR}/node-b.env"
mig_run DB_HOSTS="db-1 db-2 db-3" bash "${here}/bin/migrate-db-nodes" check
if grep -q "refused: node-b's cloud-ha.env has no DB_FLOATING_IP_ID" "${MIG_DIR}/out"; then ok "migrate-db-nodes: refuses a pair without the database's Floating IP"; else fail "noip: $(cat "${MIG_DIR}/out")"; fi
mig lagging; printf '[{"Member":"node-a","Role":"Leader","State":"running"},{"Member":"node-b","Role":"Replica","State":"streaming","Lag in MB":4}]' >"${MIG_DIR}/members"
mig_run DB_HOSTS="db-1 db-2 db-3" bash "${here}/bin/migrate-db-nodes" check
if grep -q "refused: node-b is not streaming with zero lag" "${MIG_DIR}/out"; then ok "migrate-db-nodes: refuses a lagging replica"; else fail "lagging: $(cat "${MIG_DIR}/out")"; fi
mig noca; rm "${MIG_DIR}/node-a-db-ca.crt"
mig_run DB_HOSTS="db-1 db-2 db-3" bash "${here}/bin/migrate-db-nodes" check
if grep -q "refused: node-a has no /etc/maktaba/db-ca.crt" "${MIG_DIR}/out"; then ok "migrate-db-nodes: refuses an app node that cannot check PgBouncer's certificate"; else fail "noca: $(cat "${MIG_DIR}/out")"; fi
mig noca-plain; rm "${MIG_DIR}/node-a-db-ca.crt"
mig_run DB_HOSTS="db-1 db-2 db-3" DB_SSLMODE=disable bash "${here}/bin/migrate-db-nodes" check
if grep -q "check: node-a leads" "${MIG_DIR}/out"; then ok "migrate-db-nodes: DB_SSLMODE=disable (the rehearsal) needs no CA"; else fail "noca-plain: $(cat "${MIG_DIR}/out")"; fi
mig badstep; mig_run DB_HOSTS="db-1 db-2 db-3" bash "${here}/bin/migrate-db-nodes" everything
if grep -q "usage: migrate-db-nodes" "${MIG_DIR}/out"; then ok "migrate-db-nodes: an unknown step prints the usage"; else fail "badstep: $(cat "${MIG_DIR}/out")"; fi

# make-pgbouncer-cert (story 50.18): signed by the etcd CA, db.internal checked by name
mkdir -p "${work}/pki"
bash "${here}/bin/make-etcd-certs" "${work}/pki" db-1=10.60.0.31 >/dev/null 2>&1
bash "${here}/bin/make-pgbouncer-cert" "${work}/pki" >"${work}/pki/out" 2>&1
if openssl verify -CAfile "${work}/pki/ca.crt" -verify_hostname db.internal "${work}/pki/pgbouncer.crt" >/dev/null 2>&1 \
    && ! openssl verify -CAfile "${work}/pki/ca.crt" -verify_hostname evil.example "${work}/pki/pgbouncer.crt" >/dev/null 2>&1; then
    ok "make-pgbouncer-cert: the CA signs it for db.internal and no other name"
else
    fail "make-pgbouncer-cert: $(cat "${work}/pki/out")"
fi
if ! bash "${here}/bin/make-pgbouncer-cert" "${work}/nopki" >"${work}/pki/out2" 2>&1 && grep -q "make-etcd-certs first" "${work}/pki/out2"; then
    ok "make-pgbouncer-cert: refuses without the CA"
else
    fail "make-pgbouncer-cert without a CA: $(cat "${work}/pki/out2")"
fi

# bluegreen (story 50.08): a release that fails its readiness soon after it starts is stopped
# and the old colour is never touched; a good one replaces the old colour, which drains
cat >"${work}/bin/colour" <<'STUB'
#!/usr/bin/env bash
echo "$*" >>"${STATE}/colours"
STUB
chmod +x "${work}/bin/colour"
bg() { # bg <case> [env...]
    local name=$1; shift
    rm -rf "${work:?}/${name:?}"; mkdir -p "${work}/${name}/state"
    echo blue >"${work}/${name}/state/colour"; echo v1 >"${work}/${name}/state/deployed"
    env PATH="${work}/bin:${PATH}" STATE="${work}/${name}" STATE_DIR="${work}/${name}/state" \
        START_CMD="${work}/bin/colour start" STOP_CMD="${work}/bin/colour stop" READY_STABLE=5 WATCH=4 CHECK_EVERY=2 \
        "$@" bash "${here}/bin/bluegreen" v2 >"${work}/${name}/out" 2>&1
    echo $?
}
rc="$(bg bad GREEN_READY=2)"
if [ "${rc}" != 0 ] && [ "$(tr '\n' ',' <"${work}/bad/colours")" = "start green v2,stop green," ] && [ "$(cat "${work}/bad/state/colour")" = blue ]; then
    ok "bluegreen: a release failing its readiness is stopped; blue untouched"
else
    fail "bluegreen bad: rc=${rc} colours=$(cat "${work}/bad/colours" 2>/dev/null) $(cat "${work}/bad/out")"
fi
rc="$(bg good)"
if [ "${rc}" = 0 ] && [ "$(tr '\n' ',' <"${work}/good/colours")" = "start green v2,stop blue," ] \
    && [ "$(cat "${work}/good/state/colour")" = green ] && [ "$(cat "${work}/good/state/deployed")" = v2 ]; then
    ok "bluegreen: a good release replaces blue, which drains; green v2 recorded"
else
    fail "bluegreen good: rc=${rc} colours=$(cat "${work}/good/colours" 2>/dev/null) $(cat "${work}/good/out")"
fi
rc="$(bg rollback GREEN_READY=6 WATCH=6)"
if [ "${rc}" != 0 ] && grep -q "rolling back to blue" "${work}/rollback/out" && [ "$(tr '\n' ',' <"${work}/rollback/colours")" = "start green v2,stop blue,start blue v1,stop green," ]; then
    ok "bluegreen: a release failing during the watch rolls back to blue v1"
else
    fail "bluegreen rollback: rc=${rc} colours=$(cat "${work}/rollback/colours" 2>/dev/null) $(cat "${work}/rollback/out")"
fi

# backup (story 50.09 T01): a replica's timer does nothing and exits 0; the leader backs up
cat >"${work}/bin/pgbackrest" <<'STUB'
#!/usr/bin/env bash
echo "$*" >>"${STATE}/pgbackrest"
STUB
chmod +x "${work}/bin/pgbackrest"
backup() { # backup <case> <patroni code> [env...]
    local name=$1 code=$2; shift 2
    rm -rf "${work:?}/${name:?}"; mkdir -p "${work}/${name}"
    env PATH="${work}/bin:${PATH}" STATE="${work}/${name}" CLOUD_HA_ENV=/nonexistent GUARD_CODE="${code}" \
        TEXTFILE_DIR="${work}/${name}/tf" "$@" bash "${here}/bin/backup" >"${work}/${name}/out" 2>&1
    echo $?
}
rc="$(backup replica 503)"
if [ "${rc}" = 0 ] && [ ! -f "${work}/replica/pgbackrest" ]; then ok "backup: a replica does nothing, exits 0"; else fail "backup replica: rc=${rc} $(cat "${work}/replica/out")"; fi
rc="$(backup sunday 200 DOW=7)"
if [ "${rc}" = 0 ] && grep -q -- "--repo=1 --type=full backup" "${work}/sunday/pgbackrest" && grep -q -- "--repo=2 --type=full backup" "${work}/sunday/pgbackrest" \
    && grep -q 'type="full"' "${work}/sunday/tf/maktaba_backup.prom"; then
    ok "backup: the leader takes a full backup to both repositories on Sunday and records it"
else
    fail "backup sunday: rc=${rc} $(cat "${work}/sunday/out")"
fi
rc="$(backup monday 200 DOW=1)"
if grep -q -- "--type=diff backup" "${work}/monday/pgbackrest"; then ok "backup: a differential on the other days"; else fail "backup monday: $(cat "${work}/monday/out")"; fi

# drill-checks (T02): counts off by 5 % fail naming the table; within 1 % and fresh pass
cat >"${work}/bin/psql" <<'STUB'
#!/usr/bin/env bash
db="$1"; q="${*: -1}"
case "${db}:${q}" in
    restored:*"FROM users") echo "${R_USERS:-1000}" ;;
    live:*"FROM users") echo 1000 ;;
    restored:*"FROM servers") echo 500 ;;
    live:*"FROM servers") echo 500 ;;
    *:*"FROM subscriptions") echo 0 ;;
    restored:*relay_nodes) echo "${NEWEST:-1000000}" ;;
    *) echo "psql stub: ${db} ${q}" >&2; exit 1 ;;
esac
STUB
chmod +x "${work}/bin/psql"
drill() { # drill <case> [env...]
    local name=$1; shift
    rm -rf "${work:?}/${name:?}"; mkdir -p "${work}/${name}"
    env PATH="${work}/bin:${PATH}" "$@" bash "${here}/bin/drill-checks" restored live 1000100 >"${work}/${name}/out" 2>&1
    echo $?
}
rc="$(drill off R_USERS=950)"
if [ "${rc}" = 1 ] && grep -q "drill: users: 950 rows restored, 1000 live" "${work}/off/out"; then ok "drill-checks: 5 % fewer users fails, naming the table"; else fail "drill off: rc=${rc} $(cat "${work}/off/out")"; fi
rc="$(drill good R_USERS=995)"
if [ "${rc}" = 0 ]; then ok "drill-checks: within 1 % and fresh passes"; else fail "drill good: $(cat "${work}/good/out")"; fi
rc="$(drill stale NEWEST=999000)"
if [ "${rc}" = 1 ] && grep -q "freshness: the newest restored write is 1100 s older" "${work}/stale/out"; then ok "drill-checks: a stale restore fails"; else fail "drill stale: rc=${rc} $(cat "${work}/stale/out")"; fi

# fip-check (story 50.10): the holder from the API and the leader from Patroni, pushed
mkdir -p "${work}/fipbin"
cat >"${work}/fipbin/curl" <<'STUB'
#!/usr/bin/env bash
url="${*: -1}"
case "${url}" in
    *floating_ips/7) echo '{"floating_ip":{"server":2}}' ;;
    http://10.0.0.3:8008/primary) printf 200 ;;
    *:8008/primary) printf 503 ;;
    */metrics/job/fip_check) cat >"${STATE}/pushed" ;;
esac
STUB
chmod +x "${work}/fipbin/curl"
rm -rf "${work:?}/fip"; mkdir -p "${work}/fip"
env PATH="${work}/fipbin:${PATH}" STATE="${work}/fip" CLOUD_HA_ENV=/nonexistent HCLOUD_TOKEN_FILE="${work}/token" FLOATING_IP_ID=7 \
    NODE_A_ADDR=10.0.0.2 NODE_B_ADDR=10.0.0.3 SERVER_ID_A=1 SERVER_ID_B=2 bash "${here}/bin/fip-check" >/dev/null 2>&1
if grep -q 'fip_holder{node="node-b"} 1' "${work}/fip/pushed" && grep -q 'patroni_primary{node="node-b"} 1' "${work}/fip/pushed" \
    && grep -q 'fip_holder{node="node-a"} 0' "${work}/fip/pushed"; then
    ok "fip-check: pushes the holder and the leader per node"
else
    fail "fip-check: $(cat "${work}/fip/pushed" 2>/dev/null)"
fi

# dns-update (story 50.15): the release's binary under the dns role, the old one kept; a role
# that doesn't stay up gets the old one back and fails; a node without the role only installs
mkdir -p "${work}/dnsbin"
cat >"${work}/dnsbin/systemctl" <<'STUB'
#!/usr/bin/env bash
echo "$*" >>"${STATE}/systemctl"
case "$1" in
    is-enabled) [ "${DNS_ENABLED:-yes}" = yes ] ;;
    is-active) [ "$(cat "${BIN}")" != broken ] ;;
    *) exit 0 ;;
esac
STUB
printf '#!/usr/bin/env bash\nexit 0\n' >"${work}/dnsbin/sleep"
chmod +x "${work}/dnsbin/"*
dnsupdate() { # dnsupdate <case> <new binary's text> [env...]: old binary "v1", then dns-update v2
    local name=$1 text=$2; shift 2
    rm -rf "${work:?}/${name:?}"; mkdir -p "${work}/${name}/releases/v2"
    printf '%s' "${text}" >"${work}/${name}/releases/v2/maktaba-cloud"; chmod +x "${work}/${name}/releases/v2/maktaba-cloud"
    printf v1 >"${work}/${name}/bin"
    env PATH="${work}/dnsbin:${PATH}" STATE="${work}/${name}" RELEASES="${work}/${name}/releases" BIN="${work}/${name}/bin" \
        "$@" bash "${here}/bin/dns-update" v2 >/dev/null 2>&1
    echo $?
}
rc="$(dnsupdate dnsgood good)"
if [ "${rc}" = 0 ] && [ "$(cat "${work}/dnsgood/bin")" = good ] && [ "$(cat "${work}/dnsgood/bin.prev")" = v1 ] \
    && grep -q "restart maktaba-cloud-dns.service" "${work}/dnsgood/systemctl"; then
    ok "dns-update: the new binary runs, the old one kept"
else
    fail "dns-update good: rc=${rc} $(cat "${work}/dnsgood/systemctl" 2>/dev/null)"
fi
rc="$(dnsupdate dnsbad broken)"
if [ "${rc}" = 1 ] && [ "$(cat "${work}/dnsbad/bin")" = v1 ]; then
    ok "dns-update: a role that doesn't stay up gets the old binary back and fails"
else
    fail "dns-update bad: rc=${rc} bin=$(cat "${work}/dnsbad/bin")"
fi
rc="$(dnsupdate dnsoff good DNS_ENABLED=no)"
if [ "${rc}" = 0 ] && [ "$(cat "${work}/dnsoff/bin")" = good ] && ! grep -q restart "${work}/dnsoff/systemctl"; then
    ok "dns-update: without the role enabled, it installs and restarts nothing"
else
    fail "dns-update off: rc=${rc} $(cat "${work}/dnsoff/systemctl" 2>/dev/null)"
fi
rm -rf "${work:?}/dnsmissing"; mkdir -p "${work}/dnsmissing"
env PATH="${work}/dnsbin:${PATH}" STATE="${work}/dnsmissing" RELEASES="${work}/dnsmissing" BIN="${work}/dnsmissing/bin" \
    bash "${here}/bin/dns-update" v9 >/dev/null 2>&1; rc=$?
if [ "${rc}" = 2 ] && [ ! -e "${work}/dnsmissing/bin" ]; then ok "dns-update: a missing release changes nothing"; else fail "dns-update missing: rc=${rc}"; fi

# app-update: the release's binary under the api and worker, the old one kept; on the leader
# the running units restart (the worker first) and one that doesn't stay up gets the old
# binary back and fails; on the standby (nothing running) it only installs
mkdir -p "${work}/appbin"
cat >"${work}/appbin/systemctl" <<'STUB'
#!/usr/bin/env bash
echo "$*" >>"${STATE}/systemctl"
case "$1" in
    is-active)
        if [ -e "${STATE}/restarted" ]; then [ "$(cat "${BIN}")" != broken ]; else [ "${RUNNING:-yes}" = yes ]; fi ;;
    restart) touch "${STATE}/restarted" ;;
    *) exit 0 ;;
esac
STUB
printf '#!/usr/bin/env bash\nexit 0\n' >"${work}/appbin/sleep"
chmod +x "${work}/appbin/"*
appupdate() { # appupdate <case> <new binary's text> [env...]: old binary "v1", then app-update v2
    local name=$1 text=$2; shift 2
    rm -rf "${work:?}/${name:?}"; mkdir -p "${work}/${name}/releases/v2"
    printf '%s' "${text}" >"${work}/${name}/releases/v2/maktaba-cloud"; chmod +x "${work}/${name}/releases/v2/maktaba-cloud"
    printf v1 >"${work}/${name}/bin"
    env PATH="${work}/appbin:${PATH}" STATE="${work}/${name}" RELEASES="${work}/${name}/releases" BIN="${work}/${name}/bin" \
        "$@" bash "${here}/bin/app-update" v2 >/dev/null 2>&1
    echo $?
}
rc="$(appupdate appgood good)"
restarts="$(grep '^restart' "${work}/appgood/systemctl" 2>/dev/null | tr '\n' ' ')"
if [ "${rc}" = 0 ] && [ "$(cat "${work}/appgood/bin")" = good ] && [ "$(cat "${work}/appgood/bin.prev")" = v1 ] \
    && [ "${restarts}" = "restart maktaba-cloud-worker.service restart maktaba-cloud-api.service " ]; then
    ok "app-update: the leader runs the new binary, worker then api restarted, the old one kept"
else
    fail "app-update good: rc=${rc} restarts=${restarts}"
fi
rc="$(appupdate appbad broken)"
if [ "${rc}" = 1 ] && [ "$(cat "${work}/appbad/bin")" = v1 ] \
    && [ "$(grep -c '^restart maktaba-cloud-api.service' "${work}/appbad/systemctl")" = 2 ]; then
    ok "app-update: an api or worker that doesn't stay up gets the old binary back, restarted, and fails"
else
    fail "app-update bad: rc=${rc} bin=$(cat "${work}/appbad/bin") $(cat "${work}/appbad/systemctl" 2>/dev/null)"
fi
rc="$(appupdate appstandby good RUNNING=no)"
if [ "${rc}" = 0 ] && [ "$(cat "${work}/appstandby/bin")" = good ] && ! grep -q restart "${work}/appstandby/systemctl"; then
    ok "app-update: on the standby it installs and restarts nothing"
else
    fail "app-update standby: rc=${rc} $(cat "${work}/appstandby/systemctl" 2>/dev/null)"
fi
rm -rf "${work:?}/appmissing"; mkdir -p "${work}/appmissing"
env PATH="${work}/appbin:${PATH}" STATE="${work}/appmissing" RELEASES="${work}/appmissing" BIN="${work}/appmissing/bin" \
    bash "${here}/bin/app-update" v9 >/dev/null 2>&1; rc=$?
if [ "${rc}" = 2 ] && [ ! -e "${work}/appmissing/bin" ]; then ok "app-update: a missing release changes nothing"; else fail "app-update missing: rc=${rc}"; fi


# api-rollout (story 50.17): one node at a time, each ready before the next; a node that never
# gets ready stops the rollout with the nodes after it untouched. curl answers from
# $STATE/ready-<node> (absent: ready), and the restart command records the order.
mkdir -p "${work}/rollbin"
cat >"${work}/rollbin/curl" <<'STUB'
#!/usr/bin/env bash
url="${*: -1}"; node="${url#http://}"; node="${node%%:*}"
echo "${node}" >>"${STATE}/checks"
[ ! -e "${STATE}/down-${node}" ]
STUB
cat >"${work}/rollbin/restart" <<'STUB'
#!/usr/bin/env bash
echo "$1 $2" >>"${STATE}/restarts"
STUB
printf '#!/usr/bin/env bash\nexit 0\n' >"${work}/rollbin/sleep"
chmod +x "${work}/rollbin/"*
rollout() { # rollout <case> [env...]
    local name=$1; shift
    rm -rf "${work:?}/${name:?}"; mkdir -p "${work}/${name}"
    env PATH="${work}/rollbin:${PATH}" STATE="${work}/${name}" API_NODES="api-1 api-2 api-3" \
        RESTART_CMD="${work}/rollbin/restart" READY_STABLE=0 READY_WITHIN=0 "$@" \
        bash "${here}/bin/api-rollout" v2 >/dev/null 2>&1
    echo $?
}
rc="$(rollout rollgood)"
order="$(tr '\n' ' ' <"${work}/rollgood/restarts")"
if [ "${rc}" = 0 ] && [ "${order}" = "api-1 v2 api-2 v2 api-3 v2 " ]; then
    ok "api-rollout: every node, one at a time, in order"
else
    fail "api-rollout good: rc=${rc} order=${order}"
fi
rm -rf "${work:?}/rollbad"; mkdir -p "${work}/rollbad"; touch "${work}/rollbad/down-api-2"
rc="$(env PATH="${work}/rollbin:${PATH}" STATE="${work}/rollbad" API_NODES="api-1 api-2 api-3" \
    RESTART_CMD="${work}/rollbin/restart" READY_STABLE=0 READY_WITHIN=0 bash "${here}/bin/api-rollout" v2 >/dev/null 2>&1; echo $?)"
order="$(tr '\n' ' ' <"${work}/rollbad/restarts")"
if [ "${rc}" = 1 ] && [ "${order}" = "api-1 v2 api-2 v2 " ]; then
    ok "api-rollout: a node that never gets ready stops it; the next nodes keep their release"
else
    fail "api-rollout bad: rc=${rc} order=${order}"
fi

if [ "${fails}" -ne 0 ]; then echo "${fails} failed"; exit 1; fi
echo "all passed"

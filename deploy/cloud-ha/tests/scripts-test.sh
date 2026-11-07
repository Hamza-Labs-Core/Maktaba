#!/usr/bin/env bash
# Tests for deploy/cloud-ha/bin (story 50.07 T01–T03, T08), with stubbed curl, patronictl,
# logger and sleep: on-role-change as primary moves the Floating IP once and starts the cloud
# processes, retries a 5xx, and raises fip_assign_failed after the retry window; as a replica
# it stops them and never calls the API; switchover refuses a lagging or unhealthy candidate
# and runs patronictl when every check passes. Needs bash and jq; touches only a temp dir.
set -uo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT
fails=0
ok()   { printf '  ok    %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }

mkdir -p "${work}/bin"
# curl: the Hetzner API answers the codes in $CODES one per call ("201" when empty), and each
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
    printf '%s' "${GUARD_CODE:-200}"
    exit 0
fi
if [[ "${url}" == *floating_ips* ]]; then
    echo "$*" >>"${STATE}/calls"
    n="$(wc -l <"${STATE}/calls" | tr -d ' ')"
    code="$(sed -n "${n}p" <<<"${CODES:-}" | tr -d ' ')"
    printf '%s' "${code:-201}"
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
role refused primary CODES="$(printf '503\n%.0s' $(seq 1 40))" FIP_RETRY_FOR=15 FIP_RETRY_EVERY=1
if grep -q '^fip_assign_failed .*DNS-swap runbook' "${work}/refused/alerts" 2>/dev/null && [ "$(cat "${work}/refused/active")" = start ]; then
    ok "refused: fip_assign_failed after the retry window, with the runbook; processes started"
else
    fail "refused: alerts=$(cat "${work}/refused/alerts" 2>/dev/null) out=$(cat "${work}/refused/out")"
fi

# T02: replica: processes stopped, no API call
for r in replica demoted; do
    role "${r}" "${r}"
    if [ "$(count "${work}/${r}/calls")" = 0 ] && [ "$(cat "${work}/${r}/active")" = stop ]; then
        ok "${r}: stopped, no assign call"
    else
        fail "${r}: calls=$(count "${work}/${r}/calls") active=$(cat "${work}/${r}/active" 2>/dev/null)"
    fi
done

# T03: switchover
cat >"${work}/bin/patronictl" <<'STUB'
#!/usr/bin/env bash
case "$*" in
    *"list -f json"*) cat "${STATE}/members" ;;
    *switchover*) echo "$*" >>"${STATE}/switched"
        jq '[.[] | if .Member == "node-b" then .Role = "Leader" else .Role = "Replica" end]' "${STATE}/members" >"${STATE}/m2" && mv "${STATE}/m2" "${STATE}/members" ;;
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

if [ "${fails}" -ne 0 ]; then echo "${fails} failed"; exit 1; fi
echo "all passed"

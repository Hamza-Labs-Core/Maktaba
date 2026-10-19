#!/usr/bin/env bash
# Multi-machine Maktaba on one host (Epic 31): a scratch home and two joined machines,
# then the checks in check.py. Never touches the live system: its own database in the
# Postgres container, its own API on free loopback ports, its own HOME for every process.
#
#   tools/dev/cluster-e2e/run.sh            set up, check, tear down (exit 0 = all passed)
#   E2E_KEEP=1 tools/dev/cluster-e2e/run.sh keep everything running afterwards (stop.sh)
#
# See README.md for what it builds and checks. Bash 3.2+ (macOS /bin/bash works).
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO=$(cd "$HERE/../../.." && pwd)
# shellcheck source=lib.sh
. "$HERE/lib.sh"

E2E_KEEP=${E2E_KEEP:-0}
E2E_VENV=${E2E_VENV:-$HERE/.venv}
# --stages for the machines: probe does the real work (ffprobe through the machine's own
# path map); the home-only stages are asked for on purpose: a machine must still refuse them
E2E_NODE_STAGES=${E2E_NODE_STAGES:-probe,thumb_small,review_pages,index}
# the home's own worker serves a home-only stage, so the checks see who pulls it
E2E_HOME_STAGES=${E2E_HOME_STAGES:-thumb_small}
E2E_API_PORT=${E2E_API_PORT:-8190}
E2E_ADMIN_PORT=${E2E_ADMIN_PORT:-9290}
E2E_OLLAMA_PORT=${E2E_OLLAMA_PORT:-11500}
E2E_JOIN_TIMEOUT=${E2E_JOIN_TIMEOUT:-60}

rand_hex() { od -An -N"$1" -tx1 /dev/urandom | tr -d ' \n'; }

# ---------------------------------------------------------------- preflight
for tool in docker go goose uv ffmpeg ffprobe curl; do
  command -v "$tool" >/dev/null 2>&1 || e2e_die "$tool is not on PATH (see README.md prerequisites)"
done
docker exec "$E2E_PG_CONTAINER" true 2>/dev/null ||
  e2e_die "the Postgres container $E2E_PG_CONTAINER is not running"
[[ $(e2e_psql postgres -Atc "SELECT rolsuper OR rolcreaterole FROM pg_roles WHERE rolname = current_user") == t ]] ||
  e2e_die "$E2E_PG_USER may not create roles in $E2E_PG_CONTAINER: a home could not take machines"

RID=$(rand_hex 4)
e2e_valid_rid "$RID" || e2e_die "could not make a run id"
DB=cluster_e2e_$RID
e2e_valid_db "$DB" || e2e_die "bad database name $DB"
RUN=$E2E_ROOT/$RID
mkdir -p "$RUN"/bin "$RUN"/logs "$RUN"/migrations "$RUN"/nas/export/videos \
  "$RUN"/nas/export/shows "$RUN"/home/HOME "$RUN"/home/data "$RUN"/machine-a/home \
  "$RUN"/machine-a/mnt "$RUN"/machine-b/home
STATE=$RUN/state.env
: >"$RUN/pids"
e2e_state_set "$STATE" RID "$RID"
e2e_state_set "$STATE" DB "$DB"

FAILED=1
on_exit() {
  local rc=$?
  trap - EXIT INT TERM
  if [[ $rc == 130 || $rc == 143 ]]; then
    e2e_log "interrupted"
  elif [[ $FAILED != 0 && $rc != 0 ]]; then
    e2e_log "FAILED (exit $rc); the last lines of each log:"
    local f
    for f in "$RUN"/logs/*.log; do
      [[ -s $f ]] || continue
      printf '\n----- %s -----\n' "$(basename "$f")" >&2
      tail -n 25 "$f" >&2 || true
    done
    printf '\n' >&2
  fi
  if [[ $E2E_KEEP == 1 ]]; then
    e2e_log "kept: run dir $RUN, database $DB, processes in $RUN/pids"
    e2e_log "clean up with: $HERE/stop.sh $RUN"
  else
    e2e_log "cleaning up run $RID (processes, database $DB, machine roles, $RUN)"
    e2e_cleanup_run "$RUN" || true
  fi
  exit "$rc"
}
trap on_exit EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
e2e_log "run $RID: database $DB, files in $RUN"

# -------------------------------------------------------------- scratch database
e2e_psql postgres -c "CREATE DATABASE \"$DB\"" >/dev/null
PG_DSN="postgres://$E2E_PG_USER:$E2E_PG_PASSWORD@$E2E_PG_HOST:$E2E_PG_PORT/$DB?sslmode=disable"

# slot 0000 the way the API applies it (outside goose, which refuses version 0), then goose
# over a temp copy of the Postgres files (the .sqlite.sql siblings left out)
sed '/^-- +goose Down/,$d' "$REPO/shared/db/migrations/0000_schema_version.sql" | e2e_psql "$DB" >/dev/null
for f in "$REPO"/shared/db/migrations/[0-9][0-9][0-9][0-9]_*.sql; do
  case $(basename "$f") in 0000_* | *.sqlite.sql) continue ;; esac
  cp "$f" "$RUN/migrations/"
done
e2e_log "applying $(ls "$RUN/migrations" | wc -l | tr -d ' ') migrations with goose"
goose -dir "$RUN/migrations" postgres "$PG_DSN" up >"$RUN/logs/migrate.log" 2>&1 ||
  e2e_die "goose up failed (see $RUN/logs/migrate.log)"

# Instrumentation, in this database only: who claimed which job (a finished job's
# claimed_by is cleared, so the checks read the claims from here).
e2e_psql "$DB" >/dev/null <<'SQL'
SET client_min_messages = warning;
CREATE TABLE IF NOT EXISTS e2e_claims (
    id          bigserial PRIMARY KEY,
    job_id      bigint NOT NULL,
    stage       text NOT NULL,
    subject_id  uuid,
    claimed_by  text NOT NULL,
    at          timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE OR REPLACE FUNCTION e2e_record_claim() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
BEGIN
    IF NEW.claimed_by IS NOT NULL AND NEW.claimed_by IS DISTINCT FROM OLD.claimed_by THEN
        INSERT INTO e2e_claims (job_id, stage, subject_id, claimed_by)
        VALUES (NEW.id, NEW.stage, NEW.subject_id, NEW.claimed_by);
    END IF;
    RETURN NEW;
END
$f$;
DROP TRIGGER IF EXISTS e2e_record_claim ON processing_jobs;
CREATE TRIGGER e2e_record_claim AFTER UPDATE OF claimed_by ON processing_jobs
    FOR EACH ROW EXECUTE FUNCTION e2e_record_claim();
SQL

# ------------------------------------------------------------------ binaries
e2e_log "building the API"
(cd "$REPO/api" && go build -o "$RUN/bin/maktaba-api" .) >"$RUN/logs/build.log" 2>&1 ||
  e2e_die "go build failed (see $RUN/logs/build.log)"
e2e_log "syncing the harness's pipeline venv ($E2E_VENV)"
# its own environment: never the checkout's pipeline/.venv that a running stack uses
(cd "$REPO/pipeline" && UV_PROJECT_ENVIRONMENT=$E2E_VENV uv sync --all-extras --frozen) \
  >"$RUN/logs/venv.log" 2>&1 || e2e_die "uv sync failed (see $RUN/logs/venv.log)"
PY=$E2E_VENV/bin/python
[[ -x $PY ]] || e2e_die "no python in $E2E_VENV"

# -------------------------------------------------------------- free ports
free_port() {
  "$PY" - "$@" <<'PY'
import socket, sys
avoid = {5182, 5432, 8000, 8080, 8081, 9100, 11434, 50051} | {int(a) for a in sys.argv[2:]}
p = int(sys.argv[1])
while True:
    if p not in avoid:
        s = socket.socket()
        try:
            s.bind(("127.0.0.1", p))
            print(p)
            break
        except OSError:
            pass
        finally:
            s.close()
    p += 1
PY
}
API_PORT=$(free_port "$E2E_API_PORT")
ADMIN_PORT=$(free_port "$E2E_ADMIN_PORT" "$API_PORT")
OLLAMA_PORT=$(free_port "$E2E_OLLAMA_PORT" "$API_PORT" "$ADMIN_PORT")
API=http://127.0.0.1:$API_PORT
TOKEN=$(rand_hex 24)
e2e_state_set "$STATE" API "$API"

# ----------------------------------------------------- the share and its library
SHARE=$RUN/nas/export # the NAS export; the database knows it as /share/
DB_SHARE=/share
e2e_log "making the library (ffmpeg)"
NCLIPS=0
for spec in videos/clip-01:1:0 videos/clip-02:2:40 videos/clip-03:1:80 videos/clip-04:2:120 \
  videos/clip-05:1:160 videos/clip-06:2:200 videos/clip-07:1:240 videos/clip-08:2:280 \
  shows/episode-01:1:20 shows/episode-02:2:60; do
  rel=${spec%%:*}
  rest=${spec#*:}
  ffmpeg -nostdin -loglevel error -y -f lavfi \
    -i "testsrc=size=160x90:rate=10:duration=${rest%%:*},hue=h=${rest#*:}" \
    -c:v mpeg4 -q:v 8 "$SHARE/$rel.mp4"
  NCLIPS=$((NCLIPS + 1))
done
# machine A sees the share as a mounted directory (MAKTABA_STORAGE_HINTS, like a Linux
# /mnt mount); machine B through a symlink in its home (the macOS ~/nas-nfs shape)
MOUNT_A=$RUN/machine-a/mnt/nas
MOUNT_B=$RUN/machine-b/home/nas-nfs
ln -s "$SHARE" "$MOUNT_A"
ln -s "$SHARE" "$MOUNT_B"

"$PY" - "$SHARE" "$DB_SHARE" >"$RUN/seed.sql" <<'PY'
import hashlib, os, sys, uuid
from datetime import datetime, timezone
share, db_share = sys.argv[1], sys.argv[2]
def q(s):
    return "'" + s.replace("'", "''") + "'"
print("BEGIN;")
for lib in ("videos", "shows"):
    lib_id = str(uuid.uuid4())
    root = f"{db_share}/{lib}"
    print(f"INSERT INTO libraries (id, name, roots) VALUES ({q(lib_id)}, {q('E2E ' + lib)}, ARRAY[{q(root)}]);")
    print(
        "INSERT INTO library_roots (library_id, path, path_canonical) "
        f"VALUES ({q(lib_id)}, {q(root)}, {q(root)});"
    )
    for name in sorted(os.listdir(os.path.join(share, lib))):
        local = os.path.join(share, lib, name)
        st = os.stat(local)
        with open(local, "rb") as fh:
            digest = hashlib.sha256(fh.read()).hexdigest()
        mtime = datetime.fromtimestamp(st.st_mtime, timezone.utc).isoformat()
        print(
            "INSERT INTO videos (library_id, content_hash, path, filename, size_bytes, mtime, title) "
            f"VALUES ({q(lib_id)}, {q(digest)}, {q(root + '/' + name)}, {q(name)}, "
            f"{st.st_size}, {q(mtime)}, {q(name)});"
        )
print("COMMIT;")
PY
e2e_psql "$DB" <"$RUN/seed.sql" >/dev/null
e2e_log "library: $NCLIPS clips under $SHARE (database form $DB_SHARE/...)"

# ------------------------------------------------------------------ helpers
BASE_ENV=(
  "PATH=$PATH" "TMPDIR=${TMPDIR:-/tmp}" "LANG=en_US.UTF-8" "TERM=dumb"
  "PYTHONUNBUFFERED=1" "PYTHONDONTWRITEBYTECODE=1" "NO_PROXY=*" "no_proxy=*" "NO_COLOR=1"
  # never fetch a model: a stage that would is a bug in this harness, not a download
  "HF_HUB_OFFLINE=1" "TRANSFORMERS_OFFLINE=1"
)
# start NAME LOG -- ENV... -- CMD...: a background process under a clean environment
# (env -i: nothing of the caller's MAKTABA_* / DATABASE_URL leaks in)
start() {
  local name=$1 log=$2
  shift 2
  [[ ${1:-} == -- ]] && shift
  local envs=()
  while [[ $# -gt 0 && $1 != -- ]]; do
    envs+=("$1")
    shift
  done
  shift
  env -i "${BASE_ENV[@]}" ${envs[@]+"${envs[@]}"} "$@" >>"$log" 2>&1 </dev/null &
  local pid=$!
  printf '%s %s\n' "$name" "$pid" >>"$RUN/pids"
  eval "PID_$name=$pid"
}
API_STATUS=000
API_BODY=
api_call() { # METHOD PATH [BODY] -> $API_STATUS, $API_BODY (no subshell)
  local out
  out=$(mktemp "$RUN/.api.XXXXXX")
  API_STATUS=$(curl -sS --noproxy '*' -o "$out" -w '%{http_code}' -X "$1" \
    -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
    ${3:+--data "$3"} "$API$2") || API_STATUS=000
  API_BODY=$(cat "$out")
  rm -f "$out"
}
json() { "$PY" -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
wait_until() { # SECONDS DESCRIPTION CMD...
  local secs=$1 what=$2 i=0
  shift 2
  while ! "$@" >/dev/null 2>&1; do
    i=$((i + 1))
    [[ $i -ge $((secs * 2)) ]] && e2e_die "timed out after ${secs}s waiting for $what"
    sleep 0.5
  done
}
alive() { kill -0 "$1" 2>/dev/null && [[ $(ps -o stat= -p "$1" 2>/dev/null) != Z* ]]; }
upper() { printf '%s' "$1" | tr 'a-z' 'A-Z'; }

# ------------------------------------------------------------------ the home
API_ENV=(
  "HOME=$RUN/home/HOME"
  "DATABASE_URL=$PG_DSN"
  "MAKTABA_ENV=dev"
  "MAKTABA_HTTP_ADDR=127.0.0.1:$API_PORT"
  "MAKTABA_ADMIN_ADDR=127.0.0.1:$ADMIN_PORT"
  "MAKTABA_ADMIN_TOKEN=$TOKEN"
  "MAKTABA_DISCOVERY_MDNS=0"
  "MAKTABA_UPDATE_DISABLE=1"
  "MAKTABA_DATA_DIR=$RUN/home/data"
  "MAKTABA_CLUSTER_PG_PORT=$E2E_PG_PORT"
  # phase 2 (the cluster channel): the share as the home sees it and where its cluster
  # files go; a phase-1 API ignores them
  "MAKTABA_SHARE_ROOT=$DB_SHARE"
  "MAKTABA_PATH_MAP=$DB_SHARE/=$SHARE/"
  "MAKTABA_CLUSTER_DIR=$SHARE/maktaba/cluster"
)
e2e_log "starting the API on $API (admin 127.0.0.1:$ADMIN_PORT)"
start api "$RUN/logs/api.log" -- "${API_ENV[@]}" -- "$RUN/bin/maktaba-api" serve
api_ready() { alive "$PID_api" && [[ $(curl -s --noproxy '*' -o /dev/null -w '%{http_code}' "$API/healthz") == 200 ]]; }
wait_until 60 "the API" api_ready
api_call GET /api/cluster
[[ $API_STATUS == 200 ]] || e2e_die "GET /api/cluster answered $API_STATUS: $API_BODY"
[[ $(json 'd["home"]["roles_supported"]' <<<"$API_BODY") == True ]] ||
  e2e_die "the home cannot give machines roles: $(json 'd["home"].get("roles_reason")' <<<"$API_BODY")"

# The home's own worker: it syncs the stage registry (a machine never does) and serves the
# home-only stage(s) the checks watch. Same HOME as the API (phase 2: the home agent key).
HOME_ENV=(
  "HOME=$RUN/home/HOME"
  # phase 2: a home process dials its own API; never let it default to the live :8080
  "MAKTABA_API_URL=$API"
  "MAKTABA_HOME_URL=$API"
  "MAKTABA_OLLAMA_URL=http://127.0.0.1:9"
  "MAKTABA_LLM_ENDPOINTS="
  "MAKTABA_SHARE_ROOT=$DB_SHARE"
  "MAKTABA_CLUSTER_DIR=$SHARE/maktaba/cluster"
)
HOME_WORKER=e2e-$RID-home-worker
e2e_log "starting the home's worker (stages: $E2E_HOME_STAGES)"
start home "$RUN/logs/worker-home.log" -- "${HOME_ENV[@]}" -- \
  "$PY" -m maktaba_pipeline run --database-url "$PG_DSN" --host "e2e-$RID-home" \
  --worker-id "$HOME_WORKER" --path-map "$DB_SHARE/=$SHARE/" \
  --stages "$E2E_HOME_STAGES" --claim-poll-sec 0.5 --claim-poll-max-sec 2
stages_synced() {
  alive "$PID_home" &&
    [[ $(e2e_psql "$DB" -Atc "SELECT count(*) FROM job_stages WHERE stage = 'probe'") == 1 ]] &&
    [[ $(e2e_psql "$DB" -Atc "SELECT count(*) FROM workers WHERE id = '$HOME_WORKER'") == 1 ]]
}
wait_until 120 "the home's worker to sync the stage registry" stages_synced

e2e_log "starting a fake Ollama for machine B on 127.0.0.1:$OLLAMA_PORT"
start ollama_b "$RUN/logs/fake-ollama-b.log" -- -- \
  "$PY" "$HERE/fake_ollama.py" --port "$OLLAMA_PORT" --machine "e2e-$RID-b" \
  --requests "$RUN/fake-ollama-b.requests.jsonl"
wait_until 15 "the fake Ollama" curl -sf --noproxy '*' "http://127.0.0.1:$OLLAMA_PORT/api/version"

# ------------------------------------------------------------ join + approve
api_call PUT /api/cluster/settings '{"accept_joins":true}'
[[ $API_STATUS == 200 ]] || e2e_die "could not open joins ($API_STATUS): $API_BODY"

# One machine's environment: its own HOME, node config, mount and LLM runtime.
machine_env() { # a|b -> one VAR=value per line
  local m=$1
  printf '%s\n' "HOME=$RUN/machine-$m/home" "MAKTABA_NODE_CONFIG=$RUN/machine-$m/node.json" \
    "MAKTABA_LLM_ENDPOINTS="
  if [[ $m == a ]]; then
    # no LLM runtime on A: a closed port, never a real Ollama on this host
    printf '%s\n' "MAKTABA_STORAGE_HINTS=$MOUNT_A" "MAKTABA_OLLAMA_URL=http://127.0.0.1:9"
  else
    printf '%s\n' "MAKTABA_OLLAMA_URL=http://127.0.0.1:$OLLAMA_PORT"
    # macOS finds ~/nas-nfs by itself; elsewhere home symlinks are no candidates
    [[ $(uname -s) == Darwin ]] || printf '%s\n' "MAKTABA_STORAGE_HINTS=$MOUNT_B"
  fi
}

code_shown() { grep -q 'Pairing code:' "$1"; }
exited() { ! alive "$1"; }
join_machine() { # a|b
  local m=$1 M name log line node_id code role pid pid_var envs=()
  M=$(upper "$m")
  name=e2e-$RID-$m
  log=$RUN/logs/join-$m.log
  while IFS= read -r line; do envs+=("$line"); done < <(machine_env "$m")
  e2e_log "machine $M: join --home $API --name $name"
  start "join_$m" "$log" -- "${envs[@]}" -- "$PY" -m maktaba_pipeline join --home "$API" --name "$name"
  pid_var=PID_join_$m
  pid=${!pid_var}
  wait_until "$E2E_JOIN_TIMEOUT" "machine $M's pairing code" code_shown "$log"
  code=$(sed -n 's/.*Pairing code: *\([0-9]\{3\}\) *\([0-9]\{3\}\).*/\1\2/p' "$log" | head -1)
  [[ $code =~ ^[0-9]{6}$ ]] || e2e_die "no pairing code in $log"
  node_id=$(json 'd["node_id"]' <"$RUN/machine-$m/node.json")
  e2e_state_set "$STATE" "NODE_$M" "$node_id"
  e2e_state_set "$STATE" "ROLE_$M" "maktaba_node_$(printf '%s' "$node_id" | tr -d '-')"
  api_call GET /api/cluster
  [[ $(json "next((n['state'] for n in d['nodes'] if n['id'] == '$node_id'), 'missing')" <<<"$API_BODY") == pending ]] ||
    e2e_die "machine $M ($node_id) is not a pending request on the home"
  e2e_log "machine $M: pairing code $code, node $node_id -> approve"
  api_call POST "/api/cluster/nodes/$node_id/approve" "{\"code\":\"$code\"}"
  [[ $API_STATUS == 200 ]] || e2e_die "approve answered $API_STATUS: $API_BODY"
  role=$(json 'd["db_role"]' <<<"$API_BODY")
  e2e_valid_role "$role" || e2e_die "approve gave no machine role: $API_BODY"
  wait_until "$E2E_JOIN_TIMEOUT" "machine $M's join to finish" exited "$pid"
  wait "$pid" || e2e_die "machine $M's join exited with an error (see $log)"
  grep -q '"database_url": "postgres' "$RUN/machine-$m/node.json" ||
    e2e_die "machine $M's node config holds no credentials"
}
join_machine a
join_machine b
api_call PUT /api/cluster/settings '{"accept_joins":false}'

run_machine() { # a|b
  local m=$1 line envs=()
  while IFS= read -r line; do envs+=("$line"); done < <(machine_env "$m")
  e2e_log "machine $(upper "$m"): run (stages asked for: $E2E_NODE_STAGES)"
  # no --database-url and no MAKTABA_DATABASE_URL: it runs as the node it joined as
  start "node_$m" "$RUN/logs/node-$m.log" -- "${envs[@]}" -- \
    "$PY" -m maktaba_pipeline run --host "e2e-$RID-$m" --stages "$E2E_NODE_STAGES" \
    --claim-poll-sec 0.5 --claim-poll-max-sec 2
}
run_machine a
run_machine b

# ------------------------------------------------------------------ checks
env -i "PATH=$PATH" "RID=$RID" "RUN=$RUN" "DB=$DB" "PG_DSN=$PG_DSN" "API=$API" "TOKEN=$TOKEN" \
  "SHARE=$SHARE" "DB_SHARE=$DB_SHARE" "MOUNT_A=$MOUNT_A" "MOUNT_B=$MOUNT_B" \
  "PID_API=$PID_api" "PID_HOME=$PID_home" "PID_A=$PID_node_a" "PID_B=$PID_node_b" \
  "HOME_WORKER=$HOME_WORKER" "HOME_STAGES=$E2E_HOME_STAGES" "NODE_STAGES=$E2E_NODE_STAGES" \
  "OLLAMA_URL=http://127.0.0.1:$OLLAMA_PORT" "OLLAMA_REQUESTS=$RUN/fake-ollama-b.requests.jsonl" \
  "$PY" - "$RUN/context.json" <<'PY'
import json, os, sys
e = os.environ
run = e["RUN"]
json.dump({
    "rid": e["RID"], "run_dir": run, "db": e["DB"], "pg_dsn": e["PG_DSN"],
    "api": e["API"], "token": e["TOKEN"], "share": e["SHARE"], "db_share": e["DB_SHARE"],
    "mounts": {"a": e["MOUNT_A"], "b": e["MOUNT_B"]},
    "homes": {"home": f"{run}/home/HOME", "a": f"{run}/machine-a/home", "b": f"{run}/machine-b/home"},
    "node_configs": {"a": f"{run}/machine-a/node.json", "b": f"{run}/machine-b/node.json"},
    "names": {"a": f"e2e-{e['RID']}-a", "b": f"e2e-{e['RID']}-b"},
    "pids": {k: int(e[f"PID_{k.upper()}"]) for k in ("api", "home", "a", "b")},
    "home_worker": e["HOME_WORKER"],
    "home_stages": e["HOME_STAGES"].split(","),
    "node_stages": e["NODE_STAGES"].split(","),
    "ollama": {"url": e["OLLAMA_URL"], "requests": e["OLLAMA_REQUESTS"]},
    "logs": f"{run}/logs",
}, open(sys.argv[1], "w"), indent=2)
PY
e2e_log "running the checks"
# in the background so a signal to this script cleans up at once (bash runs a trap
# only after a foreground child ends, but interrupts a wait)
CHECK_ENV=("HOME=$RUN/home/HOME" "E2E_REQUIRE_PHASE2=${E2E_REQUIRE_PHASE2:-0}")
for v in E2E_SWITCH_TIMEOUT E2E_BATCH_TIMEOUT E2E_REVOKE_TIMEOUT E2E_FILES_TIMEOUT E2E_LLM_TIMEOUT; do
  if [[ -n ${!v:-} ]]; then CHECK_ENV+=("$v=${!v}"); fi
done
env -i "${BASE_ENV[@]}" "${CHECK_ENV[@]}" "$PY" "$HERE/check.py" --context "$RUN/context.json" </dev/null &
CHECK_PID=$!
printf 'check %s\n' "$CHECK_PID" >>"$RUN/pids"
set +e
wait "$CHECK_PID"
rc=$?
set -e
[[ $rc == 0 ]] && FAILED=0
exit "$rc"

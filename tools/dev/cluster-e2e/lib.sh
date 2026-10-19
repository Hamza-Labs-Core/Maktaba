# Shared helpers for tools/dev/cluster-e2e (sourced by run.sh and stop.sh; bash 3.2+).
#
# Everything a run creates is named after its run id (RID, 8 lowercase alphanumerics):
#   database   cluster_e2e_<RID>         in the Postgres container (never "maktaba")
#   run dir    $E2E_ROOT/<RID>/          temp share, homes, logs, binaries, state.env
#   processes  their command lines carry "$E2E_ROOT/<RID>/" or "e2e-<RID>-"
#   roles      maktaba_node_<32 hex>     only the ones this run's machines were given
# so a cleanup can always find, and only find, what that run made.

E2E_PG_CONTAINER=${E2E_PG_CONTAINER:-maktaba-postgres-1}
E2E_PG_USER=${E2E_PG_USER:-maktaba}
E2E_PG_PASSWORD=${E2E_PG_PASSWORD:-maktaba}
E2E_PG_HOST=${E2E_PG_HOST:-127.0.0.1}
E2E_PG_PORT=${E2E_PG_PORT:-5432}
E2E_ROOT=${E2E_ROOT:-${TMPDIR:-/tmp}}
E2E_ROOT=${E2E_ROOT%/}
case $E2E_ROOT in */maktaba-cluster-e2e) ;; *) E2E_ROOT=$E2E_ROOT/maktaba-cluster-e2e ;; esac

e2e_log() { printf '[cluster-e2e %s] %s\n' "$(date +%H:%M:%S)" "$*" >&2; }
e2e_die() { e2e_log "ERROR: $*"; exit 1; }

# A scratch database name, or nothing at all: every DROP goes through this.
e2e_valid_db() { [[ $1 =~ ^cluster_e2e_[a-z0-9]{4,32}$ ]]; }
e2e_valid_role() { [[ $1 =~ ^maktaba_node_[0-9a-f]{32}$ ]]; }
e2e_valid_rid() { [[ $1 =~ ^[a-z0-9]{4,32}$ ]]; }

# psql as the container's superuser; $1 = database, the rest = psql args.
e2e_psql() {
  local db=$1
  shift
  docker exec -i "$E2E_PG_CONTAINER" psql -X -q -v ON_ERROR_STOP=1 -U "$E2E_PG_USER" -d "$db" "$@"
}

e2e_db_exists() {
  e2e_valid_db "$1" || return 1
  [[ $(e2e_psql postgres -Atc "SELECT 1 FROM pg_database WHERE datname = '$1'" 2>/dev/null) == 1 ]]
}

# The roles this database's machines were given (cluster_nodes.db_role), one per line.
e2e_db_roles() {
  e2e_db_exists "$1" || return 0
  e2e_psql "$1" -Atc "SELECT db_role FROM cluster_nodes WHERE db_role IS NOT NULL" 2>/dev/null || true
}

# Append KEY=value (shell-quoted) to a run's state file.
e2e_state_set() {
  local file=$1 key=$2 value=$3
  printf '%s=%q\n' "$key" "$value" >>"$file"
}

# Kill every process of a run (TERM, then KILL after a grace): the recorded pids and
# anything else whose command line names the run. A pid is only signalled while its
# command line still carries the run's marks, so a reused pid is never hit.
e2e_kill_run() {
  local run=$1 rid=$2 pids="" p cmd
  e2e_valid_rid "$rid" || return 0
  if [[ -f $run/pids ]]; then
    pids=$(awk '{print $2}' "$run/pids" 2>/dev/null | tr '\n' ' ')
  fi
  pids="$pids $(pgrep -f -- "$run/" 2>/dev/null | tr '\n' ' ') $(pgrep -f -- "e2e-$rid-" 2>/dev/null | tr '\n' ' ')"
  local live=""
  for p in $pids; do
    [[ $p =~ ^[0-9]+$ ]] || continue
    [[ $p == "$$" || $p == "$PPID" ]] && continue
    case " $live " in *" $p "*) continue ;; esac
    cmd=$(ps -o command= -p "$p" 2>/dev/null) || continue
    case $cmd in *"$run/"* | *"e2e-$rid-"*) ;; *) continue ;; esac
    live="$live $p"
  done
  [[ -z $live ]] && return 0
  {
    for p in $live; do kill -TERM "$p" || true; done
    local i any
    for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
      any=0
      for p in $live; do
        if kill -0 "$p" && [[ $(ps -o stat= -p "$p") != Z* ]]; then any=1; fi
      done
      [[ $any == 0 ]] && break
      sleep 0.5
    done
    for p in $live; do
      if kill -0 "$p" && [[ $(ps -o stat= -p "$p") != Z* ]]; then kill -KILL "$p" || true; fi
    done
    # reap our own children (no zombies, no "Terminated" job notices)
    for p in $live; do wait "$p" || true; done
  } 2>/dev/null
  return 0
}

# Drop a scratch database and the machine roles it handed out (plus any roles listed).
e2e_drop_db() {
  local db=$1
  shift
  local roles="$* $(e2e_db_roles "$db" | tr '\n' ' ')" r
  if e2e_db_exists "$db"; then
    e2e_psql postgres -c "DROP DATABASE IF EXISTS \"$db\" WITH (FORCE)" >/dev/null 2>&1 ||
      e2e_log "warning: could not drop database $db"
  fi
  for r in $(printf '%s\n' $roles | sort -u); do
    e2e_valid_role "$r" || continue
    e2e_psql postgres -c "DROP ROLE IF EXISTS \"$r\"" >/dev/null 2>&1 ||
      e2e_log "warning: could not drop role $r"
  done
  return 0
}

# The roles a run's machines were given, from their node configs (saved before the
# join request, so this works even when the database is already gone).
e2e_run_roles() {
  local run=$1 f id
  for f in "$run"/machine-*/node.json; do
    [[ -f $f ]] || continue
    id=$(sed -n 's/.*"node_id": *"\([0-9a-f-]*\)".*/\1/p' "$f" | head -1 | tr -d '-')
    [[ ${#id} == 32 ]] && printf 'maktaba_node_%s\n' "$id"
  done
  if [[ -f $run/state.env ]]; then
    sed -n 's/^ROLE_[A-Z]*=\(maktaba_node_[0-9a-f]*\)$/\1/p' "$run/state.env"
  fi
}

# Everything for one run directory: processes, database, roles, files. Idempotent.
e2e_cleanup_run() {
  local run=$1 rid db
  rid=$(basename "$run")
  e2e_valid_rid "$rid" || { e2e_log "refusing to clean $run (not a run directory)"; return 1; }
  db=cluster_e2e_$rid
  e2e_kill_run "$run" "$rid"
  e2e_drop_db "$db" $(e2e_run_roles "$run" | tr '\n' ' ')
  case $run in
    "$E2E_ROOT"/*) rm -rf "$run" && rmdir "$E2E_ROOT" 2>/dev/null ;;
    *) e2e_log "not removing $run (outside $E2E_ROOT)" ;;
  esac
  return 0
}

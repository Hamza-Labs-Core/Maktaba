#!/usr/bin/env bash
# Tear down cluster-e2e runs: their processes, scratch databases, machine roles, files.
# Idempotent: safe to run any number of times, and after a run that died half-way.
#
#   tools/dev/cluster-e2e/stop.sh RUN_DIR|RID ...   the named run(s)
#   tools/dev/cluster-e2e/stop.sh --all             every run under $E2E_ROOT, plus any
#                                                    cluster_e2e_* database left in the
#                                                    container (and the roles it handed out)
#
# Only ever drops databases named cluster_e2e_<id> and the maktaba_node_* roles that
# those databases' own machines were given; never "maktaba" or another role.
set -uo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
. "$HERE/lib.sh"

usage() {
  sed -n '2,11p' "$0" | sed 's/^# \{0,1\}//' >&2
  exit 2
}
[[ $# -gt 0 ]] || usage

clean_one() { # RUN_DIR or RID
  local arg=${1%/} run rid
  if [[ -d $arg ]]; then
    run=$(cd "$arg" && pwd)
  else
    run=$E2E_ROOT/$arg
  fi
  rid=$(basename "$run")
  e2e_valid_rid "$rid" || { e2e_log "skipping $arg: not a cluster-e2e run"; return 0; }
  e2e_log "cleaning run $rid"
  if [[ -d $run ]]; then
    e2e_cleanup_run "$run"
  else
    # the directory is gone; its database (and the roles in it) may not be
    e2e_kill_run "$run" "$rid"
    e2e_drop_db "cluster_e2e_$rid"
  fi
}

if [[ $1 == --all ]]; then
  if [[ -d $E2E_ROOT ]]; then
    for d in "$E2E_ROOT"/*/; do
      [[ -d $d ]] && clean_one "$d"
    done
  fi
  if docker exec "$E2E_PG_CONTAINER" true 2>/dev/null; then
    for db in $(e2e_psql postgres -Atc "SELECT datname FROM pg_database WHERE datname LIKE 'cluster\_e2e\_%'" 2>/dev/null); do
      e2e_valid_db "$db" || continue
      e2e_log "dropping leftover database $db"
      e2e_kill_run "$E2E_ROOT/${db#cluster_e2e_}" "${db#cluster_e2e_}"
      e2e_drop_db "$db"
    done
  fi
  [[ -d $E2E_ROOT ]] && rmdir "$E2E_ROOT" 2>/dev/null
  e2e_log "done"
  exit 0
fi

for arg in "$@"; do clean_one "$arg"; done
e2e_log "done"
